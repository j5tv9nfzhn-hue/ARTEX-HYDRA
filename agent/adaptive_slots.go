package agent

// 子代理并发槽位：把「同时能跑几个子 agent」从提示词里的一个数字，变成调度层
// 真正持有的一把闸门。
//
// 背景：RunnerConfig.MaxConcurrency 只是一个传给提示词的整数（见
// prompts_coord.go 的 ChildSlots），Coordinator.Register 从不检查在跑的 agent
// 数，于是模型派发多少就起多少——槽位信号是"说了但不生效"的。这个文件把上限
// 落到 Register 的准入路径上，并补齐另外三件模型看不见的事：
//
//  1. 排队：超额派发退化成"进队列"，而不是直接报错失败，避免模型把一次满载
//     误解成工具坏了、进而放弃派发；
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
	StallAfter = 10 * time.Minute
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
type SlotStatus struct {
	Limit   int      `json:"limit"`
	Used    int      `json:"used"`
	Free    int      `json:"free"`
	Queue   []string `json:"queue,omitempty"`
	Chain   string   `json:"chain"`
	Stalled []string `json:"stalled,omitempty"`
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

// Acquire 尝试为 id 占一个槽位。ok=false 表示已满载，id 被放进排队名单。
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
	if !s.queuedLocked(id) {
		s.queue = append(s.queue, id)
	}
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
func (s *SlotManager) QueueDepth() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

func (s *SlotManager) queuedLocked(id string) bool {
	for _, q := range s.queue {
		if q == id {
			return true
		}
	}
	return false
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
