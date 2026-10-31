package server

// 新链路与旧引擎的**唯一接缝**。
//
// 设计原则：不重写 engine.go，只让它的四个既有入口在 coord 模式下改道。
// 旧文件的改动因此只有四处一行的 `if e.coordTakeover(...) { return }`，
// 而不是把 1,598 行拆开重排——在 53 项未提交改动的仓库上，这是唯一可回滚的做法。
//
// 四个改道点：
//
//	Run(ctx, t)            → 起根 agent（取代 plannerLoop + workerLoop + 首轮 kick）
//	Pause(taskID, cause)   → 关整张图（取代 cancelExec 单个 run）
//	Resume(t)              → 重新起一张图（旧链路是 nudge 一次规划轮）
//	BeginDelete(taskID)    → 先关图再走既有删除屏障
//
// 为什么 Pause/Resume 需要改道而不是复用：旧链路的暂停是"取消在跑的 run +
// 让下一次 loop 不再领活"，它假设 loop 一直活着。新链路里 loop 就是根 agent
// 本身——暂停必须真的把根停下，恢复必须真的重新起一个，没有"活着但不动"的状态。
// 这正好对应 strix 报告里 `budget_paused` 与 `stopped` 的区别。

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// CoordAgents 是新链路需要从 Server 取的三件事。
//
// 做成接口而不是让 Engine 直接持有 *Server：Engine 在包内已被 300+ 处引用，
// 加一个字段意味着改结构体字面量的所有地方（测试里也有）。一个包级注册 +
// 两个方法，代价最小，也让"新链路依赖面"显式可见——只有这两件事。
type CoordAgents interface {
	AgentMaxTurns(key string) int
	AgentRunSeconds(key string) int
	// AgentsForTask 取任务的 agent 装配包（provider / 运行时 / 工具面来源），
	// 与旧 planner/worker 完全同源 —— 多档切换、配额轮转、上下文窗口这些
	// 既有能力因此对新链路自动生效，不需要在别处再实现一遍。
	AgentsForTask(t *Task) *taskAgentBundle
}

var coordCfgFn func() CoordAgents

// RegisterCoordAgents 由 Server 启动时调用一次（server.go:wireCoord 里）。
// 未调用时新链路仍可运行，只是不继承 agent 的轮次/时长预算（返回 0 = 不限）。
func RegisterCoordAgents(fn func() CoordAgents) { coordCfgFn = fn }

func coordCfg() CoordAgents {
	if coordCfgFn == nil {
		return nil
	}
	return coordCfgFn()
}

// CoordTakeover 报告引擎是否应把该任务的执行交给新调度层。
//
// 三个条件必须同时成立：
//  1. 进程级开关打开（ARTEX_SCHED=coord）——不做任务级混跑，见 coord_switch.go；
//  2. 该任务还没有在新链路里注册过（Run 幂等，与旧 started.LoadOrStore 同义）；
//  3. 该任务不在删除屏障里（复用旧引擎已有的判定，不新造一套）。
func (e *Engine) CoordTakeover(taskID string) bool {
	if !agent.CoordEnabled() {
		return false
	}
	if e.IsDeleting(taskID) {
		return false
	}
	if _, dup := coordGet(e, taskID); dup {
		return false
	}
	return true
}

// coordTakeoverFor 是 CoordTakeover 的 *Task 版本，语义相同、参数方便。
func (e *Engine) coordTakeoverFor(t *Task) bool {
	if t == nil {
		return false
	}
	return e.CoordTakeover(t.ID)
}

// RunCoord 是 Run 在 coord 模式下的替代实现。
//
// 与旧 Run 的区别（逐条对照，便于 review）：
//
//	旧：registerTaskRoutines(1+workers) → plannerLoop + N×workerLoop → kick 首轮
//	新：StartTask(1 个根 agent) → 它自己派活、自己等、自己收尾
//
// 保留的：删除屏障、started 幂等标记（用 coordReg 代替）、touch/trace 埋点、
// 任务状态落库。这些是**平台语义**，与调度模型无关，换引擎不该丢。
func (e *Engine) RunCoord(ctx context.Context, t *Task) {
	if t == nil {
		return
	}
	// 幂等：同一任务重复 Run 只 nudge，不重建图。
	// 旧链路用 t.Notify() 唤醒规划轮；新链路里"唤醒"就是把收尾要求塞给根 agent。
	if _, dup := coordGet(e, t.ID); dup {
		e.nudgeCoordRoot(t, "任务被再次启动，请检查当前进展并决定下一步")
		return
	}
	e.touch(t.ID)
	e.traceEv(t.ID, "engine", "thread.start", "coord", "root=1")

	opts := e.coordOptionsFor(t)
	if len(opts.Tools) == 0 {
		// 没有任何领域工具时不要起图：根 agent 会只剩协调器工具，
		// 于是它唯一能做的就是派活给一个同样没有工具的子孙——纯烧钱。
		log.Printf("[coord] task %s 未就绪：工具面为空（任务 store 未就绪？），跳过开跑", t.ID)
		return
	}
	if opts.Provider == nil {
		// 没有 provider 时不要起图：否则根 agent 会在第一轮就失败，
		// 而 UI 会显示一个已经"跑过"的失败任务。
		log.Printf("[coord] task %s 未就绪：没有可用的 LLM provider，跳过开跑", t.ID)
		return
	}

	// 一个任务一个 goroutine：根 agent 的生命周期就是任务的生命周期。
	go func() {
		if err := e.RunCoordTask(ctx, t, opts); err != nil {
			log.Printf("[coord] task %s 运行失败: %v", t.ID, err)
		}
	}()
}

// agentsForTaskCoord 取任务的 agent 装配包。
//
// 与 agentMaxTurnsCoord 同一套路：经 CoordAgents 从 Server 取，而不是让
// Engine 持有 *Server。未注册（测试 / 未接线）时返回 nil，调用方的
// bundle != nil 判断会跳过 provider 与工具面，coordOptionsFor 里
// 「工具面为空」的那道闸会拦住开跑 —— 不会起一张半装配的图。
func (e *Engine) agentsForTaskCoord(t *Task) *taskAgentBundle {
	cfg := coordCfg()
	if cfg == nil {
		return nil
	}
	return cfg.AgentsForTask(t)
}

// coordOptionsFor 把任务级配置翻译成 CoordTaskOptions。
//
// provider 取 taskLLMRuntime（按任务的 LLM profile 链解析），与旧 planner/worker
// 完全同源——这样"多档切换/配额轮转/上下文窗口"这些既有能力对新链路自动生效，
// 不需要在 agent 包里再实现一遍。
func (e *Engine) coordOptionsFor(t *Task) CoordTaskOptions {
	bundle := e.agentsForTaskCoord(t)

	opts := CoordTaskOptions{
		WorkDir:         e.dataDir(),
		MaxTurns:        e.agentMaxTurnsCoord(),
		MaxDuration:     e.agentRunSecondsCoord(),
		MaxConcurrency:  coordMaxConcurrent,
		ToolOutputDir:   e.coordToolOutputDir(),
		SessionIDPrefix: fmt.Sprintf("t%s", t.ID),
		// 权限模式必须与旧 worker 一致：ModeBypass。
		//
		// 用 ModeDefault 会走进 permission.Evaluate 的 Stage 4（交互确认），
		// 而我们没有 CanUseTool，于是**所有非只读工具**都被拒绝，
		// 错误信息是 "no approval handler configured"。表现是子 agent
		// 只会读不会做。这是一台无人值守的机器，没有人在屏幕前点"允许"——
		// 能跑什么由工具面（tools.agents 绑定 + 官方白名单）决定，不由交互决定。
		PermissionMode: permission.ModeBypass,
		// RunInfo 是工具调用的归属。ExpID 是任务创建时就落库的探索 id，
		// 直接用它让 tool_usage 按任务/探索归因（旧 worker 取 explorationID(ts)，
		// 与这里是同一个值）。TaskID 用 **数值主键**：RunInfo.TaskID 是 int64，
		// 而 t.ID 是字符串任务 id，两者不是一回事，不能互转。
		RunInfo: agent.RunInfo{
			ExplorationID: t.ExpID,
			SessionID:     fmt.Sprintf("t%s", t.ID),
		},
		// Window 在下面 provider 就位后才填（要读 workerRuntime.CompactionWindow）。
		// 这里留 0 只表示"尚未解析"；若 runtime 缺失，装配层会退回 ARTEX 的
		// 默认窗口（ResolveContextWindowTokens(0)），不会算出 0 长度预算。
		Window: 0,
		// 可见性身份：默认都是 worker（理由见 agent/coord_assembly.go）。
		RootAgentKey:  agent.DefaultCoordRootAgentKey,
		ChildAgentKey: agent.DefaultCoordChildAgentKey,
	}
	// 联网通道：WebFetch 走记录代理 + 信任 MITM CA，让读取与 curl 同样留痕；
	// WebSearch 按 worker 的配置（默认关，后端与 key 来自全局配置，
	// 每个 agent 自己决定开不开——见 server.go:607 webSearchFor）。
	if e.m != nil {
		opts.WebFetchProxy = e.m.ProxyAddr()
		opts.WebFetchCACert = e.m.ProxyCACert()
		opts.WebSearch = coordWebSearch(e.m)
	}
	// provider：bundle.workerRuntime 是 taskLLMRuntime，它实现了 llm.Provider
	// （Stream + Complete）。用 workerRuntime 而不是 plannerRuntime：
	// 子 agent 是执行者，与旧 worker 同源；根 agent 的"编排"不产生工具流量，
	// 用同一个 provider 不会造成额外的配额压力。
	if bundle != nil && bundle.workerRuntime != nil {
		opts.Provider = bundle.workerRuntime
		opts.NonStreaming = bundle.workerRuntime.nonStreaming()
		opts.MaxTokens = bundle.workerRuntime.maxTokens()
		// 上下文窗口取自与旧 worker **同一个**解析器。压缩触发点
		// （CompactionTriggerTokens）、工具输出上限（ToolOutputChars）、注入预算
		// 三处都从它推导，取不同的值会让新链路与旧链路的行为无声分叉。
		opts.Window = bundle.workerRuntime.CompactionWindow()
		// 压缩不在这里配：attachCompaction 必须等 Provider / MaxTokens /
		// NonStreaming 全部就位后由装配层调用（见 agent/coord_assembly.go
		// 的 AttachCompaction）。P2 的 `Compactor = nil` 会让新链路改用
		// agentcore 的默认压缩策略，与 ARTEX 那套 80%/120K 阈值完全不同。
	}
	// 工具面：**图谱面 + 通用执行面**，且必须经 AugmentTools 成面。
	//
	// 两层来源：
	//   - 图谱面：新版 WorkerTools() 只有 8 个（listFindings/addFinding/
	//     recordFact/insertAssets/listAssets/searchAllWorkerTraces/getWorkerTrace/
	//     nodeDetail）——ARTEX 已经放弃"每类漏洞一个专用工具"；
	//   - 执行面：CoordBaseTools() = SDKDefaultTools() 经 WrapCrossPlatformBash
	//     装饰（走设置页选的 Shell 与预热池，Windows 下不再硬编码 powershell）。
	//
	// 这里**只是 base**。技能、MCP、DB 工具表过滤、防护层、提示分段全部发生在
	// agent.AssembleCoordSurface 里（它调 AugmentTools）。把 base 直接当工具面用，
	// 等于绕开整条治理管道——P2 就是这么写的，见 agent/coord_assembly.go 文件头。
	//
	// 代理必须在**这一层**就交给 Bash 包装：worker 的 Bash 靠 proxyEnv 让子命令
	// 默认走记录代理并信任 MITM CA，漏了它，新链路的流量不会被记录，
	// 同一条命令在旧 worker 里能通、在新链路里连不上。
	proxyAddr, proxyCACert := e.coordProxy()
	opts.BashEnv = agent.ProxyEnv(proxyAddr, proxyCACert)

	var base []actool.CoreTool
	if bundle != nil && bundle.workerRuntime != nil {
		// 工作目录与旧 worker 同源：worker 用 runDir（按任务/运行目录隔离），
		// 这里用任务数据目录下的工作区，保证 Bash 的 cwd 与工具输出目录同树。
		base = append(base, agent.CoordBaseTools(e.coordWorkDir(), proxyAddr, proxyCACert)...)
	}
	if ts := e.coordToolset(t); ts != nil {
		// 图谱工具放前面：它们没有副作用，且是模型最先需要的上下文。
		opts.Tools = append(ts.WorkerTools(), base...)
	} else {
		opts.Tools = base
	}
	return opts
}

// coordWebSearch 取 worker 档的联网搜索配置。
//
// 与 webSearchFor 的区别只有一点：它读的是 Manager 上已经解析好的全局配置，
// 再用**数据库里的 worker 行**的 web_search 开关做闸门——这正是
// server.go:607 的逻辑，只是不经过 *Server，因为 Engine 只持有 Manager。
// 这样「新链路的联网搜索」与「旧 worker 的联网搜索」由同一个开关控制，
// 不会出现"改了设置只有一半 agent 生效"。
func coordWebSearch(m *Manager) agent.WebSearchOpts {
	o := m.WebSearchOpts()
	if a, err := m.pg.GetAgentByKey("worker"); err != nil || a == nil || !a.WebSearch {
		o.Enabled = false
	}
	return o
}

// coordProxy 取记录代理地址与 MITM CA。
//
// 取自 Manager 的 ProxyAddr / ProxyCACert——与 chatAgent / worker / planner /
// mainagent 的 SetProxy 调用是**同一对来源**（见 server/task_llm.go:681）。
// 空值表示未启用记录代理，proxyEnv 会返回空切片，行为退化为直连。
func (e *Engine) coordProxy() (addr, caCert string) {
	if e == nil || e.m == nil {
		return "", ""
	}
	return e.m.ProxyAddr(), e.m.ProxyCACert()
}

// coordWorkDir 是 coord agent 的工作目录。
//
// 与旧 worker 的 runDir 同为"任务数据目录下的一层"：Bash 的相对路径、工具输出
// 落盘目录、transcripts 都在这棵树上，任务删除时一起清掉。
func (e *Engine) coordWorkDir() string {
	dir := e.dataDir()
	if dir == "" {
		return ""
	}
	return dir + "/work"
}

// coordToolset 取该任务的领域工具面。
//
// 旧链路里 ToolSet 是 worker/planner 各自持有的；这里复用同一个构造入口，
// 保证新链路的工具与旧 worker 完全一致（同样的 store、同样的 target 解析、
// 同样的证据绑定）。返回 nil 表示该任务还没有可用的 store（刚建、尚未落库）。
func (e *Engine) coordToolset(t *Task) *agent.ToolSet {
	if t == nil || t.Store == nil {
		return nil // 任务刚建、store 尚未就绪：此时不该起图（RunCoord 会因 provider/工具面为空而跳过）
	}
	// role 传 "worker"：协调器工具由 runner 追加，这里只决定**领域工具面**，
	// 而领域工具面的可见性在旧代码里是按 role 查表的；用 worker 与原执行者一致。
	return agent.NewToolSet(t.Store, "worker")
}

// coordToolOutputDir 是工具输出溢出落盘目录。放在任务数据目录下，
// 使一次任务的中间产物随任务一起被清理，不会在磁盘上长期堆积。
func (e *Engine) coordToolOutputDir() string {
	dir := e.dataDir()
	if dir == "" {
		return ""
	}
	return dir + "/tool-output"
}

// dataDir 是任务数据的根目录（与 transcripts 同级）。
// 旧代码里直接写 e.m.dir，这里包一层是为了让"数据目录从哪来"只有一处答案。
func (e *Engine) dataDir() string {
	if e.m == nil {
		return ""
	}
	return e.m.dir
}

// agentMaxTurnsCoord / agentRunSecondsCoord 复用既有的 agent 配置查询。
// 取 "worker" 这一档：子 agent 是执行者，与旧 worker 的预算语义一致；
// 根 agent 用它作为自身轮次上限也是合理的（编排轮次通常远少于执行轮次）。
func (e *Engine) agentMaxTurnsCoord() int {
	cfg := coordCfg()
	if cfg == nil {
		return 0 // 0 = 不限；与旧链路 agentMaxTurns 返回 0 的语义一致
	}
	return cfg.AgentMaxTurns("worker")
}

func (e *Engine) agentRunSecondsCoord() time.Duration {
	cfg := coordCfg()
	if cfg == nil {
		return 0
	}
	return time.Duration(cfg.AgentRunSeconds("worker")) * time.Second
}

// ---------- 平台操作改道 ----------

// PauseCoord 关闭整张图。返回 true 表示该任务确实在新链路上，调用方不应再走旧路径。
func (e *Engine) PauseCoord(taskID string, cause error) bool {
	reason := "任务被暂停"
	if cause != nil {
		reason = cause.Error()
	}
	if !e.stopCoordTask(taskID, reason) {
		return false
	}
	log.Printf("[coord] task %s 已暂停（整张图关闭）：%s", taskID, reason)
	return true
}

// ResumeCoord 重新起一张图。旧链路是 nudge；新链路里根已经终结，
// 必须重建——这也是为什么 Resume 必须改道而不是复用 t.Notify()。
func (e *Engine) ResumeCoord(t *Task) bool {
	if t == nil {
		return false
	}
	if _, ok := coordGet(e, t.ID); ok {
		// 图还在：只是被 nudge，不需要重建。
		e.nudgeCoordRoot(t, "任务已恢复，请继续推进")
		return true
	}
	if !agent.CoordEnabled() {
		return false
	}
	e.RunCoord(context.Background(), t)
	return true
}

// DeleteCoord 在删除屏障之前先关图，避免删除过程中子 agent 还在写库。
func (e *Engine) DeleteCoord(taskID string) bool {
	return e.stopCoordTask(taskID, "任务正在删除")
}

// nudgeCoordRoot 给根 agent 投一条 instruction。
//
// 用 instruction 而不是 information：nudge 的语义就是"改变你当前的做法"
// （恢复后继续、被再次启动后检查进展），而 information 会被它当成背景读过去。
func (e *Engine) nudgeCoordRoot(t *Task, msg string) {
	ct, ok := coordGet(e, t.ID)
	if !ok || ct == nil || ct.run == nil || ct.run.Coord == nil {
		return
	}
	root := ct.run.RootID
	if _, err := ct.run.Coord.Send("system", root, agent.MsgInstruction, msg); err != nil {
		log.Printf("[coord] task %s nudge 根 agent 失败: %v", t.ID, err)
	}
}

// ---------- 预算闸 ----------

// ---------- 可达性自检 ----------

// CoordSelfCheck 在启动期做一次静态自检：开关是否打开、配置是否注册、
// 依存的三个 store 句柄是否就绪。它只打日志、不修状态——
// 目的是让"新链路为什么没跑起来"在日志里有一行明确答案，而不是靠猜。
func (e *Engine) CoordSelfCheck() {
	if agent.SchedModeInvalid() {
		log.Printf("[coord] 警告：%s 的值无法识别，已按 coord 运行（旧链路已不完整）。"+
			"合法值：coord（默认）/ legacy。", agent.SchedModeEnv)
	}
	if !agent.CoordEnabled() {
		log.Printf("[coord] 显式回退到旧调度链路（%s=legacy）：planner + worker 池 + frontier。"+
			"注意 reviewer 已在下线，旧链路的部分判定能力不再可用。")
		return
	}
	log.Printf("[coord] 调度层已启用（ARTEX_SCHED=coord）：planner/reviewer 不参与，根 agent 负责编排")
	if coordCfg() == nil {
		log.Printf("[coord] 警告：CoordAgents 未注册，agent 的轮次/时长预算将取默认值（0=不限）")
	}
	if e.m == nil || e.m.dir == "" {
		log.Printf("[coord] 警告：数据目录为空，工具输出将不会落盘（超长输出会被丢弃而非溢出到文件）")
	}
	if coordMaxConcurrent <= 0 {
		log.Printf("[coord] 警告：并发上限非法，将退化为串行派发")
	}
}

// checkCoordTokenBudget 是 checkTokenBudget 的新链路版本。
//
// 旧版本在"每次 run 收尾"检查；新链路里"run"的粒度是单个子 agent，
// 所以在**每个子 agent 收尾**时检查一次最自然——对应 Coordinator 的
// agent 的 result 活动（capture.go 的终态记录）。引擎通过 emitCoordActivity 调用它。
func (e *Engine) checkCoordTokenBudget(t *Task) {
	if t == nil {
		return
	}
	if t.TokenBudget <= 0 {
		return
	}
	if e.IsPaused(t.ID) || e.IsDeleting(t.ID) || ctClosed(e, t.ID) {
		return
	}
	if isTerminalStatus(e.m.TaskStatus(t.ID)) {
		return
	}
	used, err := e.m.pg.TaskTokenTotal(t.ID)
	if err != nil || used < int64(t.TokenBudget) {
		return
	}
	reason := fmt.Sprintf("Token 预算已用尽（已用 %d，预算 %d）", used, t.TokenBudget)
	if err := e.m.ApplyTaskPauseReason(t.ID, reason); err != nil {
		log.Printf("[coord] task %s 预算暂停落库失败: %v", t.ID, err)
		return
	}
	e.PauseCoord(t.ID, fmt.Errorf("%s", reason))
	e.emitActivity(t, db.Activity{Worker: "system", Kind: "text", Summary: reason})
}

// ctClosed 报告该任务的图是否已关闭（用于预算闸的幂等早退）。
func ctClosed(e *Engine, taskID string) bool {
	ct, ok := coordGet(e, taskID)
	if !ok || ct == nil {
		return true
	}
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.closed
}
