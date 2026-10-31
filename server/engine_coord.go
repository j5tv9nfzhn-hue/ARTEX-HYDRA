package server

// 新调度层的引擎入口（P1）。
//
// 旧路径是 Run → runTaskRoutine(plannerLoop) + N×runTaskRoutine(workerLoop)，
// 四条 goroutine 各自跑一个角色，靠 frontier 表与事件互相唤醒。
//
// 新路径只有一条：RunCoordTask 建一个根 agent，让它自己派活、自己等、自己收尾。
// 任务级的东西（暂停、删除、预算、超时）仍然归 Engine，因为它们是**平台语义**，
// 不是调度语义：一个任务可以被用户从 UI 暂停，这件事与 agent 图无关。
//
// 本文件刻意不 import 旧的 planner/worker 相关符号：它是新链路的独立入口，
// 与 engine.go 的旧路径零耦合，删旧代码时不会牵连到这里。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// coordTask 是一次新链路任务的内存句柄。
//
// 它挂在 Engine.coordTasks 里，使 Pause / Delete / TokenBudget 这些**平台操作**
// 能找到对应的图并把它关掉——旧链路里这些操作是去取消 planner.Plan 与
// worker.Execute，新链路里是取消整张图，语义一致且更简单。
type coordTask struct {
	taskID string
	run    *agent.TaskRun

	mu      sync.Mutex
	closed  bool
	started time.Time
}

func (c *coordTask) close(reason string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()
	c.run.Finish(reason)
}

// ---------- 引擎侧状态 ----------

// 注册表做成**包级**而不是 Engine 的字段，是为了让这次接线对 engine.go 零改动：
// 目前工作区有 53 项未提交改动，能不动老文件就不动——少一处冲突面，少一次
// 需要人工解冲突的 merge。
//
// 键是 "engine 实例指针 + 任务 id"：同一个进程里可能存在多个 Engine（测试、
// 多租户），只按任务 id 做键会让它们互相看见对方的任务。
var (
	coordRegMu sync.Mutex
	coordReg   = map[string]*coordTask{} // key: engineKey(e) + "|" + taskID
)

func coordKey(e *Engine, taskID string) string {
	return fmt.Sprintf("%p|%s", e, taskID)
}

func coordPut(e *Engine, t *coordTask) {
	coordRegMu.Lock()
	coordReg[coordKey(e, t.taskID)] = t
	coordRegMu.Unlock()
}

func coordGet(e *Engine, taskID string) (*coordTask, bool) {
	coordRegMu.Lock()
	defer coordRegMu.Unlock()
	t, ok := coordReg[coordKey(e, taskID)]
	return t, ok
}

func coordDel(e *Engine, taskID string) {
	coordRegMu.Lock()
	delete(coordReg, coordKey(e, taskID))
	coordRegMu.Unlock()
}

// stopCoordTask 是平台操作（暂停/删除/预算耗尽）关闭新链路任务的唯一入口。
// 找不到任务时返回 false，调用方据此决定是否走旧的取消路径。
func (e *Engine) stopCoordTask(taskID, reason string) bool {
	t, ok := coordGet(e, taskID)
	if !ok {
		return false
	}
	t.close(reason)
	return true
}

// ---------- 入口 ----------

// CoordTaskOptions 是新链路跑一个任务所需的全部输入。
//
// 为什么不让 Engine 自己去查 provider / 工具面：这两样东西的来源（task_llm.go 的
// per-task LLM 运行时、ToolSet 的领域工具面）都属于 **server 层接线**，引擎只该
// 消费它们。把它们塞进 Engine 会让 engine_coord.go 反向依赖 task_llm.go，
// 删旧链路时又要多断一根线。
type CoordTaskOptions struct {
	// Provider 是该任务的 LLM 运行时（通常是 taskLLMRuntime：它按任务的
	// LLM profile 链解析，天然支持多档切换）。必填。
	Provider llm.Provider
	// Tools 是该 agent 的基础工具面（领域工具）。协调器工具由 runner 追加。
	Tools []actool.CoreTool
	// ToolsFor 可选：按角色/是否根返回不同工具面，覆盖 Tools。
	ToolsFor func(role string, isRoot bool) []actool.CoreTool

	WorkDir            string
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
	ToolOutputDir      string
	MaxToolOutputChars int
	SessionIDPrefix    string

	// Window 是该任务的上下文窗口（token）。由 workerRuntime.CompactionWindow()
	// 提供——与旧 worker 取的是**同一个函数**，所以压缩触发点、工具输出上限、
	// 注入预算三处口径自动一致。0 = 未提供，装配层退回默认窗口。
	Window int
	// RunInfo 是工具调用的归属（探索 id / 会话 id）。缺失时 tool_usage 会把
	// 调用记成无归属，成本无法按任务归因。
	RunInfo agent.RunInfo
	// RootAgentKey / ChildAgentKey 是根与子在可见性表里的身份。
	RootAgentKey  string
	ChildAgentKey string

	// WebFetch / WebSearch 是"联网取内容"的两条独立通道。
	//
	// WebFetch 走**记录代理**并信任其 MITM CA：目标的 HTTPS 响应因此和 curl
	// 一样被留痕，进入流量视图。关掉它等于让新链路的读取行为对审计不可见——
	// 这不是"少一个工具"，而是证据链上缺一块。
	//
	// WebSearch 是可选能力（默认关），后端由设置页决定；与 MITM 代理无关，
	// 它有独立的出口代理。
	WebFetchProxy  string
	WebFetchCACert string
	WebSearch      agent.WebSearchOpts

	// Emit 可选：宿主想自己接管活动流时提供。为空则由 RunCoordTask 用
	// emitCoordActivity（写 db.Activity + SSE）接管。
	Emit func(db.Activity)
}

func (o CoordTaskOptions) runnerConfig() agent.RunnerConfig {
	return agent.RunnerConfig{
		Provider:           o.Provider,
		BaseTools:          o.Tools,
		ToolsFor:           o.ToolsFor,
		WorkDir:            o.WorkDir,
		ToolOutputDir:      o.ToolOutputDir,
		MaxToolOutputChars: o.MaxToolOutputChars,
		MaxTurns:           o.MaxTurns,
		MaxDuration:        o.MaxDuration,
		MaxTokens:          o.MaxTokens,
		MaxConcurrency:     o.MaxConcurrency,
		NonStreaming:       o.NonStreaming,
		PermissionMode:     o.PermissionMode,
		AllowedTools:       o.AllowedTools,
		DisallowedTools:    o.DisallowedTools,
		BashEnv:            o.BashEnv,
		Hooks:              o.Hooks,
		Compactor:          o.Compactor,
		Settlement:         o.Settlement,
		TokenBudget:        o.TokenBudget,
		EscalateMaxTokens:  o.EscalateMaxTokens,
		SessionIDPrefix:    o.SessionIDPrefix,
		Window:             o.Window,
		RunInfo:            o.RunInfo,
		RootAgentKey:       o.RootAgentKey,
		ChildAgentKey:      o.ChildAgentKey,
		WebFetchProxy:      o.WebFetchProxy,
		WebFetchCACert:     o.WebFetchCACert,
		WebSearch:          o.WebSearch,
		Emit:               o.Emit,
	}
}

// RunCoordTask 用新调度层跑一个任务。它是 Run 在 ARTEX_SCHED=coord 时的替代。
//
// 与旧 Run 的差别：
//   - 不起 plannerLoop、不起 worker 池，只起一个根 agent；
//   - 任务的"完成"由根 agent 的 finish_task 决定，不再由 planner 判 met；
//   - frontier / intent / handoff / reviewer 全链路不参与；
//   - 资产、发现、探索图这些**业务实体**仍然走既有 store（它们不是控制流）。
func (e *Engine) RunCoordTask(ctx context.Context, t *Task, opts CoordTaskOptions) error {
	if t == nil {
		return fmt.Errorf("coord: nil task")
	}
	if _, dup := coordGet(e, t.ID); dup {
		return nil // Run 必须幂等（与旧路径的 started sync.Map 同义）
	}

	if opts.Provider == nil {
		return fmt.Errorf("coord: task %s 没有可用的 LLM provider（任务 LLM 链可能已耗尽）", t.ID)
	}
	cfg := opts.runnerConfig()
	// 活动流必须在构造 runner 之前挂上：否则根 agent 的首轮（通常是它唯一的
	// "思考"轮）不会有任何 activity，UI 看起来像卡住了。
	cfg.Emit = func(a db.Activity) { e.emitCoordActivity(t, a) }
	runner := agent.NewSessionRunner(cfg)

	// 根任务的 spec：goal 是任务目标原文，约束来自任务级配置。
	spec := agent.AgentSpec{
		Name:   "root",
		Role:   "root",
		Task:   t.Goal,
		Depth:  0,
		Skills: nil,
	}
	// 任务没有 goal（人工直投型）时，给根一个明确的启动指令，
	// 而不是让它在空目标上猜（旧架构用 goalless 分支绕过了 planner）。
	if spec.Task == "" {
		spec.Task = "本任务没有预设目标，由你从当前资产与探索图自行判断值得推进的方向；" +
			"派活前先用只读工具确认现状，结束时用 finish_task 说明你的判断依据。"
	}

	taskID := t.ID
	run, err := agent.StartTask(ctx, taskID, runner, spec, coordMaxConcurrent)
	if err != nil {
		return fmt.Errorf("coord: start root agent: %w", err)
	}
	ct := &coordTask{taskID: taskID, run: run, started: time.Now()}
	coordPut(e, ct)
	defer func() {
		ct.close("task goroutine exited")
		coordDel(e, taskID)
	}()

	// 活动流在 NewSessionRunner 之前已挂（见上）。这里只留说明：
	// 把 agent 事件转成既有的 db.Activity，使前端不需要为新链路改协议——
	// "换引擎不换 UI"，UI 看到的仍然是 timeline 里一条条 activity。

	e.emitActivity(t, db.Activity{Worker: "system", Kind: "text",
		Summary: "新调度层已启动：1 个根 agent 负责编排，子 agent 按需派生"})

	// 等根收工。这里多一层墙钟兜底：根 agent 可能与子 agent 互相等待（A 等 B、
	// B 等 A），harness 的 MaxDuration 只约束**单次 run**，约束不了跨 agent 的
	// 等待链。没有这层兜底，一个环就永久占着一个任务位。
	awaitCtx, cancelAwait := context.WithTimeout(ctx, coordTaskWallClock(t))
	defer cancelAwait()
	brief, werr := run.AwaitRoot(awaitCtx)
	if werr != nil {
		log.Printf("[coord] task %s 等待根 agent 结束失败: %v", taskID, werr)
		if errors.Is(werr, context.DeadlineExceeded) {
			brief = agent.Brief{
				State:     agent.StateCompleted,
				Outcome:   "open_proof_gap",
				Summary:   "任务墙钟预算耗尽，根 agent 未能在限内交卷。已关闭整张图，未闭环项见各子 agent 报告。",
				OpenItems: []string{"根 agent 未给出最终结论"},
			}
		}
	}

	// 收尾：把根的交卷落成任务状态。verdict met → done；blocked → failed；
	// partial / 其它 → done（有结论就收，未闭环项进报告）。
	e.settleCoordTask(t, brief)
	return nil
}

// settleCoordTask 把根 agent 的 Brief 折成任务终态与一条可读摘要。
func (e *Engine) settleCoordTask(t *Task, b agent.Brief) {
	if t == nil {
		return
	}
	status := "done"
	switch b.Outcome {
	case "blocked":
		status = "failed"
	case "partial", "met", "":
		status = "done"
	}
	if err := e.m.SetTaskStatus(t.ID, status); err != nil {
		log.Printf("[coord] task %s 落终态 %s 失败: %v", t.ID, status, err)
	}
	summary := b.Summary
	if len(b.OpenItems) > 0 {
		summary += fmt.Sprintf("（未闭环 %d 项）", len(b.OpenItems))
	}
	e.emitActivity(t, db.Activity{Worker: "system", Kind: "text",
		Summary: fmt.Sprintf("任务收尾：%s / %s", b.Outcome, summary)})
}

// emitCoordActivity 把 capture 产出的 Activity 落到活动流。
//
// 与旧链路唯一的差别在 Worker 字段：它现在装的是 **agent id**（root / sqli-1 …），
// 而不是固定的 planner/worker。前端按 Worker 分栏时，新链路自然呈现为
// "每个 agent 一栏"，不需要改任何前端协议。
//
// 过滤规则同理保留 capture.go 的口径（它已合并流式增量、关联 tool_use/result、
// 中断补终止行），这里只做两件事：打上任务维度、在 run 结束时检查 token 预算。
func (e *Engine) emitCoordActivity(t *Task, a db.Activity) {
	if t == nil {
		return
	}
	e.emitActivity(t, a)
	if a.Kind == "result" {
		// 每次 run 收尾检查一次任务级 token 预算：新链路里"run"的粒度是
		// 单个 agent 的一次执行，这是最接近旧 checkTokenBudget 调用点的位置。
		// 放在这里而不是等整张图跑完，是为了让预算超限能**中途**停图。
		e.checkCoordTokenBudget(t)
	}
}

// trimForUI 把一段文本压成 UI 友好的一行。
func trimForUI(s string, n int) string {
	out := make([]rune, 0, n)
	space := false
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			if !space {
				out = append(out, ' ')
				space = true
			}
			continue
		}
		space = false
		out = append(out, r)
		if len(out) >= n {
			return string(out) + "…"
		}
	}
	return string(out)
}

// coordMaxConcurrent 是新链路的并行子 agent 上限。与旧架构的 worker 池
// （N 个固定 goroutine 抢 frontier）不同，这里是"按派发动态起停"，
// 上限只受资源约束，不再受"池大小"约束。
const coordMaxConcurrent = 4

// coordTaskWallClock 是任务级墙钟上限。
//
// 三个来源按优先级叠加，都是为了回答同一个问题：**等根 agent 到底该等多久**。
//   - DeadlineAt（任务已盖截止章）：直接等到那个时刻，这是用户的承诺；
//   - TimeoutSeconds：从"现在"起算（DeadlineAt 未盖时的兜底）；
//   - 都没有：默认 6 小时——不是"合理地长"，而是"足够长到只兜住死锁"。
//
// 刻意不按「单次 run 预算 × N」推导：子 agent 是树形展开的，层数不确定，
// 按乘法算出来的值会让深树任务被误杀。
func coordTaskWallClock(t *Task) time.Duration {
	const def = 6 * time.Hour
	if t == nil {
		return def
	}
	if t.DeadlineAt > 0 {
		if d := time.Until(time.Unix(t.DeadlineAt, 0)); d > 0 {
			return d + time.Minute // 截止时刻后留一分钟，让收尾动作能跑完
		}
		return time.Minute
	}
	if t.TimeoutSeconds > 0 {
		return time.Duration(t.TimeoutSeconds) * time.Second
	}
	return def
}
