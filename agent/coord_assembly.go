package agent

// 新链路的**装配面**：一次装配 = 领域工具 + 协调器工具，全部经 AugmentTools
// 一次成面；系统提示经 deferredSystem 分段定界；压缩经 attachCompaction 装配。
//
// ── 为什么必须有这个文件 ────────────────────────────────────────────────────
//
// P2 交付的 runner 把 `WorkerTools() + SDKDefaultTools()` 直接塞进
// agentcore.Options.Tools。这在**每一个**旧 agent（worker / planner / mainagent /
// chat）上都不是它们实际走的路径——它们全部经过下面这条管道：
//
//	AugmentTools(ctx, agentKey, base)                       agent/assembly.go:46
//	  ├ ToolAugment       → 技能 + MCP（直接可见 / skill-gated deferred / 解锁集合）
//	  ├ ToolResolve       → DB 工具表：按 agent 绑定过滤、覆盖说明与 schema、
//	  │                     注入缺省入参、计量（tool_usage）
//	  ├ findingWorkflowTools → 找洞工作流指引，按**过滤后**的工具面生成
//	  ├ routeToolFace     → 能力向量路由（本次不启用，理由见下）
//	  ├ leanToolList      → 工具面瘦身
//	  ├ lockResources     → 同一文件/同一图/同一 MCP 服务按资源键串行
//	  ├ guardPanic        → panic 兜底、入参协议防御与纠偏、兜底超时、
//	  │                     统一输出裁剪（head 70% / tail 30% + 全量落盘）
//	  └ wrapSkillRecall   → 技能旁路再加载
//
// 绕开它等于：子 agent 看不见任何 MCP 与技能；工具没有 panic 兜底与入参纠偏；
// 两个 agent 同时改同一个文件不串行；工具调用完全不进 tool_usage（成本无法归因）；
// 系统提示没有 <available-deferred-tools> 段，缓存前缀也不成立。
//
// 换句话说，这不是"少接一个可选增强"，是**整个工具面治理层被跳过**。
//
// ── 装配顺序为什么必须是这个顺序 ────────────────────────────────────────────
//
// 与 worker.go:514–723 逐行对齐，顺序不是风格问题，每一步都有依赖：
//
//	1. WithContextWindow  → findingWorkflowTools 与注入预算要读它
//	2. WithRunInfo        → 计量与技能台账要它定归属，且必须在 AugmentTools 之前
//	3. AugmentTools       → 一次成面（含技能/MCP/DB 过滤/防护层）
//	4. deferredSystem     → 依赖 def.GlobalNames / def.MCPCatalog 才算得出分段
//	5. attachCompaction   → 依赖 opts.Provider / MaxTokens / NonStreaming 已就位
//
// 把 5 放到 3 之前，压缩器会拿到一个 Provider 为 nil 的 summarizer；把 1 放到 3
// 之后，注入预算按"未知窗口"取兜底值，长任务的表现会与旧链路无声分叉。

import (
	"context"
	"strings"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	actool "github.com/Autumn-27/norma/tool"
)

const (
	// DefaultCoordRootAgentKey / DefaultCoordChildAgentKey 是根与子 agent 在
	// **可见性表**（agents / agent_visibility / tools.agents）里的身份。
	//
	// 两者都取 "worker"，这是刻意的，不是偷懒：
	//
	//	ToolResolve 用 tools.agents 这一列决定"这个工具给不给这个 agent"，
	//	而领域工具（insert_assets / list_findings / record_fact / add_finding /
	//	search_all_worker_traces …）在种子里绑定的就是 "worker"。给根换一个
	//	"mainagent" 身份，会让根在 ToolResolve 里被判定为未绑定，于是它连
	//	list_findings 都查不到——而"根看不到子 agent 的发现"正是新架构最不能
	//	出的故障。
	//
	// 根与子的区别不靠工具面表达，靠**协调器工具**表达：
	//	根  → finish_task（结束整个任务）
	//	子  → submit_report（只结束自己）
	// 一个 agent 只有一种"我完事了"的出口，否则模型会在两种之间犹豫。
	//
	// 这两个键做成可配置项（RunnerConfig.RootAgentKey / ChildAgentKey），
	// 是为了将来真的给根单独一套可见性时，不必改这里。
	DefaultCoordRootAgentKey  = "worker"
	DefaultCoordChildAgentKey = "worker"
)

// CoordSurface 是一次装配的全部产物。
//
// 把产物做成一个值而不是直接写进 Options，是为了让"装配"与"下单"分开：
// 装配会连 MCP、读 DB，是重操作且**必须正好一次**；Options 只是把结果摆好。
type CoordSurface struct {
	Tools    []actool.CoreTool
	System   []string
	Boundary int

	// Deferred / Unlock 是 MCP 惰性加载的那一半：名字进系统提示的
	// <available-deferred-tools> 块，schema 扣住不给，调用经 ExecuteExtraTool。
	// 为空表示本次装配没有任何 deferred 工具，属正常。
	Deferred []string
	Unlock   *actool.UnlockSet

	MaxToolOutputChars int
	Settlement         *harness.Settlement

	// AgentKey 是本次装配实际用的可见性身份，记下来用于日志对账。
	AgentKey string
	// Window 是本次装配认定的上下文窗口（token），0 表示未提供、已取默认。
	Window int
	// ToolCalls 型的统计不在这里做：它由 capture 的 Activity 流统计。

	cleanup func()
}

// Cleanup 关闭本次装配打开的 MCP 客户端。
//
// 必须真的被 defer：每个 MCP 客户端是一个子进程或一条 HTTP 连接，任务跑完
// 不关，第二次跑同一任务时旧连接还在，服务端会看到 N 份同样的会话。
func (s *CoordSurface) Cleanup() {
	if s == nil || s.cleanup == nil {
		return
	}
	s.cleanup()
}

// CoordAssembleInput 是装配一个 coord agent 所需的全部输入。
type CoordAssembleInput struct {
	// Base 是领域工具面（不含协调器工具——那些由调用方先拼进 Base）。
	Base    []actool.CoreTool
	SysBody string
	IsRoot  bool
	RunInfo RunInfo

	// Window 是任务的上下文窗口（token）。0 表示按默认窗口。
	Window int

	// RootKey / ChildKey 覆盖默认可见性身份。
	RootKey  string
	ChildKey string

	// SettlementDisabled 是收尾阶段要藏起来的工具名。
	SettlementDisabled []string
}

// AssembleCoordSurface 按旧链路同一顺序装配一个 coord agent 的工具面与系统提示。
//
// 返回的 CoordSurface 必须 Cleanup。
func AssembleCoordSurface(ctx context.Context, in CoordAssembleInput) *CoordSurface {
	key := coordAgentKey(in.RootKey, in.ChildKey, in.IsRoot)

	window := in.Window
	if window <= 0 {
		// 与 worker 的默认口径同源：ResolveContextWindowTokens(0) 返回
		// defaultWindowK*1000，而不是在这里另写一个常数。
		if tok, _ := ResolveContextWindowTokens(0); tok > 0 {
			window = tok
		}
	}

	// 复制一份 base：AugmentTools 内部会 append，直接传调用方的切片有可能
	// 写到它的底层数组里，而调用方可能复用同一个切片装配多个 agent。
	base := append([]actool.CoreTool{}, in.Base...)

	// 顺序依赖见文件头 1–3。
	ctx = WithRunInfo(ctx, in.RunInfo)
	ctx = WithContextWindow(ctx, window)

	tools, def, cleanup := AugmentTools(ctx, key, base)

	// 4：系统提示分段。deferredSystem 会追加 def.FindingGuidance，并把
	// <available-deferred-tools> + MCP 速查表放在**最后一段**，DynamicBoundary
	// 指向段数，使整段 system（含该块）进同一个缓存前缀。
	system, boundary := deferredSystem(in.SysBody, def)

	surf := &CoordSurface{
		Tools:    tools,
		System:   system,
		Boundary: boundary,
		Deferred: def.Deferred,
		Unlock:   def.Unlock,
		// 工具输出进历史的字符上限按窗口推导（窗口 1/8，夹 4K–24K），
		// 与 worker.go:694 同一口径。写死一个常数会让大窗口任务无谓地把
		// 每轮历史重发体积放大好几倍。
		MaxToolOutputChars: ToolOutputChars(window),
		Settlement:         coordSettlement(in.IsRoot, in.SettlementDisabled, resolveWrapupTurns(key)),
		AgentKey:           key,
		Window:             window,
		cleanup:            cleanup,
	}
	return surf
}

// Apply 把装配结果落到 agentcore.Options。
//
// 只写装配面拥有的字段；预算、Provider、WorkingDir 等仍由调用方负责，
// 这样"谁决定什么"在代码里是看得见的，而不是一个 40 行的字面量糊在一起。
func (s *CoordSurface) Apply(opts *agentcore.Options) {
	if s == nil || opts == nil {
		return
	}
	opts.Tools = s.Tools
	opts.SystemPrompt = s.System
	opts.DynamicBoundary = s.Boundary
	opts.DeferredTools = s.Deferred
	opts.UnlockSet = s.Unlock
	opts.MaxToolOutputChars = s.MaxToolOutputChars
	if opts.Settlement == nil {
		opts.Settlement = s.Settlement
	}
}

// AttachCompaction 装配上下文压缩。
//
// 必须在 Apply 之后、且 opts.Provider / MaxTokens / NonStreaming 已就位之后调用：
// attachCompaction 会用 opts.Provider 造摘要器，Provider 为 nil 时得到的是一个
// 一压缩就失败的压缩器——而失败在 SDK 里只会被记成一次 warn，看起来像"压缩没触发"。
//
// 这里接的是 ARTEX 自己那套阈值推导（CompactionTriggerTokens：80% 与「窗口−预留」
// 取更早，且不超过 120K 绝对上限），不是 agentcore 的默认值。P2 里写成
// `opts.Compactor = nil` + 注释"暂用 agentcore 默认策略"，实际效果是新链路与旧
// worker 的压缩行为从第一轮起就不同。
func (s *CoordSurface) AttachCompaction(ctx context.Context, opts *agentcore.Options, label string) {
	if s == nil || opts == nil {
		return
	}
	attachCompaction(ctx, opts, s.Window, label, skillRecallFor(ctx, s.AgentKey))
}

// coordAgentKey 解析本次装配的可见性身份。
func coordAgentKey(rootKey, childKey string, isRoot bool) string {
	if isRoot {
		return firstNonEmpty(rootKey, DefaultCoordRootAgentKey)
	}
	return firstNonEmpty(childKey, DefaultCoordChildAgentKey)
}

// coordSettlement 是预算耗尽时的收尾要求。
//
// 不复用 wrapupSettlement("worker", …)：worker 的收尾词点名
// report_finding / http_request / web_recon 等工具，其中一批已在第六阶段被
// 摘出工具面（见 CORRECTION-toolface.md：14 个领域工具无工具面引用）。提示里
// 点名一个不存在的工具，模型会照着调一次、拿到 unknown tool、然后不再尝试——
// 这正是 agent/tool_lean.go 的 toolNameDiscipline 要防的那类漂移。
//
// 所以这里只点名**确定在面上**的工具：
//
//	根：finish_task；子：submit_report + record_fact。
func coordSettlement(isRoot bool, disabled []string, maxTurns int) *harness.Settlement {
	var p string
	if isRoot {
		p = "**本次运行预算已耗尽，整个任务即将结束**。(1) 不要再派生任何子 agent——" +
			"现在派出去的工作不会被等待，也不会进入结论。(2) 已收到的子任务交卷按" +
			"实际证据并入结论，不要补写没有证据的部分。(3) 立刻用 finish_task 收尾：" +
			"outcome 如实填（未闭环就写 partial），summary 写结论与依据，" +
			"既不能确认也不能排除的东西全部写进 open_items。"
	} else {
		p = "**本次运行预算已耗尽**。(1) 不要开启新的探测，也不要跑长命令。" +
			"(2) 已经确认但尚未写回的事实，用 record_fact 立刻落下（detail 里写清" +
			"下一步 / 假设 / 已排除项）。(3) 立刻用 submit_report 交卷：结论是什么、" +
			"证据是什么、哪些东西既不能确认也不能排除（写进 open_items）。" +
			"不要用散文描述过程，不要停在半路。"
	}
	if maxTurns <= 0 {
		maxTurns = 2
	}
	return &harness.Settlement{
		Prompt:        withWrapupLang(strings.TrimSpace(p)),
		DisabledTools: disabled,
		MaxTurns:      maxTurns,
	}
}

// CoordBaseTools 返回 coord agent 的基础执行工具面。
//
// 等价于 worker.go:551 那句 `WrapCrossPlatformBash(defaultToolsExcept(trimSDK...),
// runDir, proxyEnv(...))`，但**不做 trimSDK**：
//
//   - worker 裁掉 Write / Edit / MultiEdit / LS / Sleep / apply_patch，是因为它
//     有专用领域工具可派，且通用写入工具会吞掉专用工具的调用份额；
//   - 新链路的子 agent **只有**这一套通用执行器（8 个图谱工具 + SDK 默认面），
//     裁掉写入工具等于让它在需要落一个 PoC 脚本时无路可走。
//
// 保留 LS 与 Sleep 同理：它们在这里不是"逃生舱"，而是唯一可用的原语。
// 这个差别来自工具面的构成，不是放宽纪律——纪律由工具面本身表达。
func CoordBaseTools(workDir, proxyAddr, proxyCACert string) []actool.CoreTool {
	return WrapCrossPlatformBash(SDKDefaultTools(), workDir, proxyEnv(proxyAddr, proxyCACert))
}

// ProxyEnv 导出 proxyEnv，供 server 层填 Options.BashEnv（Bash 子命令的 HTTP
// 与工具链信任 CA 都靠它）。与 worker 用的是同一个函数，不另写一份。
func ProxyEnv(proxyAddr, proxyCACert string) []string {
	return proxyEnv(proxyAddr, proxyCACert)
}

// CoordSettlementDisabled 返回收尾阶段该藏起来的工具名。
//
// 收尾的目的是**停止产生新工作**，所以藏掉的是"还能继续干活"的两类工具：
//   - Bash：有它就能继续跑命令，模型的默认反应就是再试一次；
//   - delegate_task：有它就能再派一个子 agent，等于把"收尾"变成"扩大战线"。
//
// 名字一律用常量（ToolDelegateTask），不写字面量：写错一个不存在的名字不会报错，
// 只会静默地什么都不藏——收尾阶段照旧能派活，而日志上看不出任何异常。
// 这个函数的第一版就是写成了 "spawn"，它在工具面上根本不存在。
//
// 交卷工具（finish_task / submit_report）与写回工具（record_fact）必须留着，
// 否则收尾阶段唯一的出口被藏掉，agent 只能空转，最后由外层补轮兜底。
func CoordSettlementDisabled() []string {
	return []string{"Bash", ToolDelegateTask}
}
