package agent

// 子代理并发槽位：把「同时能跑几个子 agent」从提示词里的一个数字，变成调度层
// 真正持有的一把闸门。
//
// 背景：RunnerConfig.MaxConcurrency 只是一个传给提示词的整数（见
// prompts_coord.go 的 ChildSlots），Coordinator.Register 从不检查在跑的 agent
// 数，于是模型派发多少就起多少——槽位信号是"说了但不生效"的。这个文件把上限
// 落到 Register 的准入路径上，并补齐另外三件模型看不见的事：
//
//  1. 满载语义 = **拒绝**，不是排队（2026-11-07 修正）：
//     本文件最初把超额派发设计成"进队列等空位"，但 Coordinator.Register 里
//     根本没有"出队再放行"的消费者——它拿到 Acquire 的 false 后立刻 Release，
//     把 id 从队列里摘掉，然后返回 ErrSlotsFull。也就是说运行期 Queue 恒空，
//     而 UI / 提示词 / 测试却都还在说"已排队"。
//     既然没有出队通道，"排队"就是假承诺：模型会以为子代理正在等，转身去做
//     别的事，最后等一个永远不会启动的 agent。现在统一为"满载即拒绝并如实
//     告知水位"，SlotStatus.Full 是唯一口径，Queue 字段保留但恒空。
//  2. 回收：超过 ttl 没有任何活动的槽位（lastAct 不前进）被视为僵死，
//     可被 reclaim 掉并把 agent 落 stopped，防止跑飞的子代理永久占位；
//  3. 可观测：Status 给出 used/limit/free、排队名单与槽位耗尽曲线（3->2->1->0），
//     wait_for_agents 把这些原样交给根，让"槽位满"成为可读信号而不是静默等待。

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	// MinChildSlots / MaxChildSlots 是并发上限的可配置区间。
	MinChildSlots = 1
	MaxChildSlots = 16
	// DefaultChildSlots 与 runner_coord.go 的 maxChildSlots 兜底值一致。
	DefaultChildSlots = 4
	// StallAfter 是「多久没活动算僵住」的默认阈值。
	//
	// 取 20 分钟而不是 10：Bash 单条命令的上限就是 10 分钟，一个正常的
	// nmap/sqlmap 长命令会让 turn 在 10 分钟内没有任何 MarkTurn；阈值贴着
	// 工具上限会把合法长任务误杀。20 分钟仍能在真正卡死时回收槽位。
	StallAfter = 20 * time.Minute
)

// ClampChildSlots 把任意输入夹到 [MinChildSlots, MaxChildSlots]；<=0 取默认值。
func ClampChildSlots(n int) int {
	if n <= 0 {
		return DefaultChildSlots
	}
	if n < MinChildSlots {
		return MinChildSlots
	}
	if n > MaxChildSlots {
		return MaxChildSlots
	}
	return n
}

// SlotStatus 是一次槽位快照。字段全部可序列化，直接进工具返回值。
//
// 【Queue 恒为空，保留字段是为了兼容旧客户端】见文件头「满载语义」那段：
// 当前策略是满载直接拒绝，`Register` 会把 id 从排队名单里摘掉再返回
// ErrSlotsFull，所以运行期不可能有"正在排队"的 agent。字段留着不删是因为
// web 端的 AgentLiveSlots 仍按它解析（多一个空数组不会破坏渲染），删掉反而
// 会让旧前端因为 undefined 而崩。
type SlotStatus struct {
	Limit int `json:"limit"`
	Used  int `json:"used"`
	Free  int `json:"free"`
	// Queue 永远为空，见类型注释。不要用它判断"是否有人排队"。
	Queue   []string `json:"queue,omitempty"`
	Chain   string   `json:"chain"`
	Stalled []string `json:"stalled,omitempty"`
	// Full 是 UI 该看的那个布尔：true = 满载、新的派发会被**拒绝**。
	// 上一版本 UI 用 Queue 非空来表达"满载"，于是永远显示"空闲"——
	// 因为 Queue 永远为空。这是"说法与代码不符"里最隐蔽的一处。
	Full bool `json:"full"`
}

// SlotManager 是并发槽位的唯一持有者。零值不可用，必须经 NewSlotManager 构造。
type SlotManager struct {
	mu    sync.Mutex
	limit int
	ttl   time.Duration
	inUse map[string]time.Time // agentID -> 最近一次活动时间
	queue []string             // 因满载被挡下的 agentID，按到达顺序
}

// NewSlotManager 构造一个带默认 ttl 的槽位管理器。
func NewSlotManager(limit int) *SlotManager {
	return &SlotManager{
		limit: ClampChildSlots(limit),
		ttl:   StallAfter,
		inUse: make(map[string]time.Time),
	}
}

// SetLimit 调整上限并夹到合法区间。已经超额的占用不会被驱逐。
func (s *SlotManager) SetLimit(n int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.limit = ClampChildSlots(n)
	s.mu.Unlock()
}

// Limit 返回当前上限。
func (s *SlotManager) Limit() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limit
}

// Acquire 尝试为 id 占一个槽位。
//
// ok=false 表示已满载。**满载不排队**：返回的 SlotStatus 里 Queue 恒空、
// Full=true。调用方（Coordinator.Register）会把这次派发当作"拒绝"处理，
// 让模型如实知道"没有派出去"，而不是以为在排队等空位。
//
// 同一个 id 重复申请是幂等的（只刷新活动时间），不会吃掉两个槽位。
func (s *SlotManager) Acquire(id string) (SlotStatus, bool) {
	if s == nil {
		return SlotStatus{Chain: "unlimited"}, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if _, held := s.inUse[id]; held {
		s.inUse[id] = now
		return s.statusLocked(now), true
	}
	if len(s.inUse) < s.limit {
		s.inUse[id] = now
		s.dequeueLocked(id)
		return s.statusLocked(now), true
	}
	// 满载：不把 id 留在队列里。
	//
	// 这里以前会 append 到 s.queue，但没有任何出队放行的消费者
	// （Coordinator.Register 拿到 false 后会立刻 Release，把它摘掉）。
	// 也就是说"排队"从未真的发生，只是在这一个函数里短暂存在过——
	// 而 Status.Queue 因此永远是空的。既然如此，就不要制造这个中间态：
	// 直接返回满载快照，让上层如实回一句"未派出"。
	return s.statusLocked(now), false
}

// Release 归还槽位；排队中的 id 也会从队列里摘掉。
func (s *SlotManager) Release(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inUse, id)
	s.dequeueLocked(id)
}

// Touch 刷新活动时间。活跃的 turn 必须调用它，否则会被当成僵死槽位回收。
func (s *SlotManager) Touch(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, held := s.inUse[id]; held {
		s.inUse[id] = time.Now()
	}
}

// Status 返回当前快照（含僵死名单）。
func (s *SlotManager) Status() SlotStatus {
	if s == nil {
		return SlotStatus{Chain: "unlimited"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked(time.Now())
}

// Stalled 列出占用槽位但超过 ttl 没有活动的 agent。
func (s *SlotManager) Stalled(now time.Time) []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stalledLocked(now)
}

// ReclaimStalled 回收僵死槽位并返回被回收的 agentID，交由上层落终态。
func (s *SlotManager) ReclaimStalled(now time.Time) []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	stalled := s.stalledLocked(now)
	for _, id := range stalled {
		delete(s.inUse, id)
		s.dequeueLocked(id)
	}
	return stalled
}

// QueueDepth 返回排队数量。
//
// 【恒为 0】满载即拒绝，没有等待队列。这个函数保留是因为 wait_for_agents 与
// 几个测试在读它；不要把它当成"有 N 个 agent 在等空位"——那种语义不存在。
func (s *SlotManager) QueueDepth() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

func (s *SlotManager) dequeueLocked(id string) {
	if len(s.queue) == 0 {
		return
	}
	out := s.queue[:0]
	for _, q := range s.queue {
		if q != id {
			out = append(out, q)
		}
	}
	s.queue = out
}

func (s *SlotManager) stalledLocked(now time.Time) []string {
	if s.ttl <= 0 {
		return nil
	}
	var out []string
	for id, at := range s.inUse {
		if now.Sub(at) >= s.ttl {
			out = append(out, id)
		}
	}
	if len(out) > 1 {
		sortStrings(out)
	}
	return out
}

func (s *SlotManager) statusLocked(now time.Time) SlotStatus {
	free := s.limit - len(s.inUse)
	if free < 0 {
		free = 0
	}
	st := SlotStatus{
		Limit:   s.limit,
		Used:    len(s.inUse),
		Free:    free,
		Chain:   slotChain(free),
		Stalled: s.stalledLocked(now),
		Full:    free == 0,
	}
	if len(s.queue) > 0 {
		st.Queue = append([]string(nil), s.queue...)
	}
	return st
}

// slotChain 把"还剩几个槽位"画成一条耗尽曲线：free=3 → 3->2->1->0。
// 根提示词与 wait_for_agents 都用它表达"还能开几条并行"。
func slotChain(free int) string {
	if free <= 0 {
		return "0"
	}
	var b strings.Builder
	for i := free; i >= 0; i-- {
		if i != free {
			b.WriteString("->")
		}
		fmt.Fprintf(&b, "%d", i)
	}
	return b.String()
}

func sortStrings(v []string) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}
