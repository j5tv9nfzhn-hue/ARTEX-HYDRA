package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/tracelog"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
)

// isFKViolation reports whether err is a foreign-key violation — e.g. an activity
// insert whose exploration_id has no parent row. foreign_keys(ON) is part of the
// DSN contract (see config.SQLiteDSN), so this can actually fire.
func isFKViolation(err error) bool {
	return db.ForeignKeyViolation(err)
}

// dropReason classifies why an activity write was dropped, so the log can be
// grouped/analysed by cause rather than by raw error text.
func dropReason(err error) string {
	if code := db.ConstraintCode(err); code != "" {
		return code
	}
	return "write_error"
}

// bumpDrop increments and returns the running count of dropped (unpersistable)
// activity records for a task. Concurrent planner + worker emits race here, so the
// counter is an atomic behind sync.Map. The count in the log shows loss scale at a
// glance instead of forcing a grep-and-count.
func (e *Engine) bumpDrop(taskID string) int64 {
	v, _ := e.dropCnt.LoadOrStore(taskID, new(int64))
	return atomic.AddInt64(v.(*int64), 1)
}

// preview collapses newlines and trims s to a short rune-safe snippet for one-line
// log output (avoids dumping a multi-KB summary/detail into the log).
func preview(s string, n int) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

// model_error（provider/API 故障：LLM 层瞬时重试耗尽，或流已开始后中途断流）
// 收场的 work 不是「试过没做完」也不是真失败，而是外部抖动。默认把它当永久
// blocked 会白丢一条意图，所以这里对该终态额外重跑几次，每次之间退避一下，给
// provider 恢复的时间；重试期间若被暂停/终止/取消则立即让位给对应分支处理。
const (
	// model_error 收场后整条意图的重跑次数。
	//
	// 上限压到 4 次是成本导向：每次重跑都是一整轮 LLM + 工具调用，10 次会把
	// 「供应商抖动」放大成一次昂贵的自动循环。默认退避改为指数梯度（见
	// modelErrorRetryBackoff），4 次合计等待 3+6+12+24≈45 秒，足以扛过短时
	// 限流/排队；更长的大面积故障交给用户观察后手动重跑，不在这里无声烧钱。
	//
	// 硬失败（密钥失效、余额不足、参数非法）不值得等——重试不会让它们变好。
	// 连续硬失败多为配置/模型问题，继续重试只是烧钱，所以由
	// modelErrorHardFailRetries 单独限制到 2 次，尽快把问题暴露给用户。
	modelErrorRetries = 4
	// modelErrorHardFailRetries 是硬失败的意图重跑上限。
	modelErrorHardFailRetries = 2
	workControlWaitTimeout    = 30 * time.Second
)

var errWorkControlConflict = errors.New("work control conflict")

// 四类 ControlWork 与 Worker 干预错误经 applyIntentControl、controlIntent、
// sendWorkerMessage 以 writeErr 展示，应统一为中文。
// SteerWork、KillWork 返回 actool 工具结果，transitionIntentState 仅记录内部状态日志，
// 保留其中文模型输入和诊断语义。恢复冲突复用 errIntentCtrlStateConflictFmt。
const (
	errWorkControlNoRunningWorkFmt = "%w：意图 %d 没有正在运行的 Worker（可能已终止或尚未分配）"
	errWorkControlBusyFmt          = "%w：针对意图 %d 已有 %s 控制正在进行中"
	errWorkControlWaitFmt          = "等待意图 %d 完成 %s 收尾时发生错误：%w"
	errDetachedWorkerNotReady      = "Worker 尚未就绪"
)

// retryableWorkerModelError excludes errors already handled by the task router.
// In particular, a quota error after partial streaming advances the task cursor
// for the next LLM call but must not replay this whole intent on the backup.
func retryableWorkerModelError(reason harness.TerminalReason, err error) bool {
	return reason == harness.ReasonModelError && !isTaskLLMRuntimeError(err)
}

// Engine drives the event-driven exploration loop with real LLM agents
// (docs §4.3/§4.4): on asset/exploration-graph change (debounced) it wakes the
// planner, which reads the route, queries assets, judges goals and emits intents;
// N concurrent work agents claim intents and execute them. There is no
// simulation mode — an LLM provider is required. The planner/worker can be
// (re)installed at runtime (LLM configured from the UI); the loops always run
// but idle until an LLM is set.
type Engine struct {
	m        *Manager
	debounce time.Duration
	tr       *tracelog.Store // P0 会话语义旁路转录（nil = off）

	bc *Broadcaster // live activity pub/sub (SSE)

	started  sync.Map // taskID -> bool, so Run is idempotent per task
	lastAct  sync.Map // taskID -> int64 unix, last planner/worker activity (heartbeat)
	llmCalls sync.Map // taskID -> *int64, actual planner/worker/main-agent LLM calls
	paused   sync.Map // taskID -> bool, user-paused (planner + workers idle but loops alive)
	deleting sync.Map // taskID -> bool, delete barrier (no new task-owned writes)
	dropCnt  sync.Map // taskID -> *int64, running count of dropped (unpersistable) activity records

	// deleteMu makes installing the delete barrier atomic with registering a new
	// task operation. Once BeginDelete returns, every admitted writer is reflected
	// in inflight and every later writer is rejected.
	deleteMu sync.RWMutex

	// Every long-lived task goroutine (planner, workers and deadline coordinator)
	// runs under one task-scoped context. Successful deletion cancels that context,
	// waits for all goroutines, then releases every task-level Engine reference.
	runtimeMu sync.Mutex
	runtimes  map[string]*taskRuntime

	// per-task execution context: each planner.Plan / worker.Execute runs under it,
	// so pausing can CANCEL an in-flight run (not just skip the next one). Recreated
	// on resume since cancelling is one-shot. Every cancellation carries a named
	// cause so the activity trace can identify the initiating control path.
	execMu     sync.Mutex
	execCancel map[string]context.CancelCauseFunc
	execCtx    map[string]context.Context

	// Per-work control lets the planner kill a worker and lets the UI pause/cancel
	// one intent without pausing the whole task. The done channel closes only after
	// runWorkerStep has stopped writing and committed its final state.
	workMu sync.Mutex
	work   map[int64]*workExecution

	// steerBox queues planner course-corrections for a running work (keyed by intent
	// id). The worker's PreToolUse hook drains it before its next tool call and hands
	// the message to the model (blocking that call) so it re-plans — no kill needed.
	steerMu  sync.Mutex
	steerBox map[int64][]string

	plannerRound sync.Map // taskID -> int, planner round counter (for UI round separators)

	// dirGates 是方向代际门控（防强制终止后仍按旧方向派发浪费算力）：kill_work/steer_work
	// 推进方向代际；kill 额外冻结新意图领取（claimNext），直到新一轮规划开始/任一次
	// add_intent 成功解冻，或 dispatchHoldWindow 超时兜底。
	dirGates sync.Map // taskID -> *dirGate

	// 任务级超时(见 docs/任务级超时与收尾设计.md):
	settling     sync.Map // taskID -> bool, 任务已进入收尾时序(停止派/领新意图)
	deadline     sync.Map // taskID -> int64 unix, 绝对截止时刻(首次运行时盖章;0/缺省=不限)
	stamped      sync.Map // taskID -> bool, first_run_at 是否已盖章(本进程内只盖一次)
	inflight     sync.Map // taskID -> *int64, 在跑的 planner.Plan + worker.Execute 计数(用于 drain)
	coordStarted sync.Map // taskID -> bool, deadline 协调器是否已启动(Run/reload 去重)

	// drainMu guards drainCond and is held while a delete waits for in-flight
	// work to reach zero. Every decInflight that drops a task's counter to 0
	// broadcasts on drainCond, so waitTaskQuiescent blocks on a condition
	// variable instead of polling with a 10ms ticker. Under a burst of task
	// deletions the ticker version woke each waiter 100x/second purely to
	// re-read two fields — measurable CPU with no work done.
	drainMu   sync.Mutex
	drainCond *sync.Cond

	// resolve reports whether a task can currently reach an LLM at all — wired by
	// the server to the task-router. false means this task is deliberately
	// unavailable (for example an exhausted failover chain); there is no global
	// fallback.
	//
	// 【P4】它原先返回一对 (*agent.Planner, *agent.Worker) —— 旧调度链路的两个
	// 角色。角色下线后没有任何调用方会用到那两个值，唯一被读取的信息是
	// "两个都不是 nil"，即"这个任务能不能跑"。所以这里直接收敛成 bool：
	// 保留判断，去掉已经不存在的主语。resolveAuthoritative 一并删除——它只在
	// "拿到了非 nil 组合要不要放行"这个二选一里有意义，bool 化之后没有歧义。
	resolve func(t *Task) bool
	// readiness reports whether a global LLM provider is configured — the signal behind
	// Ready()/the llm_configured indicator. Wired once at startup; nil → not ready.
	readiness func() bool

	// intentSettledHook observes every intent run reaching a settled state
	// (done/blocked/exhausted/stopped/paused) — the host uses it to capture
	// timeline checkpoints (批次 6 / #29–#35). Set once via SetIntentSettledHook;
	// called from the worker goroutine, must not block.
	intentSettledHook func(t *Task, intentID int64, state string)
}

// SetIntentSettledHook wires the per-intent settle observer (see field doc).
func (e *Engine) SetIntentSettledHook(fn func(t *Task, intentID int64, state string)) {
	e.intentSettledHook = fn
}

// notifyIntentSettled invokes the settle hook defensively (foreign goroutine).
func (e *Engine) notifyIntentSettled(t *Task, intentID int64, state string) {
	e.traceIntentEv(t, intentID, "engine", "intent.settle", state, "")
	if e.intentSettledHook == nil || t == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[checkpoints] intent settled hook panic: %v", r)
		}
	}()
	e.intentSettledHook(t, intentID, state)
}

type taskRuntime struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type workExecution struct {
	cancel context.CancelCauseFunc
	done   chan error
	action string // user action: pause | cancel
	taskID string // owning task (direction gate in KillWork/SteerWork; empty = unknown)
}

// nextPlannerRound returns the next planner round number for a task (1-based).
func (e *Engine) nextPlannerRound(taskID string) int {
	v, _ := e.plannerRound.LoadOrStore(taskID, 0)
	n := v.(int) + 1
	e.plannerRound.Store(taskID, n)
	return n
}

// dirGate is the per-task direction-generation gate (see Engine.dirGates).
type dirGate struct {
	epoch  atomic.Uint64 // 方向代际：每次 kill/steer 递增
	hold   atomic.Bool   // true = 暂停领取新意图（kill 后、重评估前）
	holdAt atomic.Int64  // 冻结起始 unixnano（0=未冻结），超时自解除兜底
}

// dispatchHoldWindow 是 kill_work 冻结派发的兜底时长：正常情况下新一轮规划开始或任一次
// add_intent/restart 成功即提前解冻；规划者已停/goalless 任务无人派发时，超时后自动恢复
// 领取，避免把没有规划者的任务永久卡死。
const dispatchHoldWindow = 2 * time.Minute

// dirGateFor returns (creating on first use) the task's direction gate.
func (e *Engine) dirGateFor(taskID string) *dirGate {
	v, _ := e.dirGates.LoadOrStore(taskID, &dirGate{})
	return v.(*dirGate)
}

// DirectionEpoch returns the task's current direction epoch (0 = never bumped).
func (e *Engine) DirectionEpoch(taskID string) uint64 {
	return e.dirGateFor(taskID).epoch.Load()
}

// bumpDirection records a direction change for the task. hard=true (kill_work)
// additionally freezes new claims until AckDispatch/dispatchHoldWindow; soft bumps
// (steer_work) only invalidate in-flight planner rounds (the add_intent gate).
func (e *Engine) bumpDirection(taskID string, hard bool) {
	g := e.dirGateFor(taskID)
	g.epoch.Add(1)
	if hard {
		g.hold.Store(true)
		g.holdAt.Store(time.Now().UnixNano())
	}
	name := "soft"
	if hard {
		name = "hard"
	}
	e.traceEv(taskID, "engine", "dir.gate", name, "")
}

// AckDispatch releases the dispatch freeze after a fresh decision was made: a new
// planner round starting (re-evaluation underway) or a successful add_intent /
// restart dispatch. Idempotent; a later kill re-freezes via bumpDirection.
func (e *Engine) AckDispatch(taskID string) {
	g := e.dirGateFor(taskID)
	g.hold.Store(false)
	g.holdAt.Store(0)
}

// dispatchHeld reports whether claimNext should skip claiming new intents.
func (e *Engine) dispatchHeld(taskID string) bool {
	g := e.dirGateFor(taskID)
	if !g.hold.Load() {
		return false
	}
	at := g.holdAt.Load()
	if at == 0 {
		return false
	}
	if time.Since(time.Unix(0, at)) >= dispatchHoldWindow {
		g.hold.Store(false) // 兜底解冻，防永久卡死
		return false
	}
	return true
}

// Pause stops a task: marks it paused AND cancels any in-flight planner/worker run
// for it (a long worker.Execute would otherwise keep going until it finishes).
func (e *Engine) Pause(taskID string, cause error) {
	e.paused.Store(taskID, true)
	e.traceEv(taskID, "engine", "task.control", "pause", errDetail(cause))
	e.cancelExec(taskID, cause)
	// 【B1 改道点 2/4】coord 模式下 cancelExec 只取消"正在跑的 run"，而新链路里
	// 活着的是**整张图**（根 + 在飞的子 agent）。暂停必须真的把根停掉，否则
	// UI 显示已暂停、根 agent 还在派活。旧链路没有这个动作，因为它假设 loop
	// 一直活着、只要 next tick 不再领活就自然静止。
	e.PauseCoord(taskID, cause)
}

// checkTokenBudget 是任务级 Token 预算硬闸：每次 run（worker 意图 / 规划轮）收尾后
// 检查本任务在 llm_usage 账本上的累计 input+output 是否达到预算上限（计量口径见
// db.TaskTokenTotal），达到则自动暂停任务并在活动流留痕。
//
// 为什么放在 run 收尾而不是 run 启动前：llmrec 每次 LLM 调用返回即落账，收尾时
// 读到的读数必然包含刚结束这一 run 的全部消耗，不会漏按本 run 计费；且此刻不在
// LLM 调用临界区，暂停不会打断正在进行的一次调用。两道调用点覆盖全部消耗路径：
// runIntent（worker 池 + 人工消息直投，defer 覆盖所有 return 分支）与 plannerLoop
// 的常规规划轮。
//
// 顺序上先落库（ApplyTaskPause 的 Guarded UPDATE）再停引擎：落库失败时任务保持
// 原样、日志留痕，下一轮 run 收尾会重试，绝不出现「内存已暂停、UI 仍显示运行」的
// 劈叉。Guarded UPDATE 未命中（任务已被用户暂停/删除/进入收尾/变终态）说明别的
// 路径已接管，同样静默让位——这里不区分失败原因，统一保持任务现状。
//
// 幂等：预算<=0（不限）、已暂停、删除中、收尾中、已终态一律早退；调用点是稀疏的
// run 收尾（每个任务同一时刻只有少量在跑），自身不会并发触发。
func (e *Engine) checkTokenBudget(t *Task) {
	if t == nil {
		return
	}
	lifecycle := t.lifecycleSnapshot()
	if lifecycle.TokenBudget <= 0 {
		return // 0=不限
	}
	if lifecycle.Paused || e.IsPaused(t.ID) || e.IsDeleting(t.ID) || e.isSettling(t.ID) {
		return
	}
	if isTerminalStatus(e.m.TaskStatus(t.ID)) {
		return
	}
	used, err := e.m.pg.TaskTokenTotal(t.ID)
	if err != nil {
		log.Printf("[budget] task %s 读取 Token 累计用量失败: %v", t.ID, err)
		return
	}
	if used < int64(lifecycle.TokenBudget) {
		return
	}
	if err := e.m.ApplyTaskPauseReason(t.ID, fmt.Sprintf("%s（已用 %d，预算 %d）", agent.AbortPausedByTokenBudget.Short, used, lifecycle.TokenBudget)); err != nil {
		log.Printf("[budget] task %s 预算已用尽（已用 %d/预算 %d）但自动暂停未落库（可能已被其他操作接管，或 DB 写入失败，下一轮 run 收尾会重试）: %v",
			t.ID, used, lifecycle.TokenBudget, err)
		return
	}
	// 落库成功后再停引擎：取消在跑的 planner/worker（被中断的意图退回 open，恢复后
	// 重新领取），worker 池不再领新意图。
	e.Pause(t.ID, agent.AbortPausedByTokenBudget)
	e.emitActivity(t, db.Activity{Worker: "system", Kind: "text",
		Summary: fmt.Sprintf(budgetPausedSummaryFmt, used, lifecycle.TokenBudget)})
	log.Printf("[budget] task %s Token 预算已用尽（已用 %d/预算 %d），已自动暂停", t.ID, used, lifecycle.TokenBudget)
}

// BeginDelete installs an execution barrier before task data/files are removed.
// The temporary pause is not a user pause. The server serializes this transition
// with lifecycle admission and tells AbortDelete whether the persisted task is
// paused/queued if cleanup fails.
func (e *Engine) BeginDelete(taskID string) bool {
	e.deleteMu.Lock()
	if _, loaded := e.deleting.LoadOrStore(taskID, true); loaded {
		e.deleteMu.Unlock()
		return false
	}
	e.paused.Store(taskID, true)
	e.deleteMu.Unlock()
	e.cancelExec(taskID, agent.AbortTaskDeleted)
	// 【B1 改道点 4/4】删除屏障之前先关图：否则删除过程中子 agent 还在往库里写，
	// 清理与写入赛跑。放在 cancelExec 之后是刻意的——先断执行上下文，再关图，
	// 让在飞的工具调用先收到取消，而不是反过来。
	e.DeleteCoord(taskID)
	return true
}

func (e *Engine) AbortDelete(taskID string, keepPaused bool) {
	e.deleteMu.Lock()
	if !e.IsDeleting(taskID) {
		e.deleteMu.Unlock()
		return
	}
	e.deleting.Delete(taskID)
	if !keepPaused {
		e.paused.Delete(taskID)
	}
	e.deleteMu.Unlock()
	if !keepPaused && e.m != nil {
		if t, ok := e.m.Task(taskID); ok {
			t.Notify()
		}
	}
}

func (e *Engine) IsDeleting(taskID string) bool {
	_, ok := e.deleting.Load(taskID)
	return ok
}

// registerTaskRoutines reserves count goroutines in the task runtime. Callers
// hold deleteMu for reading so StopTask cannot race WaitGroup.Add with Wait.
func (e *Engine) registerTaskRoutines(parent context.Context, taskID string, count int) *taskRuntime {
	e.runtimeMu.Lock()
	defer e.runtimeMu.Unlock()
	rt := e.runtimes[taskID]
	if rt == nil {
		ctx, cancel := context.WithCancel(parent)
		rt = &taskRuntime{ctx: ctx, cancel: cancel}
		e.runtimes[taskID] = rt
	}
	rt.wg.Add(count)
	return rt
}

func runTaskRoutine(rt *taskRuntime, fn func(context.Context)) {
	go func() {
		defer rt.wg.Done()
		fn(rt.ctx)
	}()
}

// StopTask permanently stops every long-lived goroutine and removes all Engine
// state for a successfully deleted task. The delete barrier remains installed
// until cleanup finishes, so no new task operation can race the teardown.
func (e *Engine) StopTask(taskID string) {
	e.deleteMu.Lock()
	e.deleting.Store(taskID, true)
	e.deleteMu.Unlock()

	e.cancelExec(taskID, agent.AbortTaskDeleted)
	e.runtimeMu.Lock()
	rt := e.runtimes[taskID]
	if rt != nil {
		rt.cancel()
	}
	e.runtimeMu.Unlock()
	if rt != nil {
		rt.wg.Wait()
	}

	e.execMu.Lock()
	if cancel := e.execCancel[taskID]; cancel != nil {
		cancel(agent.AbortTaskDeleted)
	}
	delete(e.execCancel, taskID)
	delete(e.execCtx, taskID)
	e.execMu.Unlock()

	e.runtimeMu.Lock()
	if e.runtimes[taskID] == rt {
		delete(e.runtimes, taskID)
	}
	e.runtimeMu.Unlock()

	e.started.Delete(taskID)
	e.lastAct.Delete(taskID)
	e.llmCalls.Delete(taskID)
	e.paused.Delete(taskID)
	e.dropCnt.Delete(taskID)
	e.plannerRound.Delete(taskID)
	e.settling.Delete(taskID)
	e.deadline.Delete(taskID)
	e.stamped.Delete(taskID)
	e.inflight.Delete(taskID)
	e.coordStarted.Delete(taskID)
	e.deleteMu.Lock()
	e.deleting.Delete(taskID)
	e.deleteMu.Unlock()
}

// cancelExec cancels a task's current per-task exec context (any in-flight
// planner.Plan / worker.Execute), if present. Shared by Pause and the settle
// sequence's hard-drain backstop.
func (e *Engine) cancelExec(taskID string, cause error) {
	e.execMu.Lock()
	if cancel := e.execCancel[taskID]; cancel != nil {
		cancel(cause)
	}
	e.execMu.Unlock()
}

// Resume un-pauses a task and nudges a fresh planning round. The next exec under
// it gets a fresh (uncancelled) context.
func (e *Engine) Resume(t *Task) {
	// BeginDelete owns the pause barrier once deletion starts. A concurrent
	// resume must never clear it and let a planner/worker re-enter while cleanup
	// is waiting for task operations to drain.
	if t == nil {
		return
	}
	e.deleteMu.RLock()
	defer e.deleteMu.RUnlock()
	if e.IsDeleting(t.ID) {
		return
	}
	e.paused.Delete(t.ID)
	e.traceEv(t.ID, "engine", "task.control", "resume", "")
	// 【B1 改道点 3/4】coord 模式下"恢复"不等于"唤醒规划轮"：根 agent 已经终结
	// （暂停时整张图被关掉），必须重建一张图。ResumeCoord 内部区分两种情况——
	// 图还在就 nudge，图没了就重新 StartTask。
	if e.ResumeCoord(t) {
		return
	}
	t.Notify()
}

// execContextFor returns a live per-task context derived from parent, recreating
// it if a prior pause cancelled it.
func (e *Engine) execContextFor(parent context.Context, taskID string) context.Context {
	e.execMu.Lock()
	defer e.execMu.Unlock()
	if e.IsPaused(taskID) {
		// never hand out a live context while paused (guards the claim→Execute race)
		c, cancel := context.WithCancelCause(parent)
		cancel(agent.AbortPausedRaceGuard)
		return c
	}
	if c := e.execCtx[taskID]; c != nil && c.Err() == nil {
		return c
	}
	c, cancel := context.WithCancelCause(parent)
	e.execCtx[taskID] = c
	e.execCancel[taskID] = cancel
	return c
}

// IsPaused reports whether a task is user-paused.
func (e *Engine) IsPaused(taskID string) bool {
	v, ok := e.paused.Load(taskID)
	return ok && v.(bool)
}

// Started reports whether the engine loops are running for a task.
func (e *Engine) Started(taskID string) bool {
	_, ok := e.started.Load(taskID)
	return ok
}

// LastActivity returns the unix time of the last planner/worker activity for a
// task (0 if none yet).
func (e *Engine) LastActivity(taskID string) int64 {
	if v, ok := e.lastAct.Load(taskID); ok {
		return v.(int64)
	}
	return 0
}

// BeginLLMCall/EndLLMCall track actual provider calls separately from the
// scheduler's task-operation counter. A task can have live loops while all of
// them are waiting for a trigger; that state must remain idle in the UI.
func (e *Engine) BeginLLMCall(taskID string) {
	v, _ := e.llmCalls.LoadOrStore(taskID, new(int64))
	atomic.AddInt64(v.(*int64), 1)
	e.traceEv(taskID, "engine", "llm.call.begin", "", "")
}

func (e *Engine) EndLLMCall(taskID string) {
	if v, ok := e.llmCalls.Load(taskID); ok {
		p := v.(*int64)
		if atomic.AddInt64(p, -1) <= 0 {
			atomic.StoreInt64(p, 0)
		}
	}
	e.traceEv(taskID, "engine", "llm.call.end", "", "")
}

func (e *Engine) ActiveLLMCalls(taskID string) int64 {
	if v, ok := e.llmCalls.Load(taskID); ok {
		return atomic.LoadInt64(v.(*int64))
	}
	return 0
}

func (e *Engine) touch(taskID string) { e.lastAct.Store(taskID, time.Now().Unix()) }

func NewEngine(m *Manager) *Engine {
	e := &Engine{m: m, debounce: 800 * time.Millisecond, bc: NewBroadcaster(),
		execCancel: map[string]context.CancelCauseFunc{}, execCtx: map[string]context.Context{},
		work: map[int64]*workExecution{}, steerBox: map[int64][]string{},
		runtimes: map[string]*taskRuntime{}}
	e.drainCond = sync.NewCond(&e.drainMu)
	return e
}

// registerWork records the cancel for the work currently running intentID.
// Kept for callers that don't know the owning task (tests); production runs use
// registerWorkFor so KillWork/SteerWork can drive the per-task direction gate.
func (e *Engine) registerWork(intentID int64, cancel context.CancelCauseFunc) {
	e.registerWorkFor("", intentID, cancel)
}

// registerWorkFor records the cancel AND the owning task id for the work currently
// running intentID (taskID powers the direction gate in KillWork/SteerWork).
func (e *Engine) registerWorkFor(taskID string, intentID int64, cancel context.CancelCauseFunc) {
	e.workMu.Lock()
	e.work[intentID] = &workExecution{cancel: cancel, done: make(chan error, 1), taskID: taskID}
	e.workMu.Unlock()
}

// detachWork removes the live control handle once Execute has returned. complete
// must be called after the final intent state write so a waiting cancel handler can
// safely delete the worker's blackboard output without racing a late write.
func (e *Engine) detachWork(intentID int64) (action string, complete func(error)) {
	e.workMu.Lock()
	run := e.work[intentID]
	if run != nil {
		delete(e.work, intentID)
		action = run.action
		run.cancel(agent.AbortWorkFinished) // release resources (no-op if already cancelled)
	}
	e.workMu.Unlock()
	e.steerMu.Lock()
	delete(e.steerBox, intentID) // drop any undelivered steering for a finished work
	e.steerMu.Unlock()
	if run == nil {
		return action, func(error) {}
	}
	return action, func(err error) { run.done <- err }
}

// ControlWork requests a user-visible pause or cancellation and waits until the
// worker has fully stopped writing. Cancellation cleanup is performed by the API
// handler after this returns; pause state is committed by runWorkerStep itself.
func (e *Engine) ControlWork(ctx context.Context, intentID int64, action string) error {
	if action != "pause" && action != "cancel" {
		return fmt.Errorf("unsupported work action %q", action)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	e.workMu.Lock()
	run := e.work[intentID]
	if run == nil {
		e.workMu.Unlock()
		return fmt.Errorf(trMsg(errWorkControlNoRunningWorkFmt), errWorkControlConflict, intentID)
	}
	if run.action != "" {
		e.workMu.Unlock()
		return fmt.Errorf(trMsg(errWorkControlBusyFmt), errWorkControlConflict, intentID, run.action)
	}
	run.action = action
	done := run.done
	cause := error(agent.AbortWorkPausedByUser)
	if action == "cancel" {
		cause = agent.AbortWorkCancelledByUser
	}
	run.cancel(cause)
	e.workMu.Unlock()

	timer := time.NewTimer(workControlWaitTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		e.releaseWorkControl(intentID, run, action)
		return fmt.Errorf(trMsg(errWorkControlWaitFmt), intentID, action, ctx.Err())
	case <-timer.C:
		e.releaseWorkControl(intentID, run, action)
		return fmt.Errorf(trMsg(errWorkControlWaitFmt), intentID, action, context.DeadlineExceeded)
	}
}

// releaseWorkControl drops only this caller's reservation after its wait is
// cancelled. The work context stays cancelled; runWorkerStep recognizes the
// named cancellation cause and settles the intent into the recoverable paused
// state even if the HTTP caller has gone away.
func (e *Engine) releaseWorkControl(intentID int64, run *workExecution, action string) {
	e.workMu.Lock()
	if current := e.work[intentID]; current == run && current.action == action {
		current.action = ""
	}
	e.workMu.Unlock()
}

func transitionIntentState(store *db.ExplorationStore, intentID int64, expected, state string) error {
	changed, err := store.CompareAndSetIntentState(intentID, expected, state)
	if err != nil {
		return err
	}
	if !changed {
		// 此 CAS 冲突只在返回 bool 的 runIntent 内使用，记录 log.Printf 或控制流程，
		// 不作为 HTTP 响应和 Agent 工具结果返回，保留诊断语义。
		return fmt.Errorf("%w: 意图 %d 不再是 %s 状态", db.ErrIntentStateConflict, intentID, expected)
	}
	return nil
}

// SteerWork queues a mid-run course-correction for the work running intentID (the
// planner's steer_work tool). The worker delivers it before its next tool call and
// re-plans — no kill. Errors if no work is currently running that intent.
func (e *Engine) SteerWork(intentID int64, msg string) error {
	// SteerWork 连接 Planner、MainAgent 的 steer_work 工具，
	// 错误由 actool.Errorf 返回模型，保持既有中文语义，不改变工具反馈。
	if strings.TrimSpace(msg) == "" {
		return fmt.Errorf("纠偏消息不能为空")
	}
	e.workMu.Lock()
	run := e.work[intentID]
	e.workMu.Unlock()
	if run == nil {
		return fmt.Errorf("意图 %d 当前没有运行中的 work（可能已结束或未被领取）", intentID)
	}
	// 纠偏 = 方向变化信号（软）：推进方向代际，让规划者本轮余下的派发被门控拒绝、
	// 下一轮重新评估后再派发；不冻结领取（纠偏不打断运行中的 work）。
	if run.taskID != "" {
		e.bumpDirection(run.taskID, false)
	}
	e.steerMu.Lock()
	e.steerBox[intentID] = append(e.steerBox[intentID], msg)
	e.steerMu.Unlock()
	e.traceWorkEv(run.taskID, intentID, "planner", "steer.order", "", tracelog.Clip(msg, 200))
	return nil
}

// drainSteer pops the oldest queued steering message for intentID (FIFO), if any.
func (e *Engine) drainSteer(intentID int64) (string, bool) {
	e.steerMu.Lock()
	defer e.steerMu.Unlock()
	q := e.steerBox[intentID]
	if len(q) == 0 {
		return "", false
	}
	msg := q[0]
	if len(q) == 1 {
		delete(e.steerBox, intentID)
	} else {
		e.steerBox[intentID] = q[1:]
	}
	return msg, true
}

// steerHooks wraps the guard's hook runner so the planner can steer a running work:
// before each tool call it drains a queued course-correction (if any) and blocks the
// call, handing the message back to the model — which re-plans its next step instead
// of running the tool. No queued message → the guard behaves exactly as before.
// 它同时负责「空转回合」的续跑，见 Stop。
type steerHooks struct {
	inner harness.HookRunner
	drain func() (string, bool)
	// nudges 是本条意图已注入的空转续跑次数，上限 limit。指针:harness 持有的是
	// steerHooks 的值拷贝，计数必须共享同一份。
	nudges *atomic.Int64
	// limit 是空转续跑的次数上限，由 Engine.emptyTurnNudgeLimit() 从「空响应重试
	// 次数」解析而来。<=0 = 不介入(用户显式关掉了这层)。
	limit int
	// label 形如 "worker-1 (#42)"，用于日志与拦截记录的 agent_name。
	label string
}

// 空转回合(只有思考、既无正文也无工具调用)续跑次数的默认值，与 SDK 空响应重试的
// 内置默认(norma/llm/openai.go 的 emptyResponseRetries)保持一致——两层共用同一个
// 旋钮，不配置时的行为也该对齐。解析见 Engine.emptyTurnNudgeLimit。
//
// 注意这个数是「一条意图的总量」，不是「连续几次」:harness 自身的 stopHookActive
// 已经限死了连续空转只推一次——推完那一轮若还是空转，Stop 钩子不会再被调到，run 直接
// 收场;只有真正发生过一次工具回合，配额才刷新(norma/harness/query.go:534)。所以这个
// 闸挡的是「工具 → 空转 → 推 → 工具 → 空转」这种病态循环，别让它把意图预算耗光。
const defaultEmptyTurnNudges = 2

// emptyTurnNudge 是空转回合注入的续跑指令。
//
// 这种回合在 harness 眼里是一次自然结束(stop_reason=end_turn 且无 tool_use)，五层
// LLM 重试一层都不适用——它不是错误，是模型「想完了但没动手」。SDK 的空响应重试也
// 够不着:它以「有没有 yield 过事件」判空，而思考增量本身就是事件(norma/llm/openai.go
// 的 SEThinkingDelta)，所以 thinking-only 不算空。何况那层是原样重发整个 prompt，
// 对这种由上下文形状决定的空转，重发只会让模型再想一遍。这里换成追加一条指令，让它
// 带着已经产出的思考继续，输入变了才有理由给出不同的行为。
const emptyTurnNudge = "【空转提醒】你上一轮只输出了思考过程，既没有给出正文回复，也没有调用任何工具，" +
	"这一轮等于没有产出。请直接执行你刚才想好的下一步：要么调用工具，要么给出结论文字。不要重复思考。"

// isThinkingOnlyTurn reports whether the latest assistant turn produced neither
// text nor a tool call — i.e. the model spent the whole round thinking.
func isThinkingOnlyTurn(messages []llm.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role != llm.RoleAssistant {
			continue
		}
		return strings.TrimSpace(m.Text()) == "" && len(m.ToolUses()) == 0
	}
	return false
}

func (h steerHooks) PreToolUse(ctx context.Context, name string, input []byte) (bool, string, []byte) {
	if msg, ok := h.drain(); ok {
		return true, "【规划者实时纠偏】" + msg +
			"\n（这是规划者对本意图的即时指令；本次工具调用未执行，请据此调整下一步。若与你当前打算冲突，以此为准。）", nil
	}
	if h.inner != nil {
		return h.inner.PreToolUse(ctx, name, input)
	}
	return false, "", nil
}

func (h steerHooks) PostToolUse(ctx context.Context, name string, input, result []byte, isErr bool) {
	if h.inner != nil {
		h.inner.PostToolUse(ctx, name, input, result, isErr)
	}
}

// Stop 在 guard 原有语义之上补一层「空转回合」续跑:模型只输出了思考、既没给正文
// 也没调工具时，harness 会把它当成自然结束并以空 summary 收场(query.go 的
// ReasonCompleted + asst.Text())，一条本来还没做完的意图就这样断在半路。此时注入
// 一条续跑指令，让模型带着已有思考接着走。
func (h steerHooks) Stop(ctx context.Context, messages []llm.Message) (bool, []string, string) {
	var (
		prevent  bool
		blocking []string
		msg      string
	)
	if h.inner != nil {
		prevent, blocking, msg = h.inner.Stop(ctx, messages)
	}
	// inner 已经决定硬停、或已经要注入自己的续跑消息 → 尊重它，不再叠加。
	// limit<=0 = 用户把「空响应重试次数」配成了 -1，即显式关掉这层。
	if prevent || len(blocking) > 0 || h.nudges == nil || h.limit <= 0 || !isThinkingOnlyTurn(messages) {
		return prevent, blocking, msg
	}
	n := h.nudges.Add(1)
	if n > int64(h.limit) {
		log.Printf("[work %s] 空转回合(仅思考、无正文无工具)已达续跑上限 %d，放行收场", h.label, h.limit)
		return prevent, blocking, msg
	}
	log.Printf("[work %s] 空转回合(仅思考、无正文无工具)，注入续跑指令 (%d/%d)", h.label, n, h.limit)
	return false, []string{emptyTurnNudge}, ""
}

// KillWork cancels the in-flight work running intentID (planner's kill_work tool).
// The work's agent-core session honors ctx cancellation and aborts promptly.
func (e *Engine) KillWork(intentID int64) error {
	e.workMu.Lock()
	run := e.work[intentID]
	e.workMu.Unlock()
	if run == nil {
		// KillWork 连接 Planner/MainAgent/Auto 的 kill_work 工具，错误是模型输入，
		// 处理原则与 SteerWork 一致。
		return fmt.Errorf("意图 %d 当前没有运行中的 work（可能已结束或未被领取）", intentID)
	}
	// 强制终止 = 方向级信号：推进方向代际并冻结新派发，直到规划者重新评估
	// （见 dirGate / claimNext / addOneIntent 门控），避免按旧方向继续派发浪费算力。
	if run.taskID != "" {
		e.bumpDirection(run.taskID, true)
	}
	e.traceWorkEv(run.taskID, intentID, "planner", "kill.order", "", "")
	run.cancel(agent.AbortKilledByPlanner)
	return nil
}

// Broadcaster exposes the engine's live activity pub/sub (used by the SSE handler).
func (e *Engine) Broadcaster() *Broadcaster { return e.bc }

// liveBroadcaster 返回实时帧广播器；engine 还没装配时给 nil。
// newLiveEmitter 对 nil 广播器是静默丢弃，所以这条路径在无 LLM 配置、
// 或单元测试只起了 Server 的情况下不会再把整个进程打挂（历史上 chat 链路
// 就因为这个 nil 解引用 panic 掉过测试进程）。
func (s *Server) liveBroadcaster() *Broadcaster {
	if s == nil || s.engine == nil {
		return nil
	}
	return s.engine.Broadcaster()
}

// emitActivity 从一个入口持久化捕获步骤并分发给 SSE 订阅者，避免数据库与流不同步。
// 以下任务级摘要没有 node_id，只显示于活动转录。planner.workerOutput 与
// get_worker_output 仅按 intent node_id 读取 result、text，因此不会回读这些摘要。
// 中文化不改变模型上下文，%d 格式参数保持原样。[[G132]]
const (
	goallessTaskDoneSummary = "所有目标均已达成，直接投入的意图也已执行完毕，任务结束"
	plannerRoundSummaryFmt  = "第 %d 轮规划"
	// budgetPausedSummaryFmt 是预算闸自动暂停后的任务级留痕（activity，无 node_id），
	// 参数为已用量与预算上限；文案同时给出恢复办法，用户看活动流即可自助处理。
	budgetPausedSummaryFmt = "Token 预算已用尽（已用 %d/预算 %d），任务已自动暂停；可在任务设置中调高预算后恢复"
)

func (e *Engine) emitActivity(t *Task, r db.Activity) db.Activity {
	// 调了不存在的工具也要进账本：meteredTool 只包住解析成功的工具，这类调用走不到
	// 它，过去只在 activity 里留一行，账面错误率因此偏低。
	if unknownToolResult(r) {
		e.meterUnknownTool(t, r)
	}
	id, err := e.appendActivity(t, r)
	if err != nil {
		// NO LONGER SILENT: dropping a record breaks command↔result pairing in the
		// trace — a tool_use whose tool_result was lost shows as "执行中" forever, and
		// a lost 'result'/'round' record leaves the session with no summary ("无总结").
		// Everything needed to分析根因 goes into ONE error-level line: reason class,
		// summary preview, running drop count for this task, and — on the FK case — a
		// live probe of WHY the parent exploration is unreachable.
		n := e.bumpDrop(t.ID)
		diag := ""
		// On the FK-parent failure (23503) probe the live DB so the log records WHY the
		// exploration is unreachable (row gone / wrong expID) instead of just that it is.
		if isFKViolation(err) {
			storeID := t.Store.ID()
			if exists, refs, maxID, dErr := e.m.pg.ExplorationDiag(storeID); dErr != nil {
				diag = fmt.Sprintf(" | FK诊断查询失败(store.expID=%d task.ExpID=%d): %v", storeID, t.ExpID, dErr)
			} else {
				diag = fmt.Sprintf(" | FK诊断: store.expID=%d task.ExpID=%d exploration存在=%v 引用它的task数=%d MAX(exploration.id)=%d",
					storeID, t.ExpID, exists, refs, maxID)
			}
		}
		log.Printf("[activity] task %s 丢弃活动记录(该任务累计第 %d 条) worker=%s kind=%s tool=%s tuid=%s reason=%s summary=%q: %v%s",
			t.ID, n, r.Worker, r.Kind, r.Tool, r.ToolUseID, dropReason(err), preview(r.Summary, 80), err, diag)
		e.touch(t.ID)
		return r
	}
	r.ID = id
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now()
	}
	e.bc.Publish(t.ID, r)
	e.touch(t.ID)
	return r
}

// appendActivity 的中止线：activityAppendAttempts 次尝试 + 递增退避。
// 上限的意义是「宁可多等，不轻易丢配对记录」：busy_timeout 已经承担了锁等待，
// 这里的退避针对的是「等待被立即拒绝」的路径（如快照失效），几十到几百毫秒后
// 重试就能在对方提交后成功。
const activityAppendAttempts = 4

// activityAppendBackoff[i] 是第 i+1 次失败后的等待时长。
var activityAppendBackoff = [...]time.Duration{
	100 * time.Millisecond,
	400 * time.Millisecond,
	1200 * time.Millisecond,
}

// appendActivity persists one activity row, retrying on write failure.
// Concurrent planner + worker inserts into the same exploration's activity log
// occasionally fail under SQLite write contention; the retry keeps a failed
// record from being dropped, because a dropped tool_use/tool_result row leaves
// the UI stuck on 「执行中」 forever (see emitActivity). Every failure is logged.
func (e *Engine) appendActivity(t *Task, r db.Activity) (int64, error) {
	var id int64
	var err error
	for attempt := 1; attempt <= activityAppendAttempts; attempt++ {
		if id, err = t.Store.AppendActivity(r); err == nil {
			if attempt > 1 {
				log.Printf("[activity] task %s 写入第 %d 次重试成功 (worker=%s kind=%s tool=%s)",
					t.ID, attempt, r.Worker, r.Kind, r.Tool)
			}
			return id, nil
		}
		log.Printf("[activity] task %s 写入失败 (第 %d/%d 次, worker=%s kind=%s tool=%s expID=%d): %v",
			t.ID, attempt, activityAppendAttempts, r.Worker, r.Kind, r.Tool, t.Store.ID(), err)
		if attempt < activityAppendAttempts {
			time.Sleep(activityAppendBackoff[attempt-1])
		}
	}
	return 0, err
}

// SetReadiness wires the global "an LLM provider is configured" predicate (read by
// Ready() / the llm_configured indicator). Called once at startup.
func (e *Engine) SetReadiness(fn func() bool) { e.readiness = fn }

// SetTaskRunnable installs the per-task runnability predicate read by ReadyFor.
//
// 【P4】原方法名 SetAuthoritativeAgentResolver。它带回来的东西已经不是"agent
// 解析器"——没有 agent 可以被解析了，只剩"这条任务能不能拿到 LLM"这一个布尔
// 事实。改名的理由是让签名与语义一致：一个返回 bool 的函数不该叫 Resolver。
//
// Called once at startup before any task loop runs, so no lock is needed on
// reads. Task-level failover chains use this so a fully exhausted chain cannot
// silently bypass its configured boundary.
func (e *Engine) SetTaskRunnable(fn func(t *Task) bool) {
	e.resolve = fn
}

// taskRunnable asks the task-router resolver whether a task can reach an LLM.
// false means the task is deliberately unavailable (e.g. an exhausted failover
// chain); there is no global fallback.
func (e *Engine) taskRunnable(t *Task) bool {
	if e.resolve == nil {
		return false
	}
	return e.resolve(t)
}

// Ready reports whether a global LLM provider is configured (via the readiness
// predicate wired at startup).
func (e *Engine) Ready() bool {
	return e.readiness != nil && e.readiness()
}

// ReadyFor reports whether a specific task can reach an LLM through the
// task-router. An explicit task profile chain can be runnable even when no global
// default provider is configured, so task status must not rely on Ready alone.
func (e *Engine) ReadyFor(t *Task) bool {
	return e.taskRunnable(t)
}

// Run starts a task's scheduling.
//
// 【P4】旧实现在这里起 plannerLoop + N 个 workerLoop，再由 B1 改道点在第一行
// 判断 coord 是否接管。旧骨架删除后不再有两个分支：Run 只有一件事要做——把
// 任务交给 coord，起一个根 agent，由它自己派活、自己等、自己收尾。
//
// 因此 legacy 回退（ARTEX_SCHED=legacy）也随之失效：已经没有可回退的实现。
// 保留这个入口而不是改名，是因为 30+ 处调用方用的是 "Run"，而它表达的语义
// （"让这个任务开始跑"）并没有变。
func (e *Engine) Run(ctx context.Context, t *Task) {
	if t == nil {
		return
	}
	e.RunCoord(ctx, t)
}

// taskExecutionPaused reports whether a context cancellation cause is a *task-level*
// pause (user pause, reload, admission queue…) rather than a terminal stop. It
// decides whether an interrupted run's intent goes back to the frontier or is
// settled for good, so it is platform semantics — independent of which role ran
// the work.
//
// 【P4】它一度随旧调度代码一起被我误删：唯一的调用者在 runIntent 里，而 runIntent
// 已经删掉了。但这条分类规则本身仍有意义——引擎的暂停/删除/恢复路径都要用它，
// 测试也直接调用它。删代码时要分清"角色专属"和"平台专属"，这是后者。
func taskExecutionPaused(cause error) bool {
	var abort *agent.AbortCause
	if !errors.As(cause, &abort) {
		return false
	}
	switch abort.Code {
	case "paused_by_user", "paused_by_orchestrator", "paused_on_reload", "paused_on_startup", "paused_race_guard",
		"paused_by_token_budget",
		"queued_for_admission", "llm_unavailable_queued", "task_deleted":
		return true
	default:
		return false
	}
}

func sleepCtx(ctx context.Context, d time.Duration) (done bool) {
	select {
	case <-ctx.Done():
		return true
	case <-time.After(d):
		return false
	}
}
