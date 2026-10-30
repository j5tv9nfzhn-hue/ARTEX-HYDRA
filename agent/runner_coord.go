package agent

// AgentRunner 的真实实现：用 norma/agentcore 的常驻 Session 跑一个 agent，
// 在每一轮之间把 mailbox 里的消息注入会话，并按交卷状态收尾。
//
// ── 为什么基座是 agentcore.Session 而不是 harness.Query ─────────────────────
//
// harness.Query 是**一次性**的：接受一份 Messages 快照，跑完把完整会话放进
// Terminal.Messages。用它做子代理只能得到"一问一答"（norma/subagent 就是这样）。
// 新架构要求 agent 能**在中途收到消息**（instruction 改方向、query 提问），
// 这需要会话常驻、可在两次 Prompt 之间追加一段 user 输入。
// agentcore.Session 正好提供这个：它持有 messages，每次 Prompt 追加一条 user
// 消息后调 harness.Query，结束后把 Terminal.Messages 写回自己。
//
// ── 为什么不自己写事件循环 ─────────────────────────────────────────────────
//
// capture.go 的 captureRunSession 已经把这件做全了：流式增量合并、tool_use ↔
// tool_result 关联、中断补终止行、拦截审计（intercept.WithTrace）、live 推送、
// 用量折算。自己再写一遍等于把 200 行有真实缺陷修复历史的代码换成一份没跑过的
// 复制品——这正是本次重构要消除的那类重复。
//
// 所以这里只做 harness **不提供**的三件事：
//  1. 每轮把 mailbox 注入会话（接收指令的唯一时点）；
//  2. 判定是否已交卷（模型调了 submit_report / finish_task）；
//  3. 没交卷就继续——沉默不等于完成。
//
// 它们都只依赖 Coordinator，不依赖任何数据库表。

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// SessionRunner 是 AgentRunner 的默认实现。
//
// 它不持有 Coordinator：协调器通过 Run 的入参进来（一个 runner 服务一张图）。
// 它持有的是一份"造会话"的模板：Provider、工具面、工作目录、预算等，
// 全部来自任务的既有配置（与旧 worker 同源），不新增配置项。
type SessionRunner struct {
	Provider llm.Provider

	// ToolsFor 返回某个 agent 可用的工具面。nil 时用 BaseTools。
	// 做成回调是因为工具面**绑定到 agent 而不是 runner**：根与子的领域工具面
	// 可以不同（例如根只给只读 + 协调工具），而预算、provider 是共享的。
	ToolsFor  func(role string, isRoot bool) []actool.CoreTool
	BaseTools []actool.CoreTool

	WorkDir            string
	ToolOutputDir      string
	MaxToolOutputChars int

	MaxTurns           int
	MaxDuration        time.Duration
	MaxTokens          int
	MaxConcurrency     int
	NonStreaming       bool
	PermissionMode     permission.Mode
	AllowedTools       []string
	DisallowedTools    []string
	BashEnv            []string
	Hooks              harness.HookRunner
	Compactor          harness.Compactor
	Settlement         *harness.Settlement
	TokenBudget        int
	EscalateMaxTokens  bool
	DisableBackgroundTasks bool
	SessionIDPrefix    string

	// Window 是该任务的上下文窗口（token）。0 = 未提供，按 ARTEX 的默认窗口。
	//
	// 它同时决定**压缩触发点**与**工具输出进历史的字符上限**（ToolOutputChars：
	// 窗口 1/8，夹 4K–24K）。这两件事都不能写死常数：写死会让大窗口任务每轮
	// 无谓地重发几倍历史，而小窗口任务的工具输出会撑爆窗口。
	Window int

	// RootAgentKey / ChildAgentKey 是根与子在**可见性表**里的身份，不是提示词
	// 里的角色名。默认都是 "worker"，理由见 coord_assembly.go 的常量注释：
	// ToolResolve 用 tools.agents 决定"这个工具给不给这个 agent"，而领域工具
	// 绑定的就是 worker；换身份会让根连 list_findings 都查不到。
	RootAgentKey  string
	ChildAgentKey string

	// RunInfo 是工具调用的归属（任务 / 探索 / 意图）。
	//
	// 工具计量（tool_usage）与技能台账都按它归属到任务；缺失时所有调用会被记成
	// "无归属"，成本无法按任务归因——这正是 server/assembly.go 里那段注释描述的
	// "20,521 次调用只有 50 个 key" 的事故形态。必须在装配**之前**进 ctx。
	RunInfo RunInfo

	// WebFetchProxy / WebFetchCACert 让 WebFetch 走记录代理并信任其 MITM CA。
	//
	// 不接等于：同一个 URL，用 curl 读会被记进流量视图，用 WebFetch 读（它是
	// 原生工具，更常用）不会。取证链上这属于"证据缺失"，不是"少个便利工具"。
	WebFetchProxy  string
	WebFetchCACert string
	// WebSearch 是可选联网搜索（默认关，由设置页与 agent 的 web_search 开关共同决定）。
	WebSearch WebSearchOpts

	// Emit 是活动流出口，直接复用 capture.go 的 db.Activity 口径：
	// 前端 timeline 不需要为新链路改任何协议。
	Emit func(db.Activity)

	// toolset 是收尾工具需要的 *ToolSet 载体（见 coordToolset）。
	toolset *ToolSet
}

// Run 实现 AgentRunner：把一个 agent 跑到交卷。
//
// 外层是"回合循环"：
//   - 内层一次 captureRunSession ＝ 一次 Prompt（跑完模型与工具，可能多轮）；
//   - 外层负责注入 mailbox、判定交卷、没交卷就补一轮。
func (r *SessionRunner) Run(ctx context.Context, spec AgentSpec, agentID string, coord *Coordinator) AgentResult {
	start := time.Now()
	out := AgentResult{State: StateCompleted}

	// 装配一次，用完关闭。装配是重操作（会连 MCP、读 DB 工具表），且
	// **必须正好一次**：每多装配一次就多一组 MCP 子进程，而每一组都要被关掉。
	surf := r.assemble(ctx, spec, coord, agentID)
	defer surf.Cleanup()

	tools := surf.Tools
	sysPrompt := surf.System

	opts := agentcore.Options{
		Provider:               r.Provider,
		SystemPrompt:           sysPrompt,
		DynamicBoundary:        surf.Boundary,
		DeferredTools:          surf.Deferred,
		UnlockSet:              surf.Unlock,
		Tools:                  tools,
		WorkingDir:             r.WorkDir,
		ToolOutputDir:          r.ToolOutputDir,
		MaxToolOutputChars:     surf.MaxToolOutputChars,
		MaxTurns:               spec.EffectiveMaxTurns(r.MaxTurns),
		MaxDuration:            r.MaxDuration,
		MaxTokens:              r.MaxTokens,
		MaxConcurrency:         r.MaxConcurrency,
		NonStreaming:           r.NonStreaming,
		PermissionMode:         permissionModeOrDefault(r.PermissionMode),
		AllowedTools:           r.AllowedTools,
		DisallowedTools:        r.DisallowedTools,
		BashEnv:                r.BashEnv,
		Hooks:                  r.Hooks,
		Compactor:              r.Compactor,
		Settlement:             r.Settlement,
		TokenBudget:            r.TokenBudget,
		EscalateMaxTokens:      r.EscalateMaxTokens,
		DisableBackgroundTasks: r.DisableBackgroundTasks,
		SessionID:              r.sessionIDFor(agentID),

		// 联网：WebFetch 恒开（取证需要），走记录代理 + 信任 CA。
		// WebSearch 只在宿主打开时挂（后端与 key 由设置页给）。
		EnableWebFetch:        true,
		WebFetchProxy:         r.WebFetchProxy,
		WebFetchCACert:        r.WebFetchCACert,
		EnableWebSearch:       r.WebSearch.Enabled,
		WebSearchBackend:      r.WebSearch.Backend,
		BraveSearchAPIKey:     r.WebSearch.BraveKey,
		TavilySearchAPIKey:    r.WebSearch.TavilyKey,
		DeepSeekSearchBaseURL: r.WebSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  r.WebSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   r.WebSearch.DeepSeekModel,
		WebSearchProxy:        r.WebSearch.Proxy,

		// 会话级临时待办：纯规划用，退出即丢。给它在多轮长任务里的价值是
		// "模型自己记账"，不占上下文，也不进数据库。
		Todos: actool.NewTodoStore(),
	}
	// Settlement 的取向：宿主**显式配置**的优先（它有任务级超时词这类上下文，
	// 装配层不知道）；没配时用装配层按角色生成的（根 finish_task / 子
	// submit_report，且只点名确定在面上的工具，见 coordSettlement）。
	if opts.Settlement == nil {
		opts.Settlement = surf.Settlement
	}
	// 压缩必须在 Provider / MaxTokens / NonStreaming 都就位之后装配：
	// attachCompaction 用 opts.Provider 造摘要器，顺序反了会得到一个一压缩就
	// 失败的压缩器，而那种失败在 SDK 里只记一条 warn，看起来像"压缩没触发"。
	// 显式配置了 Compactor 的宿主优先（与 agentcore 的"两者只能设一个"一致）。
	if r.Compactor == nil {
		surf.AttachCompaction(ctx, &opts, "coord-"+coordAgentKeyOf(surf))
	}
	// 调度三元组必须进 ctx：工具调用发生在 harness 的 goroutine 里，只能靠 ctx 传。
	// Spawner 由上游（StartTask / Delegate）注入并全图共享——这里**不能**新建，
	// 否则每个 agent 会得到自己的一份并发额度，上限被放大成 N×Max。
	ctx = WithCoordinator(ctx, coord)
	ctx = WithAgentID(ctx, agentID)
	if _, err := SpawnerFrom(ctx); err != nil {
		s := NewSpawner(coord, r)
		// 与根提示词里的 t.ChildSlots 同源（childSlots 读的就是 MaxConcurrency）。
		// 两处取不同的值会让模型按"还能派"的错觉去派，然后撞并发额度报错。
		s.MaxConcurrent = maxChildSlots
		if r.MaxConcurrency > 0 {
			s.MaxConcurrent = r.MaxConcurrency
		}
		ctx = WithSpawner(ctx, s)
	}

	sess := agentcore.NewSession(opts)
	defer sess.Close()

	// 首轮输入：任务全文。子 agent 看不到父级会话历史，这段话就是它的全部背景。
	prompt := spec.Task
	toolCalls := 0
	// 外层补轮上限：够纠正"忘了交卷"，又不至于在模型彻底跑偏时无限烧钱。
	const maxOuterRounds = 12

	for round := 0; round < maxOuterRounds; round++ {
		if ctx.Err() != nil {
			out.Err = ctx.Err()
			out.State = StateStopped
			return r.finish(out, sess, start, toolCalls)
		}
		coord.MarkTurn(agentID)

		// 统计本轮的 tool_use 数：captureRunSession 回报的是 Activity，
		// 我们只关心数量，用于 Usage 折算与"是否真的干了活"的判定。
		emit := func(a db.Activity) {
			if a.Kind == "tool_use" {
				toolCalls++
			}
			r.forward(a, agentID)
		}
		text, reason, err := captureRunSession(ctx, sess, prompt, emit)

		// 交卷是最常见的出口：模型调了 submit_report / finish_task，
		// Finish 已把状态写进协调器，这里直接收工。
		if v, ok := coord.View(agentID); ok && v.Brief != nil {
			out.State = v.State
			out.Brief = *v.Brief
			return r.finish(out, sess, start, toolCalls)
		}
		if err != nil && ctx.Err() != nil {
			// 取消优先于"没交卷"：这是暂停/删除，不是模型失败。
			out.Err = ctx.Err()
			out.State = StateStopped
			return r.finish(out, sess, start, toolCalls)
		}
		if err != nil {
			out.Err = err
			out.State = StateFailed
			out.Brief = Brief{
				State:   StateFailed,
				Outcome: "open_proof_gap",
				Summary: "执行失败：" + err.Error() + "。最后输出：" + truncateRunes(text, 400),
			}
			return r.finish(out, sess, start, toolCalls)
		}
		if reason == harness.ReasonMaxTurns || reason == harness.ReasonTimeout {
			// 预算用尽仍未交卷：按 open_proof_gap 收尾，而不是伪装成完成。
			// 这条兜底是"没有结论也必须留痕"的最后一道闸。
			out.Brief = Brief{
				State:   StateCompleted,
				Outcome: "open_proof_gap",
				Summary: "预算用尽（" + string(reason) + "），未能在限内给出可判定结论。最后输出：" + truncateRunes(text, 600),
				OpenItems: []string{
					"未能把这条路线推进到可判定的程度（" + string(reason) + "）",
				},
			}
			return r.finish(out, sess, start, toolCalls)
		}

		// 没交卷：先把 mailbox 里的新消息给它（接收指令的唯一时点）。
		if inject := NewMailboxInjector(coord, agentID)(); inject != "" {
			prompt = inject
			continue
		}
		// 没消息也没交卷：沉默不等于完成，补一轮明确要求收尾。
		prompt = "你还没有交卷。现在必须做二选一：\n" +
			"1) 如果这条路线还有明确动作可做，做它，然后交卷；\n" +
			"2) 如果已经做不下去，用 submit_report 如实写清楚：结论是什么、证据是什么、" +
			"哪些东西既不能确认也不能排除（写进 open_items）。\n" +
			"不要用散文描述过程，不要停在半路。"
	}

	out.State = StateFailed
	out.Err = fmt.Errorf("agent %s 在 %d 轮内始终未交卷", agentID, maxOuterRounds)
	out.Brief = Brief{State: StateFailed, Outcome: "open_proof_gap", Summary: out.Err.Error()}
	return r.finish(out, sess, start, toolCalls)
}

// forward 把 capture 产出的 Activity 打上 agent 标签后转给宿主。
func (r *SessionRunner) forward(a db.Activity, agentID string) {
	if r.Emit == nil {
		return
	}
	// Worker 字段旧链路用来区分 planner/worker，这里放 agent id：
	// 前端按 Worker 分组时，新链路自然呈现为"每个 agent 一栏"。
	if a.Worker == "" {
		a.Worker = agentID
	}
	defer func() { _ = recover() }() // 观察者不得影响执行
	r.Emit(a)
}

// finish 把会话用量与工具计数折进结果。
func (r *SessionRunner) finish(out AgentResult, sess *agentcore.Session, start time.Time, toolCalls int) AgentResult {
	u := sess.Usage()
	out.Usage = Usage{
		ToolCalls: toolCalls,
		InputTok:  u.InputTokens,
		OutputTok: u.OutputTokens,
		ElapsedMS: time.Since(start).Milliseconds(),
	}
	out.Brief.Usage = out.Usage
	return out
}

// assemble 是**唯一**的工具面与系统提示来源。
//
// 它做三件事，顺序固定：
//  1. 拼 base：领域工具（宿主给的或 ToolsFor 按角色给的）+ 协调器工具 + 收尾工具；
//  2. 交给 AssembleCoordSurface 一次成面：技能 / MCP / DB 过滤 / 防护层 / 提示分段；
//  3. 记一行对账面（工具数、deferred 数、可见性身份、窗口），供日志核对下发了什么。
//
// 第 2 步是 P2 完全缺失的那一步：P2 把 base 直接当工具面用，等于绕过了整条
// 治理管道（详见 coord_assembly.go 文件头）。
func (r *SessionRunner) assemble(ctx context.Context, spec AgentSpec, coord *Coordinator, agentID string) *CoordSurface {
	isRoot := false
	if v, ok := coord.View(agentID); ok {
		isRoot = v.Root
	}

	// ---- 1. base = 领域工具 + 协调器工具 + 收尾工具 ----
	var base []actool.CoreTool
	if r.ToolsFor != nil {
		// ToolsFor 只负责**领域**工具：协调器工具是本包私有构造的，宿主拿不到。
		base = append(base, r.ToolsFor(firstNonEmpty(spec.Role, "worker"), isRoot)...)
	} else {
		base = append(base, r.BaseTools...)
	}
	// 协调器工具：根与子都需要 spawn/send/wait/graph。
	// 用 ChildCoordTools 而不是 CoordTools(true)：根不该同时拿到 submit_report，
	// 否则一个编排者可以用"子任务交卷"的口气结束整个任务。
	ts := r.coordToolset()
	base = append(base, ts.ChildCoordTools()...)
	if isRoot {
		base = append(base, ts.finishTask())
	} else {
		base = append(base, ts.submitReport())
	}

	// ---- 2. 一次成面 ----
	sysBody := r.sysBodyFor(spec, coord, agentID, isRoot)
	surf := AssembleCoordSurface(ctx, CoordAssembleInput{
		Base:               base,
		SysBody:            sysBody,
		IsRoot:             isRoot,
		RunInfo:            r.runInfoFor(agentID),
		Window:             r.Window,
		RootKey:            r.RootAgentKey,
		ChildKey:           r.ChildAgentKey,
		SettlementDisabled: CoordSettlementDisabled(),
	})

	// ---- 3. 对账面 ----
	// 每个不同的工具面只打一次的活交给上游 logToolFace；这里只打自己这一层
	// 独有的信息（可见性身份与 deferred 规模），因为"为什么子 agent 拿不到
	// 某个工具"最常见的答案就是身份取错了。
	log.Printf("[coord] agent=%s root=%v key=%s tools=%d deferred=%d window=%d",
		agentID, isRoot, surf.AgentKey, len(surf.Tools), len(surf.Deferred), surf.Window)
	return surf
}

// runInfoFor 造工具调用归属。
//
// TaskID 与 ExplorationID 都来自宿主在 Assembler 里注入的 RunInfo（server 层
// 用 t.ExpID 填），IntentID 恒为 0：新链路没有"意图节点"这个实体——子 agent
// 本身就是意图的替代物，它的 id 在 agentID 里，不在 RunInfo 里。
func (r *SessionRunner) runInfoFor(agentID string) RunInfo {
	ri := r.RunInfo
	ri.IntentID = 0
	if ri.SessionID == "" {
		ri.SessionID = agentID
	}
	return ri
}

// sysBodyFor 拼系统提示正文（不含 deferred 段——那一段由 deferredSystem 追加）。
//
// 与旧链路的分工一致：这里只放**稳定的**内容（角色、目标、约束），
// 所有会变的（可用 MCP 清单、发现工作流指引）由装配层追加到最后一段，
// 这样 prompt cache 的前缀才是稳定的。
func (r *SessionRunner) sysBodyFor(spec AgentSpec, coord *Coordinator, agentID string, isRoot bool) string {
	vars := CoordVars{
		Goal:     spec.Task,
		Scope:    scopeOf(spec),
		MaxTurns: spec.EffectiveMaxTurns(r.MaxTurns),
	}
	if isRoot {
		vars.ChildSlots = r.childSlots()
		return RootSystemPrompt(vars)
	}
	return ChildSystemPrompt(vars)
}

// childSlots 是写进根提示词的并行上限。
//
// 与 Spawner.MaxConcurrent 必须**同源**：二者不一致会让模型按"还能派"的错觉
// 去派，然后撞到并发额度报错——这条在 P0 修过一次，不要再从两个地方取值。
func (r *SessionRunner) childSlots() int {
	if r.MaxConcurrency > 0 {
		return r.MaxConcurrency
	}
	return maxChildSlots
}

func coordAgentKeyOf(s *CoordSurface) string {
	if s == nil || s.AgentKey == "" {
		return "worker"
	}
	return s.AgentKey
}

func (r *SessionRunner) sessionIDFor(agentID string) string {
	if r.SessionIDPrefix == "" {
		return ""
	}
	return r.SessionIDPrefix + "-" + agentID
}

// scopeOf 把一份任务的约束折成一行提示。任务里写了就原样用，没写就留空——
// 不要在提示词里编造授权范围。
func scopeOf(spec AgentSpec) string {
	if len(spec.AssetIDs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(spec.AssetIDs))
	for _, id := range spec.AssetIDs {
		parts = append(parts, fmt.Sprintf("#%d", id))
	}
	return "仅限资产 " + strings.Join(parts, ", ")
}

// maxChildSlots 是写进根提示词的并行上限。与 Spawner.MaxConcurrent 同源：
// 二者不一致会让模型按"还能派"的错觉去派，然后撞到并发额度报错。
const maxChildSlots = 4

// ---------- 与宿主既有配置的桥 ----------

// RunnerConfig 从一份"任务级配置"造 runner。参数刻意用基本类型，
// 避免把 server 包的类型引进 agent 包（那会造成双向依赖）。
type RunnerConfig struct {
	Provider               llm.Provider
	BaseTools              []actool.CoreTool
	ToolsFor               func(role string, isRoot bool) []actool.CoreTool
	WorkDir                string
	ToolOutputDir          string
	MaxToolOutputChars     int
	MaxTurns               int
	MaxDuration            time.Duration
	MaxTokens              int
	MaxConcurrency         int
	NonStreaming           bool
	PermissionMode         permission.Mode
	AllowedTools           []string
	DisallowedTools        []string
	BashEnv                []string
	Hooks                  harness.HookRunner
	Compactor              harness.Compactor
	Settlement             *harness.Settlement
	TokenBudget            int
	EscalateMaxTokens      bool
	DisableBackgroundTasks bool
	Emit                   func(db.Activity)
	SessionIDPrefix        string

	// Window / RunInfo / RootAgentKey / ChildAgentKey 是装配面需要的输入，
	// 由 server 层按任务填（见 server/engine_coord_wire.go 的 coordOptionsFor）。
	Window        int
	RunInfo       RunInfo
	RootAgentKey  string
	ChildAgentKey string

	// WebFetch / WebSearch：见 SessionRunner 同名字段的注释。
	WebFetchProxy  string
	WebFetchCACert string
	WebSearch      WebSearchOpts
}

// NewSessionRunner 是 RunnerConfig → *SessionRunner 的显式构造器。
// 显式构造而不是结构体字面量，是为了以后新增字段时不会静默漏配。
func NewSessionRunner(c RunnerConfig) *SessionRunner {
	return &SessionRunner{
		toolset:                &ToolSet{},
		Provider:               c.Provider,
		ToolsFor:               c.ToolsFor,
		BaseTools:              c.BaseTools,
		WorkDir:                c.WorkDir,
		ToolOutputDir:          c.ToolOutputDir,
		MaxToolOutputChars:     c.MaxToolOutputChars,
		MaxTurns:               c.MaxTurns,
		MaxDuration:            c.MaxDuration,
		MaxTokens:              c.MaxTokens,
		MaxConcurrency:         c.MaxConcurrency,
		NonStreaming:           c.NonStreaming,
		PermissionMode:         c.PermissionMode,
		AllowedTools:           c.AllowedTools,
		DisallowedTools:        c.DisallowedTools,
		BashEnv:                c.BashEnv,
		Hooks:                  c.Hooks,
		Compactor:              c.Compactor,
		Settlement:             c.Settlement,
		TokenBudget:            c.TokenBudget,
		EscalateMaxTokens:      c.EscalateMaxTokens,
		DisableBackgroundTasks: c.DisableBackgroundTasks,
		Emit:                   c.Emit,
		SessionIDPrefix:        c.SessionIDPrefix,
		Window:                 c.Window,
		RunInfo:                c.RunInfo,
		RootAgentKey:           c.RootAgentKey,
		ChildAgentKey:          c.ChildAgentKey,
		WebFetchProxy:          c.WebFetchProxy,
		WebFetchCACert:         c.WebFetchCACert,
		WebSearch:              c.WebSearch,
	}
}

// permissionModeOrDefault 与旧 worker 保持一致。
//
// ── 为什么默认必须是 ModeBypass，而不是 norma 的 ModeDefault ─────────────────
//
// 看 permission.Evaluate 的 Stage 4：ModeDefault 下，一个**非只读**工具如果
// 没有被 allow 规则命中，会落到「交互确认」分支；而 req.Ask 来自
// Options.CanUseTool，我们**没有配**，于是得到：
//
//	Denied("denied: no approval handler configured for '<tool>'")
//
// 也就是说 ModeDefault + 无 CanUseTool 的组合，会让 Bash / Write / Edit /
// insert_assets / add_finding 这些"真正在干活"的工具**全部被拒**，只留下只读
// 工具能跑。子 agent 会表现得像"只会读、不会做"，而错误信息看起来像是权限配置
// 没填，而不是模式选错了。
//
// 旧链路里 worker 用的是 permission.ModeBypass（见 agent/worker.go 的 Options
// 字面量），这是刻意的：能跑什么由**工具面**决定（ToolResolve 的 tools.agents
// 绑定 + OfficialToolKeys 白名单 + DisallowedTools），而不是由交互确认决定——
// 这是一台无人值守的机器，没有人可以点"允许"。
func permissionModeOrDefault(m permission.Mode) permission.Mode {
	if strings.TrimSpace(string(m)) == "" {
		return permission.ModeBypass
	}
	return m
}
