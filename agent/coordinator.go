package agent

// Coordinator —— 调度层的唯一中枢（重构 P0，取代 Planner / Reviewer 双角色）。
//
// ── 为什么要换 ────────────────────────────────────────────────────────────────
//
// 旧调度层的形状是「四角色 + 数据库总线」：
//
//	图变化 → debounce → 唤醒 Planner → Planner 重读全图 → 写 frontier 表
//	      → Worker claim 表 → 执行 → 写交接单表 → 唤醒 Reviewer
//	      → Reviewer 落判决表 → 情报闸门 → 唤醒 Planner
//
// 每一次方向调整要穿过两次角色切换和四次持久化，且 Planner 每轮没有会话记忆
// （态势被迫 move-out 到 user 输入，见 planner.go 的注释），于是必须再造一个
// Reviewer 把 Worker 的原始输出压成能喂回去的结论。仓库自己的实测数据是
// 1,662 次工具调用 → 41 条 fact → 1 条 finding，input:output = 46:1。
//
// 新形状只有一条路径：
//
//	父 agent 决定 → delegate_task() → 子 agent 立刻在 mailbox 里收到任务
//	             → 子 agent 干活 → submit_report() 把它写成一条 message
//	             → 消息被追加进父 agent 的下一轮上下文
//
// 没有 planner 唤醒、没有 frontier 表、没有判决台账。方向由「谁持有 spawn 权限」
// 决定：根 agent 是唯一能派活的角色，它的"规划"就是它自己的下一步。
//
// ── 三条硬约束（对应 strix 的三根支柱，但都用内存 + 现有 store 实现）───────
//
//  1. 消息只能投递给活着的 agent。终态且不可恢复的 agent 收到的消息会被
//     立即拒绝并返回明确原因——不允许调用方在一个永远不会读消息的邮箱上等待
//     （见 ErrUnreachable）。
//  2. wait 只能等一次。同一轮里第二次 wait 立即返回 already_waited，不做忙等
//     轮询，避免模型把 wait 写成 while 循环烧 token（见 WaitOutcome）。
//  3. 一次终局只通知一次。完成报告与崩溃通知携带同一份载荷，谁先占坑谁发，
//     另一个丢弃（见 claimNotice）。
//
// ── 落盘边界 ─────────────────────────────────────────────────────────────────
//
// 协调器自身**不碰数据库**：mailbox、状态机、等待器全在内存。数据库仍然是唯一
// 的事实来源，但只承载「任务/资产/探索图/发现」这些业务实体，不再承载 agent 之间
// 的对话与控制流。进程重启时由上层用 Coordinator.Snapshot() 落一份 JSON，
// 恢复时 Rehydrate 回内存——控制流不跨进程存活是刻意的取舍：重启后该由人决定
// 是否继续，而不是让一个半死的 agent 图自己接着跑。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// AgentState 是调度层承认的全部状态。终态只有四种：没有第五种「悄悄结束」。
type AgentState string

const (
	StateRunning      AgentState = "running"       // 正在跑一轮 turn
	StateWaiting      AgentState = "waiting"       // 停在 wait_* 上
	StateCompleted    AgentState = "completed"     // 正常交卷
	StateStopped      AgentState = "stopped"       // 被上层主动终止
	StateCrashed      AgentState = "crashed"       // 异常退出，已通知父级
	StateFailed       AgentState = "failed"        // 明确失败（超过恢复上限等）
	StateBudgetPaused AgentState = "budget_paused" // 预算耗尽，可恢复
)

// IsTerminal 报告该状态是否已经终结，不再接受新的工作。
func (s AgentState) IsTerminal() bool {
	switch s {
	case StateCompleted, StateStopped, StateCrashed, StateFailed:
		return true
	}
	return false
}

// Active 报告该状态是否还占着一个执行位。
func (s AgentState) Active() bool {
	switch s {
	case StateRunning, StateWaiting, StateBudgetPaused:
		return true
	}
	return false
}

// MsgKind 是消息的三种语义，刻意只留三种：
//
//	query       提问——回答完对方继续干自己的事
//	instruction 改令——覆盖对方当前方向，优先级高于其自己的计划
//	information 通报——只需要知道，不需要回复，也不该打断手上的动作
type MsgKind string

const (
	MsgQuery       MsgKind = "query"
	MsgInstruction MsgKind = "instruction"
	MsgInformation MsgKind = "information"
)

func (k MsgKind) valid() bool {
	return k == MsgQuery || k == MsgInstruction || k == MsgInformation
}

// Interrupt 报告该消息是否应当打断接收方正在进行的 LLM 流。
// 只有 instruction 有这个待遇：通报打断一次正在生成的回答是纯浪费。
func (k MsgKind) Interrupt() bool { return k == MsgInstruction }

// WaitOutcome 是 wait_for_agents 的结果码。非空即代表"这一轮不要再来等了"。
type WaitOutcome string

const (
	WaitOK            WaitOutcome = ""                // 正常收尾，可以继续推理
	WaitAlreadyWaited WaitOutcome = "already_waited"  // 本轮已经等过一次
	WaitNoTargets     WaitOutcome = "no_live_targets" // 目标全都死了，别等
	WaitCancelled     WaitOutcome = "cancelled"       // 任务被暂停/删除
	WaitTimeoutFx     WaitOutcome = "timeout"         // 等待窗口用尽，但目标还活着
)

// Message 是 agent 之间唯一的通信载荷。它永远以一条「用户可见文本」的形式
// 追加到接收方的会话里——不是事件、不是回调、不是表行。
type Message struct {
	ID       int64     `json:"id"`
	From     string    `json:"from"`
	To       string    `json:"to"`
	Kind     MsgKind   `json:"kind"`
	Priority string    `json:"priority,omitempty"` // low|normal|high|urgent
	Body     string    `json:"body"`
	RefID    int64     `json:"ref_id,omitempty"` // 关联意图/任务号，仅用于日志与 UI 归因
	SentAt   time.Time `json:"sent_at"`
}

// Brief 是子 agent 交卷时的结构化产出。它不是散文：报告存在即被下游当作结论，
// 所以「确认 / 排除 / 未闭环」三态必须显式给，缺失信息不等于安全。
type Brief struct {
	State       AgentState  `json:"state"` // completed | failed | crashed | stopped
	Outcome     string      `json:"outcome"`
	Summary     string      `json:"summary"`
	Evidence    []string    `json:"evidence,omitempty"`
	OpenItems   []string    `json:"open_items,omitempty"`   // 既不能确认也不能排除的东西
	CounterView string      `json:"counter_view,omitempty"` // 反证 pass 的结论
	Facts       []BriefFact `json:"facts,omitempty"`
	Usage       Usage       `json:"usage"`
	FinishedAt  time.Time   `json:"finished_at"`
}

// BriefFact 是一条可入库的事实（对应旧的 fact 概念，但由提交者自己写，
// 不再需要 Reviewer 从原始输出里重构）。
type BriefFact struct {
	Summary  string  `json:"summary"`
	Detail   string  `json:"detail,omitempty"`
	AssetIDs []int64 `json:"asset_ids,omitempty"`
	IntentID int64   `json:"intent_id,omitempty"`
}

// Usage 是子 agent 自报的消耗，供父 agent 判断「值不值」。
type Usage struct {
	ToolCalls int   `json:"tool_calls"`
	InputTok  int   `json:"input_tokens"`
	OutputTok int   `json:"output_tokens"`
	Turns     int   `json:"turns"`
	ElapsedMS int64 `json:"elapsed_ms"`
}

// Runtime 是一个被调度 agent 的运行态。它的所有字段都由协调器在锁内改写，
// 外部只能通过 AgentView 拿到快照。
type Runtime struct {
	ID       string
	ParentID string
	TaskID   string
	Root     bool
	State    AgentState
	Role     string   // 仅用于日志与前端标签，不参与任何调度判断
	Skills   []string // 该 agent 被分配的能力，用于覆盖度缺口计算
	Turn     int

	mailbox    []Message
	waitedTurn int   // 记录在哪一轮已经 wait 过（0 = 从未）
	noticeSent int32 // 终局通知是否已被占坑（原子，避免报告/崩溃双发）
	doneClosed bool  // done 通道是否已关闭（受 c.mu 保护，确保只 close 一次）

	// interrupted / resumedBelow 是「预算中断可恢复」的账本，取代了早先那个
	// 永远为 true 的 resumable 布尔位。
	//
	// 为什么不能用一个布尔位（旧实现的缺陷）：Register 把 resumable 写死成 true，
	// 于是 `State.IsTerminal() && !resumable` 恒为假 —— 投递守卫形同虚设，
	// 往一个已经 crashed/failed 的 agent 发消息照样成功，消息进了一个永远没人读
	// 的邮箱。这正是本文件头第 1 条硬约束要禁的事。
	//
	// 现在把「可恢复」限定成两种来源，且都必须显式记账：
	//   - interrupted：本 agent 是被预算/暂停打断的（runner 交回 Brief 时声明），
	//     它**曾经活着**，上层把额度调大后可以重跑；重跑那一刻由 ResumeBelow 记账；
	//   - resumedBelow：注册期指定的恢复序号（MapID），只可能由上层显式给出。
	// completed / stopped / crashed / failed 四条路径都不写这两个字段，因此它们的
	// 邮箱从此永久拒收。
	interrupted  bool
	resumedBelow int64

	wake    chan struct{}
	done    chan struct{}
	cancel  context.CancelFunc
	started time.Time
	lastAct time.Time

	brief   *Brief
	usage   Usage
	lastErr string
}

// AgentView 是 Runtime 的只读快照，可以安全地跨 goroutine 持有与序列化。
type AgentView struct {
	ID         string     `json:"id"`
	ParentID   string     `json:"parent_id,omitempty"`
	TaskID     string     `json:"task_id,omitempty"`
	Root       bool       `json:"root"`
	State      AgentState `json:"state"`
	Role       string     `json:"role,omitempty"`
	Skills     []string   `json:"skills,omitempty"`
	Turn       int        `json:"turn"`
	Pending    int        `json:"pending_messages"`
	Mailbox    int        `json:"mailbox_total"`
	StartedAt  time.Time  `json:"started_at"`
	LastActive time.Time  `json:"last_active"`
	Brief      *Brief     `json:"brief,omitempty"`
	Usage      Usage      `json:"usage"`
	LastError  string     `json:"last_error,omitempty"`
}

// Coordinator 是进程内唯一的调度中枢。所有方法都可从任意 goroutine 调用。
type Coordinator struct {
	mu      sync.Mutex
	agents  map[string]*Runtime
	seq     int64 // agent id 序号
	msgSeq  int64
	closed  bool
	TaskID  string
	started time.Time

	// onChange 是可选的观察钩子（活动流 / SSE）。刻意做成"通知事实"而不是
	// "回调决策"：订阅者只能看，不能改变调度结果，避免重新长出第二套控制面。
	onChange func(Event)
}

// Event 是调度层对外广播的一次状态变化。
type Event struct {
	At       time.Time  `json:"at"`
	Kind     string     `json:"kind"` // agent.spawn|agent.state|agent.message|agent.report|task.done
	AgentID  string     `json:"agent_id,omitempty"`
	ParentID string     `json:"parent_id,omitempty"`
	State    AgentState `json:"state,omitempty"`
	Summary  string     `json:"summary,omitempty"`
	Detail   string     `json:"detail,omitempty"`
}

// ErrUnreachable 表示消息无法投递：接收方已经终结且不可恢复。
// 调用方拿到它应当停止等待，而不是换一种方式重试。
var ErrUnreachable = errors.New("agent unreachable")

// ErrNoCoordinator 表示当前上下文里没有挂协调器（例如单轮旁路工具调用）。
var ErrNoCoordinator = errors.New("no coordinator in context")

// ---------- 构造与生命周期 ----------

// NewCoordinator 建一个空的中枢。taskID 只用于日志与快照命名。
func NewCoordinator(taskID string) *Coordinator {
	return &Coordinator{
		agents:  make(map[string]*Runtime),
		TaskID:  taskID,
		started: time.Now(),
	}
}

// OnChange 安装只读观察钩子。多次调用覆盖前一个。
func (c *Coordinator) OnChange(fn func(Event)) {
	c.mu.Lock()
	c.onChange = fn
	c.mu.Unlock()
}

func (c *Coordinator) emit(ev Event) {
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	c.mu.Lock()
	fn := c.onChange
	c.mu.Unlock()
	if fn == nil {
		return
	}
	// 观察者不得影响调度：它在锁外执行，且 panic 被吞掉。
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[coordinator] onChange panic: %v", r)
		}
	}()
	fn(ev)
}

// Root 返回根 agent 的 id（空表示还没建立）。
func (c *Coordinator) Root() *AgentView {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, rt := range c.agents {
		if rt.Root {
			v := rt.view()
			return &v
		}
	}
	return nil
}

// ---------- 建立 agent ----------

// Register 把一个已经准备好运行的 agent 挂进图。root=true 的 agent 是派发者。
// 同一时刻只允许一个 root：第二个会被拒绝——「谁持有 spawn 权限」必须有唯一答案。
func (c *Coordinator) Register(id, parentID, role string, root bool, skills []string) (*AgentView, error) {
	c.mu.Lock()
	if c.closed {
		return nil, errors.New("coordinator closed")
	}
	if _, dup := c.agents[id]; dup {
		c.mu.Unlock()
		return nil, fmt.Errorf("agent %q already registered", id)
	}
	if root {
		for _, rt := range c.agents {
			if rt.Root && rt.State.Active() {
				c.mu.Unlock()
				return nil, fmt.Errorf("root agent %q already active; only one dispatcher is allowed", rt.ID)
			}
		}
	}
	if parentID != "" {
		p, ok := c.agents[parentID]
		if !ok {
			c.mu.Unlock()
			return nil, fmt.Errorf("parent %q not registered", parentID)
		}
		if p.State.IsTerminal() {
			c.mu.Unlock()
			return nil, fmt.Errorf("%w: parent %q is %s", ErrUnreachable, parentID, p.State)
		}
	}
	rt := &Runtime{
		ID:       id,
		ParentID: parentID,
		TaskID:   c.TaskID,
		Root:     root,
		State:    StateRunning,
		Role:     role,
		Skills:   append([]string(nil), skills...),
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
		started:  time.Now(),
		lastAct:  time.Now(),
	}
	c.agents[id] = rt
	c.seq++
	v := rt.view()
	c.mu.Unlock()
	// 锁外广播：观察者回调里可能再次取锁，锁内广播会自锁。
	c.emit(Event{Kind: "agent.spawn", AgentID: id, ParentID: parentID, State: StateRunning, Summary: role})
	return &v, nil
}

// SetCancel 绑定该 agent 的取消函数，使 Stop 能真正打断在跑的 turn。
func (c *Coordinator) SetCancel(id string, cancel context.CancelFunc) {
	c.mu.Lock()
	if rt, ok := c.agents[id]; ok {
		rt.cancel = cancel
	}
	c.mu.Unlock()
}

// ---------- 消息 ----------

// Send 投递一条消息。它做三件事：写 mailbox、唤醒等待者、必要时打断接收方。
//
// 关键规则：接收方已终结且不可恢复 → 立即返回 ErrUnreachable。这条规则消灭了
// 「往一个永远不会读的邮箱里发消息然后等它」这一类死等，它是旧架构里最贵的故障。
func (c *Coordinator) Send(from, to string, kind MsgKind, body string) (Message, error) {
	if !kind.valid() {
		kind = MsgInformation
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return Message{}, errors.New("coordinator closed")
	}
	rt, ok := c.agents[to]
	if !ok {
		c.mu.Unlock()
		return Message{}, fmt.Errorf("%w: unknown agent %q", ErrUnreachable, to)
	}
	if rt.State.IsTerminal() && !rt.resumableLocked() {
		st := rt.State
		c.mu.Unlock()
		return Message{}, fmt.Errorf("%w: %q is %s and will never read this message", ErrUnreachable, to, st)
	}
	c.msgSeq++
	msg := Message{
		ID:     c.msgSeq,
		From:   from,
		To:     to,
		Kind:   kind,
		Body:   strings.TrimSpace(body),
		SentAt: time.Now(),
	}
	rt.mailbox = append(rt.mailbox, msg)
	c.mu.Unlock()

	select {
	case rt.wake <- struct{}{}:
	default: // 已有一枚唤醒信号在途，无需重复
	}
	if kind.Interrupt() && rt.cancel != nil {
		// 只在下一次 turn 边界前有效；真正语义化的打断由 runner 决定是否接受。
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[coordinator] interrupt panic: %v", r)
			}
		}()
		rt.cancel()
	}
	c.emit(Event{Kind: "agent.message", AgentID: to, ParentID: from, State: rt.State,
		Summary: string(kind), Detail: preview(msg.Body, 160)})
	return msg, nil
}

// Consume 取走某 agent 的全部待处理消息。runner 在每次 turn 之前调用它，
// 把消息转成会话里的一条 user item。
func (c *Coordinator) Consume(id string) []Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	rt, ok := c.agents[id]
	if !ok || len(rt.mailbox) == 0 {
		return nil
	}
	out := rt.mailbox
	rt.mailbox = nil
	return out
}

// Peek 返回待处理条数，不取走。
func (c *Coordinator) Peek(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rt, ok := c.agents[id]; ok {
		return len(rt.mailbox)
	}
	return 0
}

// ---------- 状态迁移 ----------

// resumableLocked 报告这个终态 agent 是否还可能被上层重新拉起来干活。
//
// 调用方必须持有 c.mu。判定规则（与 Runtime 里那段字段注释互为表里）：
//   - 非终态：永远可投递（它就是活的）；
//   - stopped / crashed：永不恢复。停止是"上层让它别再干了"，崩溃是"它已经坏了"，
//     这两种情况下把指令塞进邮箱只会制造"以为送达了"的错觉；
//   - completed：只有**被打断而非自己交卷**的那种才可能重跑。runner 在预算耗尽时
//     会把 State 写成 completed 但 Outcome 记 open_proof_gap，那不算真的交卷；
//     这里用 interrupted 标记区分，没有标记的一律视为真的做完了；
//   - failed / 其它：同 crashed。
func (rt *Runtime) resumableLocked() bool {
	if !rt.State.IsTerminal() {
		return true
	}
	return rt.interrupted || rt.resumedBelow > 0
}

// MarkInterrupted 由 runner 在「被预算/暂停打断、但没死」的交卷路径上调用。
// 它只登记"这个终态是可恢复的"，不改状态——状态仍由 Finish/Crash 决定。
//
// 为什么不把这件事写进 Brief：Brief 是给父级读的结论文本，它的字段会进入
// 父级上下文；"我被打断了"是调度层的账，不该出现在模型读到的报告里。
func (c *Coordinator) MarkInterrupted(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rt, ok := c.agents[id]; ok {
		rt.interrupted = true
	}
}

// SetState 是该 agent 唯一的合法状态迁移入口。终态不可逆（除显式登记的恢复）。
func (c *Coordinator) SetState(id string, st AgentState, note string) {
	c.mu.Lock()
	rt, ok := c.agents[id]
	if !ok {
		c.mu.Unlock()
		return
	}
	prev := rt.State
	if prev.IsTerminal() {
		// 终态不可改写：避免"崩溃后又被标成完成"。通道与通知由首个终态迁移负责，
		// 所以这里连 close 都不重做——直接返回即可。
		c.mu.Unlock()
		return
	}
	rt.State = st
	rt.lastAct = time.Now()
	if note != "" && !st.IsTerminal() {
		rt.lastErr = note
	}
	parent := rt.ParentID
	c.mu.Unlock()

	c.emit(Event{Kind: "agent.state", AgentID: id, ParentID: parent, State: st, Summary: note})
	if st.IsTerminal() {
		// 关闭 done 通道是幂等的：close 两次会 panic，所以只在这里做一次判断。
		c.closeDone(id)
	}
}

// closeDone 关闭 done 通道，通知所有 WaitDone 的观察者。必须恰好一次：
// close 已关闭的 channel 会 panic，而终态迁移在崩溃/取消/正常交卷三条路径上
// 都可能发生，所以用受锁保护的布尔位去重。
func (c *Coordinator) closeDone(id string) {
	c.mu.Lock()
	rt, ok := c.agents[id]
	shouldClose := ok && !rt.doneClosed && rt.done != nil
	if shouldClose {
		rt.doneClosed = true
	}
	var ch chan struct{}
	if shouldClose {
		ch = rt.done
	}
	c.mu.Unlock()
	if shouldClose {
		close(ch)
	}
}

// Done 返回该 agent 的终结通道，供外部选择器等待。
func (c *Coordinator) Done(id string) <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rt, ok := c.agents[id]; ok {
		return rt.done
	}
	return nil
}

// claimNotice 保证一次终局只通知一次。返回 true 表示本次调用赢得了通知权。
func (c *Coordinator) claimNotice(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	rt, ok := c.agents[id]
	if !ok {
		return false
	}
	return atomic.CompareAndSwapInt32(&rt.noticeSent, 0, 1)
}

// ---------- 交卷 ----------

// Finish 记录一次终局产出，并把它作为一条 message 投递给父 agent。
//
// 这里是重构的核心替换点：旧架构里 Worker 交卷要写交接单表 → 触发 Reviewer →
// 判决落表 → 情报闸门放行，父级才能看到结论。现在交卷直接产出一条消息。
// 代价是父级看到的是子 agent 的自述而非被独立核验的结论——因此 Brief 强制
// 要求 outcome 落在封闭枚举里，并且 open_items 与 counter_view 不能为空串
// （见 Validate），把"没有核实"这件事写成显式字段，而不是靠一个中间角色兜底。
func (c *Coordinator) Finish(id string, b Brief) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if b.FinishedAt.IsZero() {
		b.FinishedAt = time.Now()
	}
	c.mu.Lock()
	rt, ok := c.agents[id]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("unknown agent %q", id)
	}
	parent := rt.ParentID
	c.mu.Unlock()

	// 先抢通知权：崩溃路径同样会抢。谁先抢到谁负责外发，另一方只落状态。
	//
	// **落 brief 也必须在抢到之后**：先写后抢会让后到的重复交卷覆盖掉已经
	// 发给父级的那份报告——邮箱里是 A、agents_graph 里是 B，排查时互相矛盾。
	won := c.claimNotice(id)
	c.SetState(id, b.State, b.Outcome)
	if won {
		c.mu.Lock()
		if cur, ok := c.agents[id]; ok {
			cur.brief = &b
			cur.usage = b.Usage
		}
		c.mu.Unlock()
	}

	if !won {
		// 另一条终局路径已经先占了通知权（崩溃通知，或重复交卷）。本次报告
		// 只落一份"后到"的痕迹，绝不覆盖已经外发的那份——否则父级邮箱里是 A，
		// agents_graph 里显示 B，排查时两条信息互相矛盾。
		c.mu.Lock()
		if cur, ok := c.agents[id]; ok && cur.brief != nil {
			cur.lastErr = "后到的终局报告（未外发）: " + b.Outcome
		}
		c.mu.Unlock()
		return nil
	}
	if parent != "" && b.State != StateStopped {
		body := b.Render()
		if _, err := c.Send(id, parent, MsgInformation, body); err != nil {
			log.Printf("[coordinator] agent %s 的报告未能送达父级 %s: %v", id, parent, err)
		}
	}
	c.emit(Event{Kind: "agent.report", AgentID: id, ParentID: parent, State: b.State,
		Summary: b.Outcome, Detail: preview(b.Summary, 240)})
	return nil
}

// Crash 在异常 unwind 之前通知父级：死掉的子 agent 仍然欠父级一份报告，
// 否则父级会一直等一个永远不会来的消息。
func (c *Coordinator) Crash(id string, err error, partial *Brief) {
	msg := "子 agent 异常退出"
	if err != nil {
		msg += "：" + err.Error()
	}
	c.mu.Lock()
	rt, ok := c.agents[id]
	if ok {
		rt.lastErr = msg
	}
	parent := ""
	if ok {
		parent = rt.ParentID
	}
	c.mu.Unlock()
	won := c.claimNotice(id)
	c.SetState(id, StateCrashed, msg)
	if !won {
		return
	}
	if parent != "" {
		b := Brief{State: StateCrashed, Outcome: "crashed", Summary: msg, FinishedAt: time.Now()}
		if partial != nil {
			b.Evidence = partial.Evidence
			b.OpenItems = partial.OpenItems
			b.Facts = partial.Facts
			b.Usage = partial.Usage
		}
		if _, err := c.Send(id, parent, MsgInformation, b.Render()); err != nil {
			log.Printf("[coordinator] 崩溃通知未能送达 %s: %v", parent, err)
		}
	}
}

// ---------- 等待 ----------

// WaitFor 阻塞到「任一目标有消息 / 全部目标终结 / ctx 结束 / 窗口用尽」。
//
// 单次语义由调用方（工具层）保证：同一 turn 只调一次。这里额外校验 turn，
// 因为模型完全可能在同一轮里连发两个 wait —— 那会把一次等待变成一次忙等。
func (c *Coordinator) WaitFor(ctx context.Context, from string, targets []string, window time.Duration) (WaitOutcome, []Message) {
	c.mu.Lock()
	me, ok := c.agents[from]
	if !ok {
		c.mu.Unlock()
		return WaitNoTargets, nil
	}
	turn := me.Turn
	if me.waitedTurn == turn {
		// 同一轮第二次 wait：直接拒绝。这一条比任何提示词劝导都可靠——
		// 模型把 wait 写成 while 循环时，第一次迭代之后就拿不到等待权了。
		c.mu.Unlock()
		return WaitAlreadyWaited, nil
	}
	me.waitedTurn = turn
	wake := me.wake
	c.mu.Unlock()

	// ── 收哪里的信：**自己的邮箱** ───────────────────────────────────────────
	//
	// 这里曾经取的是 targets 的邮箱，那是个方向性错误：agent 之间所有载荷
	// （交卷报告、崩溃通知、send_to_agent 的消息）都由 Send 落到**接收方**的邮箱，
	// 也就是等在这里的父级自己。去读被等的子 agent 的邮箱，等于去看它收到了什么，
	// 而不是看它交了卷——于是 wait_for_agents 的 reports 永远为空，父级只能靠
	// 下一轮上下文注入被动收到报告，"wait 完先读 reports"这条工具说明是假的。
	//
	// 只取来自 targets 的那些：父级自己的邮箱里也可能有人类/系统的指令，
	// 那是本轮的输入，不该被当成"子 agent 的报告"交回。
	c.mu.Lock()
	outcome, out := c.collectReportsLocked(me, targets)
	c.mu.Unlock()
	if len(out) > 0 {
		return outcome, out
	}
	if outcome != WaitOK {
		return outcome, nil
	}

	c.mu.Lock()
	if cur, still := c.agents[from]; still && cur.State == StateRunning {
		cur.State = StateWaiting
	}
	c.mu.Unlock()

	// 进入 waiting：只有从 running 进来才需要标，且退出时必须还原成 running
	// （wait 是「这一轮暂时没别的事」，不是终态，也不是 budget_paused）。
	c.emit(Event{Kind: "agent.state", AgentID: from, State: StateWaiting, Summary: "waiting for children"})
	defer func() {
		c.mu.Lock()
		if cur, still := c.agents[from]; still && cur.State == StateWaiting {
			cur.State = StateRunning
			cur.lastAct = time.Now()
		}
		c.mu.Unlock()
	}()

	if window <= 0 {
		window = 5 * time.Minute
	}
	timer := time.NewTimer(window)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return WaitCancelled, nil
	case <-timer.C:
		// 窗口用尽前最后再看一眼：唤醒信号可能与超时同时到达。
		c.mu.Lock()
		_, out := c.collectReportsLocked(c.agents[from], targets)
		c.mu.Unlock()
		if len(out) > 0 {
			return WaitOK, out
		}
		return WaitTimeoutFx, nil
	case <-wake:
	}

	// 醒来了。**必须在这里把报告取走并交回调用方**，原因有二：
	//   1. 唤醒是「图变了」的边沿信号，可能根本不是消息（子 agent 刚 spawn、
	//      状态变化、或与上一轮竞争）；醒来却什么都不返回，就等于白等一轮。
	//   2. 父级邮箱里的报告如果没有在这一刻交出去，下一次注入才有机会读到它，
	//      而模型此刻拿到的 wait 结果只能是一个空数组 —— 与工具说明不符。
	c.mu.Lock()
	defer c.mu.Unlock()
	_, out = c.collectReportsLocked(c.agents[from], targets)
	if len(out) == 0 {
		return WaitOK, nil // 边沿到了但没消息：交给上层下一轮判定
	}
	return WaitOK, out
}

// collectReportsLocked 从 me 的邮箱里取走「来自 targets」的消息并返回结果码。
//
// 调用方必须持有 c.mu。targets 为空表示"全部直接子 agent"。取走是破坏性的：
// 同一条报告不能被交回两次（那会让父级以为子 agent 交了两遍卷）。
func (c *Coordinator) collectReportsLocked(me *Runtime, targets []string) (WaitOutcome, []Message) {
	if me == nil {
		return WaitNoTargets, nil
	}
	want := make(map[string]bool, len(targets))
	for _, id := range targets {
		want[id] = true
	}
	match := func(m Message) bool {
		if len(want) > 0 {
			return want[m.From]
		}
		if child, ok := c.agents[m.From]; ok {
			return child.ParentID == me.ID
		}
		return false
	}
	out := make([]Message, 0, 4)
	kept := make([]Message, 0, len(me.mailbox))
	for _, m := range me.mailbox {
		if match(m) {
			out = append(out, m)
			continue
		}
		kept = append(kept, m)
	}
	me.mailbox = kept
	if len(out) > 0 {
		return WaitOK, out
	}
	// 没有报告可交：既没有活着、也没在排队的子 agent 才是「没得等」。
	if len(c.liveTargetsLocked(targets)) == 0 {
		return WaitNoTargets, nil
	}
	return WaitOK, nil
}

// liveTargetsLocked 返回仍在跑、或还有待读消息的目标。调用方必须持有 c.mu。
func (c *Coordinator) liveTargetsLocked(targets []string) []*Runtime {
	live := make([]*Runtime, 0, len(targets))
	for _, id := range targets {
		rt, ok := c.agents[id]
		if !ok {
			continue
		}
		if !rt.State.IsTerminal() || rt.Pending() > 0 {
			live = append(live, rt)
		}
	}
	return live
}

// MarkTurn 由 runner 在每一轮 LLM 调用开始时调用：推进轮次并重置 wait 配额。
// 轮次是所有"每轮一次"语义（wait、预算检查、压缩触发）的锚点。
func (c *Coordinator) MarkTurn(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	rt, ok := c.agents[id]
	if !ok {
		return 0
	}
	rt.Turn++
	rt.lastAct = time.Now()
	return rt.Turn
}

// ---------- 查询 ----------

// View 返回单个 agent 的快照。
func (c *Coordinator) View(id string) (*AgentView, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rt, ok := c.agents[id]
	if !ok {
		return nil, false
	}
	v := rt.view()
	return &v, true
}

// Children 返回某 agent 的直接子节点（按建立时间排序，输出稳定）。
func (c *Coordinator) Children(id string) []AgentView {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]AgentView, 0, 4)
	for _, rt := range c.agents {
		if rt.ParentID == id {
			out = append(out, rt.view())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// All 返回全部 agent 的快照，按建立顺序。
func (c *Coordinator) All() []AgentView {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]AgentView, 0, len(c.agents))
	for _, rt := range c.agents {
		out = append(out, rt.view())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// ActiveCount 是还在占执行位的 agent 数（含根）。
func (c *Coordinator) ActiveCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, rt := range c.agents {
		if rt.State.Active() {
			n++
		}
	}
	return n
}

// Idle 表示没有任何 agent 还在活跃——上层据此判定"整张图已经跑干"。
func (c *Coordinator) Idle() bool { return c.ActiveCount() == 0 }

// Stop 请求某个 agent 停下来（协作式）：先取消其 ctx，再由 runner 落 stopped。
func (c *Coordinator) Stop(id, reason string) error {
	c.mu.Lock()
	rt, ok := c.agents[id]
	if !ok {
		c.mu.Unlock()
		return fmt.Errorf("unknown agent %q", id)
	}
	cancel := rt.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	c.SetState(id, StateStopped, reason)
	return nil
}

// Close 关闭整个图：取消全部在跑的 agent，并拒绝后续注册。
func (c *Coordinator) Close(reason string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	cancels := make([]context.CancelFunc, 0, len(c.agents))
	for _, rt := range c.agents {
		if rt.cancel != nil {
			cancels = append(cancels, rt.cancel)
		}
	}
	c.mu.Unlock()
	for _, fn := range cancels {
		func() {
			defer func() { _ = recover() }()
			fn()
		}()
	}
	c.emit(Event{Kind: "task.done", Summary: reason})
}

// ---------- 快照 ----------

// Snapshot 把整张图导出成可落盘的 JSON。控制流不跨进程存活，但"谁在干什么、
// 留下什么结论"必须能被人看见，否则重启就等于失忆。
func (c *Coordinator) Snapshot() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	views := make([]AgentView, 0, len(c.agents))
	for _, rt := range c.agents {
		views = append(views, rt.view())
	}
	sort.Slice(views, func(i, j int) bool { return views[i].StartedAt.Before(views[j].StartedAt) })
	return json.MarshalIndent(map[string]any{
		"task_id":    c.TaskID,
		"started_at": c.started,
		"closed":     c.closed,
		"agents":     views,
	}, "", "  ")
}

// ---------- 内部 ----------

// Pending 返回待处理消息数（含终态 agent 上尚未被读走的）。
func (rt *Runtime) Pending() int { return len(rt.mailbox) }

func (rt *Runtime) view() AgentView {
	return AgentView{
		ID:         rt.ID,
		ParentID:   rt.ParentID,
		TaskID:     rt.TaskID,
		Root:       rt.Root,
		State:      rt.State,
		Role:       rt.Role,
		Skills:     append([]string(nil), rt.Skills...),
		Turn:       rt.Turn,
		Pending:    len(rt.mailbox),
		StartedAt:  rt.started,
		LastActive: rt.lastAct,
		Brief:      rt.brief,
		Usage:      rt.usage,
		LastError:  rt.lastErr,
	}
}

// Validate 强制交卷的封闭枚举与必填字段。校验失败直接拒绝写入，
// 这样"没核实"就不会伪装成"确认干净"。
func (b *Brief) Validate() error {
	switch b.State {
	case StateCompleted, StateFailed, StateCrashed, StateStopped:
	default:
		return fmt.Errorf("brief.state 必须是 completed/failed/crashed/stopped 之一，收到 %q", b.State)
	}
	if strings.TrimSpace(b.Summary) == "" {
		return errors.New("brief.summary 不能为空：没有结论的交卷等于把盲区留给父级")
	}
	if strings.TrimSpace(b.Outcome) == "" {
		return errors.New("brief.outcome 不能为空：必须落在 confirmed / ruled_out / open_proof_gap 之一")
	}
	return nil
}

// Render 把 Brief 渲染成交给父 agent 的纯文本。刻意不用 XML/JSON 包裹：
// 这段文字会被追加进父级会话，过度结构化只会让模型去解析而不是理解。
func (b *Brief) Render() string {
	var sb strings.Builder
	sb.WriteString("【子任务交卷】")
	sb.WriteString(string(b.State))
	sb.WriteString(" / ")
	sb.WriteString(b.Outcome)
	sb.WriteString("\n结论：")
	sb.WriteString(strings.TrimSpace(b.Summary))
	if b.CounterView != "" {
		sb.WriteString("\n反证核查：")
		sb.WriteString(strings.TrimSpace(b.CounterView))
	}
	if len(b.Evidence) > 0 {
		sb.WriteString("\n证据：")
		sb.WriteString(strings.Join(b.Evidence, "；"))
	}
	if len(b.Facts) > 0 {
		sb.WriteString("\n事实：")
		for i, f := range b.Facts {
			if i > 0 {
				sb.WriteString("；")
			}
			sb.WriteString(f.Summary)
		}
	}
	if len(b.OpenItems) > 0 {
		sb.WriteString("\n未闭环（既未确认也未排除）：")
		sb.WriteString(strings.Join(b.OpenItems, "；"))
	} else {
		// 空 open_items 是一个强断言：这条分支已经干净了。把它写出来，
		// 让父级必须面对这个断言，而不是默认它。
		sb.WriteString("\n未闭环：无（本条断言该分支已全部收口）")
	}
	sb.WriteString(fmt.Sprintf("\n消耗：工具 %d 次 / turn %d / in %d tok / out %d tok / %dms",
		b.Usage.ToolCalls, b.Usage.Turns, b.Usage.InputTok, b.Usage.OutputTok, b.Usage.ElapsedMS))
	return sb.String()
}

func preview(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

// ---------- context plumbing ----------

type coordKey struct{}

// WithCoordinator 把协调器挂进 context，供工具层取用。
func WithCoordinator(ctx context.Context, c *Coordinator) context.Context {
	return context.WithValue(ctx, coordKey{}, c)
}

// CoordinatorFrom 从 context 取协调器；没有则返回 ErrNoCoordinator。
func CoordinatorFrom(ctx context.Context) (*Coordinator, error) {
	c, ok := ctx.Value(coordKey{}).(*Coordinator)
	if !ok || c == nil {
		return nil, ErrNoCoordinator
	}
	return c, nil
}
