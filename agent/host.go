package agent

// 协调器与"真正跑 LLM 的那一层"之间的唯一接缝。
//
// 边界刻意划在这里：Coordinator 只懂状态、mailbox 和等待；它不知道 harness 怎么
// 起一轮 turn、也不知道工具面怎么装配。runner 负责把「一个 agent 该干什么」翻译成
// 一次真实的执行，并在结束时把产出交回 Coordinator。两者之间只有一个接口，
// 因此上层可以换执行器（真实 LLM / 旁路提问 / 测试替身）而不动调度逻辑。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// AgentSpec 描述"要起一个什么样的 agent"。它是 delegate_task 的全部入参，
// 也是唯一允许父级影响子级的通道——没有第二套配置下发机制。
type AgentSpec struct {
	ParentID string   `json:"parent_id,omitempty"`
	Name     string   `json:"name,omitempty"` // 人类可读标签，仅用于日志/UI
	Role     string   `json:"role,omitempty"`
	Task     string   `json:"task"`   // 必须自包含：子 agent 看不到父级的会话历史
	Skills   []string `json:"skills,omitempty"`
	AssetIDs []int64  `json:"asset_ids,omitempty"`
	IntentID int64    `json:"intent_id,omitempty"`
	MaxTurns int      `json:"max_turns,omitempty"`
	// Fresh 为 true 时子 agent 不继承父级会话（默认）。继承会把父级的上下文预算
	// 直接翻倍，而子 agent 需要的其实是"这一段任务"，不是"父级想过什么"。
	Fresh bool `json:"fresh,omitempty"`
	// Depth 是当前 agent 在图上的深度（根=0）。由 Spawner 自动递增，
	// 调用方不应自己填；它只用来做递归深度闸门。
	Depth int `json:"depth,omitempty"`
	// WaitForReport 为 true 时 delegate_task 阻塞到子 agent 交卷后一并返回报告。
	// 默认 false：异步派发，由父级自己决定何时 wait —— 这正是旧架构缺的那一点
	// 自主性（旧架构里"何时看结果"由 Reviewer 与闸门替模型决定）。
	WaitForReport bool `json:"wait_for_report,omitempty"`
}

// AgentResult 是执行器交回来的东西。它是 Brief 的执行侧对应物。
type AgentResult struct {
	State   AgentState
	Brief   Brief
	Err     error
	Usage   Usage
	Turns   int
	ToolUse int
}

// AgentRunner 起一个 agent 并跑到底。实现方负责：
//   - 装配工具面（含协调器工具）
//   - 把 ctx 里的 Coordinator 挂进执行上下文（WithCoordinator）
//   - 在每次 turn 开始时调用 coord.MarkTurn(id)
//   - 在每轮之前用 coord.Consume(id) 取走 mailbox 并渲染成会话输入
//   - 结束（任何原因）时调用 coord.Finish 或 coord.Crash
//
// 实现方**不得**自行决定状态迁移之外的调度语义：派发、等待、终止都归 Coordinator。
type AgentRunner interface {
	Run(ctx context.Context, spec AgentSpec, agentID string, coord *Coordinator) AgentResult
}

// Spawner 把 Coordinator + Runner 合成「派活」这一动作。它是工具层唯一的依赖。
type Spawner struct {
	Coord  *Coordinator
	Runner AgentRunner

	// 并发上限：0 表示不限。上限存在的原因是浏览器/容器/带宽是共享资源，
	// 而不是因为"角色太多"。超限时 delegate 直接返回可读错误，不排队——
	// 排队会让模型以为任务已经开始，然后等一个还没被领取的结果。
	MaxConcurrent int
	// MaxDepth 是递归深度上限（根=0）。0 时取 defaultMaxDepth。
	// 没有它，一个 agent 可以无限派生同类 agent，把并发额度和 token 预算一起吃光。
	MaxDepth int

	// Owner 是图的所有者 id（根的 id），用于日志。
	Owner string

	mu      sync.Mutex
	running int
	wg      sync.WaitGroup
	cancels map[string]context.CancelFunc
}

// NewSpawner 建一个派发器。需要 host 负责关闭（Close 会取消全部在跑的 agent）。
func NewSpawner(coord *Coordinator, runner AgentRunner) *Spawner {
	return &Spawner{Coord: coord, Runner: runner, cancels: make(map[string]context.CancelFunc)}
}

// ErrBusy 表示并发额度已满。
var ErrBusy = errors.New("agent slots exhausted")

// ErrTooDeep 表示递归深度超限。
var ErrTooDeep = errors.New("agent depth exceeded")

// defaultMaxDepth 是递归上限的默认值。取 3 与 norma/subagent 的默认一致：
// 根(0) → 子(1) → 孙(2) → 曾孙(3) 已经足够表达"分组执行"，
// 再深通常意味着任务本身没有被拆清楚。
const defaultMaxDepth = 3

// defaultMaxConcurrent 与 agent.maxChildSlots 同值。分两处定义是因为它们属于
// 不同包（server 与 agent），但数值必须一致——"提示词说要等、实际却卡住"是最难查的一类问题。
const defaultMaxConcurrent = 4

// Delegate 同步创建一个子 agent 并异步执行它。返回子 agent 的 id 与初始快照。
//
// 这里的语义是"派活"而不是"调用"：函数返回时子 agent 可能已经在跑，也可能还在
// 排队；父级拿到的只是一个 id。想让父级立刻拿到结论，用 WaitForReport=true。
func (s *Spawner) Delegate(parentCtx context.Context, spec AgentSpec) (*AgentView, error) {
	if s.Coord == nil || s.Runner == nil {
		return nil, errors.New("spawner not wired")
	}
	if strings.TrimSpace(spec.Task) == "" {
		return nil, errors.New("task 不能为空：子 agent 看不到父级会话，任务必须自包含")
	}
	// 深度闸门。放在 Register 之前：超深时连 agent 都不该被建出来。
	if spec.Depth >= s.maxDepth() {
		return nil, fmt.Errorf("%w: 深度 %d 已达上限 %d。把它做成同层的一个子任务，而不是继续往下派", ErrTooDeep, spec.Depth, s.maxDepth())
	}
	s.mu.Lock()
	if s.MaxConcurrent > 0 && s.running >= s.MaxConcurrent {
		n := s.running
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: 已有 %d 个 agent 在跑（上限 %d）。等现有 agent 交卷后再派，或先合并任务", ErrBusy, n, s.MaxConcurrent)
	}
	s.mu.Unlock()

	// parent 必须在 Register 之前解析：Register 用它做可达性检查，
	// 若留到 goroutine 里再解析，注册的父级与实际通知的父级会不一致。
	spec.ParentID = resolveParent(spec.ParentID, parentCtx)
	id := s.nextID(spec)
	view, err := s.Coord.Register(id, spec.ParentID, firstNonEmpty(spec.Role, spec.Name, "worker"), false, spec.Skills)
	if err != nil {
		return nil, err
	}
	spec.Depth++

	// 子 agent 的生命周期不挂在父级 ctx 上：父级被取消不代表子级该死。
	// 父级的取消由 Coordinator.Close / Stop 统一处理，避免"父级 turn 结束就顺手
	// 杀掉子级"这类隐蔽的资源泄漏。
	base := context.WithoutCancel(parentCtx)
	ctx, cancel := context.WithCancel(base)
	// 全图共用同一个 Spawner：并发额度、取消集合、深度计数因此对整张图生效，
	// 而不是每个 agent 各有一份（后者会让上限变成 N×4）。
	ctx = WithSpawner(ctx, s)
	ctx = WithCoordinator(ctx, s.Coord)
	ctx = WithAgentID(ctx, id)
	ctx = WithAgentDepth(ctx, spec.Depth)
	s.Coord.SetCancel(id, cancel)

	s.mu.Lock()
	s.cancels[id] = cancel
	s.running++
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.cancels, id)
			s.running--
			s.mu.Unlock()
			cancel()
		}()
		defer func() {
			// 执行器自己的 panic 也必须被转成一次崩溃交卷，否则父级会永远等下去。
			if r := recover(); r != nil {
				log.Printf("[spawner] agent %s panic: %v", id, r)
				s.Coord.Crash(id, fmt.Errorf("panic: %v", r), nil)
			}
		}()
		res := s.Runner.Run(ctx, spec, id, s.Coord)
		if res.Err != nil {
			s.Coord.Crash(id, res.Err, &res.Brief)
			return
		}
		if res.State == "" {
			res.State = StateCompleted
		}
		if res.Brief.State == "" {
			res.Brief.State = res.State
		}
		res.Brief.Usage = res.Usage
		if err := s.Coord.Finish(id, res.Brief); err != nil {
			// 交卷不合规（缺 outcome/summary）不是灾难：按失败收场并通知父级，
			// 让父级知道"这个分支没给出可用结论"，而不是静默丢弃。
			s.Coord.Crash(id, fmt.Errorf("交卷不合规: %w", err), &res.Brief)
		}
	}()
	return view, nil
}

// Wait 是工具层 wait_for_agents 的宿主侧实现：把 Coordinator 的等待转成
// 阻塞调用，并把结果整理成父级可读的消息列表。
func (s *Spawner) Wait(ctx context.Context, from string, targets []string, window time.Duration) (WaitOutcome, []Message) {
	if s.Coord == nil {
		return WaitNoTargets, nil
	}
	return s.Coord.WaitFor(ctx, from, targets, window)
}

// Stop 终止一个 agent 并等它真正停下。
func (s *Spawner) Stop(id, reason string) error {
	if s.Coord == nil {
		return errors.New("spawner not wired")
	}
	s.mu.Lock()
	cancel := s.cancels[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return s.Coord.Stop(id, reason)
}

// Close 取消全部在跑的 agent，并等待它们退出。
func (s *Spawner) Close(reason string) {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.cancels))
	for _, fn := range s.cancels {
		cancels = append(cancels, fn)
	}
	s.mu.Unlock()
	for _, fn := range cancels {
		fn()
	}
	if s.Coord != nil {
		s.Coord.Close(reason)
	}
	s.wg.Wait()
}

// MaxTurns 是 spec 的轮次上限；0 时沿用 runner 的默认值。
func (spec AgentSpec) EffectiveMaxTurns(def int) int {
	if spec.MaxTurns > 0 {
		return spec.MaxTurns
	}
	return def
}

func (s *Spawner) maxDepth() int {
	if s.MaxDepth > 0 {
		return s.MaxDepth
	}
	return defaultMaxDepth
}

// Running 返回当前在跑的 agent 数。
func (s *Spawner) Running() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *Spawner) nextID(spec AgentSpec) string {
	base := strings.TrimSpace(spec.Name)
	if base == "" {
		base = "worker"
	}
	base = sanitizeID(base)
	if spec.IntentID > 0 {
		return fmt.Sprintf("%s-%d", base, spec.IntentID)
	}
	// 无意图号的派发用序号保证唯一：同一父级可能并行派多个同角色 agent。
	for i := 1; ; i++ {
		id := fmt.Sprintf("%s-%d", base, i)
		if _, exists := s.Coord.View(id); !exists {
			return id
		}
	}
}

func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '/':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "worker"
	}
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}

// 注：本文件曾在此处声明 firstNonEmpty。agent 包已有一份完全等价的实现
// （tools_pentest.go），同名包级函数在同一个包里是重复定义，Go 会以
// "redeclared in this block" 拒绝编译。已删除本文件的副本。
//
// 这类冲突的隐蔽处在于：新增文件自己看是自洽的，冲突只在它与既有文件
// 相遇时才出现——这正是"把 11 个新文件装进一个已有 151 个文件的包"
// 这一步最容易漏掉的检查。
// resolveParent 允许调用方在 ctx 里携带"我自己的 id"（工具层无法直接传参时用），
// spec 里显式给了 parent 就优先用显式的。
func resolveParent(fromSpec string, ctx context.Context) string {
	if strings.TrimSpace(fromSpec) != "" {
		return fromSpec
	}
	return AgentIDFrom(ctx)
}

type agentDepthKey struct{}

// WithAgentDepth 记录当前 agent 在图上的深度（根=0）。
func WithAgentDepth(ctx context.Context, d int) context.Context {
	return context.WithValue(ctx, agentDepthKey{}, d)
}

// AgentDepthFrom 取当前 agent 的深度；缺省 0。
func AgentDepthFrom(ctx context.Context) int {
	d, _ := ctx.Value(agentDepthKey{}).(int)
	return d
}

type agentIDKey struct{}

// WithAgentID 把"当前正在执行的是谁"放进 ctx。工具层用它知道消息从谁发出。
func WithAgentID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, agentIDKey{}, id)
}

// AgentIDFrom 取当前 agent 的 id。
func AgentIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(agentIDKey{}).(string)
	return s
}

type spawnerKey struct{}

// WithSpawner 把派发器挂进 ctx，供工具层取用。
func WithSpawner(ctx context.Context, s *Spawner) context.Context {
	return context.WithValue(ctx, spawnerKey{}, s)
}

// SpawnerFrom 从 ctx 取派发器。
func SpawnerFrom(ctx context.Context) (*Spawner, error) {
	s, ok := ctx.Value(spawnerKey{}).(*Spawner)
	if !ok || s == nil || s.Coord == nil || s.Runner == nil {
		return nil, ErrNoCoordinator
	}
	return s, nil
}

// ---------- 任务级生命周期（取代 plannerLoop 的编排职责） ----------

// TaskRun 是一次任务级调度的句柄：它建立根 agent、等它跑完、把最终结论落成
// 一份 Brief。旧架构里这一段是 plannerLoop 里 200 行的 select + debounce + 心跳 +
// 空转跳过 + 终局收尾；现在它退化成"起根 agent、等它停、读它的交卷"。
type TaskRun struct {
	Coord   *Coordinator
	Spawner *Spawner
	RootID  string
	Spec    AgentSpec
}

// StartTask 建立根 agent。根是唯一持有 spawn 权限的角色，它的"规划"就是它的
// 下一步动作，因此这里不需要任何目标判定逻辑——目标判定是它自己的第一个 turn。
func StartTask(ctx context.Context, taskID string, runner AgentRunner, spec AgentSpec, maxConcurrent int) (*TaskRun, error) {
	coord := NewCoordinator(taskID)
	sp := NewSpawner(coord, runner)
	sp.MaxConcurrent = maxConcurrent
	// 显式写进 Spawner 而不是留 0：0 会被解读为"不限"，
	// 而 MaxConcurrent 的语义在 coordinator 与 spawner 两处必须一致，
	// 否则根提示词里写的"上限 4"与实际行为对不上。
	if sp.MaxConcurrent <= 0 {
		sp.MaxConcurrent = defaultMaxConcurrent
	}
	id := firstNonEmpty(spec.Name, "root")
	id = sanitizeID(id)
	if _, err := coord.Register(id, "", firstNonEmpty(spec.Role, "root"), true, spec.Skills); err != nil {
		sp.Close("register failed")
		return nil, err
	}
	tr := &TaskRun{Coord: coord, Spawner: sp, RootID: id, Spec: spec}
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	coord.SetCancel(id, cancel)

	// 根 agent 的执行上下文必须自带调度三元组：它是第一个 agent，
	// 没有「上游」可以继承这些值。
	ctx = WithSpawner(ctx, sp)
	ctx = WithCoordinator(ctx, coord)
	ctx = WithAgentID(ctx, id)
	ctx = WithAgentDepth(ctx, 0)

	sp.wg.Add(1)
	go func() {
		// defer 顺序是后进先出，因此这里按书写顺序**倒着**执行：
		// cancel → recover(兜底) → wg.Done。cancel 放在最内层意味着 panic
		// 已在此前被转成一次的 Crash，不会因 recover 先跑而把 goroutine 里的
		// panic 漏给进程。
		defer sp.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[task] root agent panic: %v", r)
				coord.Crash(id, fmt.Errorf("panic: %v", r), nil)
			}
		}()
		defer cancel()
		res := runner.Run(ctx, spec, id, coord)
		if res.Err != nil {
			coord.Crash(id, res.Err, &res.Brief)
			return
		}
		if res.State == "" {
			res.State = StateCompleted
		}
		if res.Brief.State == "" {
			res.Brief.State = res.State
		}
		res.Brief.Usage = res.Usage
		if err := coord.Finish(id, res.Brief); err != nil {
			coord.Crash(id, fmt.Errorf("交卷不合规: %w", err), &res.Brief)
		}
	}()
	return tr, nil
}

// AwaitRoot 等到根 agent 交卷，返回它的 Brief。
func (tr *TaskRun) AwaitRoot(ctx context.Context) (Brief, error) {
	if tr == nil || tr.Coord == nil {
		return Brief{}, errors.New("task run not started")
	}
	done := tr.Coord.Done(tr.RootID)
	if done == nil {
		return Brief{}, fmt.Errorf("root agent %q missing", tr.RootID)
	}
	select {
	case <-ctx.Done():
		return Brief{}, ctx.Err()
	case <-done:
	}
	if v, ok := tr.Coord.View(tr.RootID); ok && v.Brief != nil {
		return *v.Brief, nil
	}
	if v, ok := tr.Coord.View(tr.RootID); ok {
		return Brief{State: v.State, Outcome: string(v.State), Summary: firstNonEmpty(v.LastError, "根 agent 未提交报告")}, nil
	}
	return Brief{}, fmt.Errorf("root agent %q vanished", tr.RootID)
}

// Finish 关闭整张图（无论根是否正常交卷）。
func (tr *TaskRun) Finish(reason string) {
	if tr == nil || tr.Spawner == nil {
		return
	}
	tr.Spawner.Close(reason)
}
