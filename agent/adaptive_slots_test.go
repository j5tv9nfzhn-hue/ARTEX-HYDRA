package agent

import (
	"testing"
	"time"

	"github.com/Autumn-27/norma/permission"
)

func TestClampChildSlots(t *testing.T) {
	cases := []struct{ in, want int }{
		{-5, DefaultChildSlots},
		{0, DefaultChildSlots},
		{1, 1},
		{16, 16},
		{99, MaxChildSlots},
	}
	for _, c := range cases {
		if got := ClampChildSlots(c.in); got != c.want {
			t.Fatalf("ClampChildSlots(%d)=%d，期望 %d", c.in, got, c.want)
		}
	}
}

// TestSlotManagerAcquireReleasesWithoutQueueing 锁定槽位管理器的满载语义。
//
// 原名 ...AcquireQueueRelease，测的是"满载进队列、归还后仍在队列里、重试时才消费"。
// 那套行为在 2026-11-07 被判定为**假承诺**：Coordinator.Register 拿到 false 后
// 立刻 Release，队列从来没有消费者，UI 却一直显示"排队中"。现在的语义是：
//
//	满载 → Acquire 返回 false + Full=true，**不**入队；
//	归还 → 空出的名额由下一次全新的 Acquire 占用，不存在"提拔旧排队者"。
func TestSlotManagerAcquireReleasesWithoutQueueing(t *testing.T) {
	s := NewSlotManager(2)

	st, ok := s.Acquire("a")
	if !ok || st.Used != 1 || st.Free != 1 {
		t.Fatalf("第一个应占位成功：ok=%v status=%+v", ok, st)
	}
	if st.Full {
		t.Fatalf("还有空位时 Full 不应为 true：%+v", st)
	}

	// 同一个 id 重复申请是幂等的，不该吃掉第二个槽位。
	if _, ok := s.Acquire("a"); !ok {
		t.Fatalf("重复申请应幂等成功")
	}
	if got := s.Status().Used; got != 1 {
		t.Fatalf("重复申请后 used=%d，期望 1", got)
	}

	if _, ok := s.Acquire("b"); !ok {
		t.Fatalf("第二个应占位成功")
	}

	st, ok = s.Acquire("c")
	if ok {
		t.Fatalf("满载时不应占位成功")
	}
	if !st.Full {
		t.Fatalf("满载时 Full 必须为 true：%+v", st)
	}
	if len(st.Queue) != 0 || s.QueueDepth() != 0 {
		t.Fatalf("满载不应入队（队列无消费者），实得 %+v depth=%d", st.Queue, s.QueueDepth())
	}
	if st.Chain != "0" {
		t.Fatalf("满载时槽位曲线应为 0，实得 %q", st.Chain)
	}

	// 归还腾出容量：名额由新的 Acquire 占用，而不是"提拔"一个不存在的排队者。
	s.Release("a")
	if got := s.QueueDepth(); got != 0 {
		t.Fatalf("队列应恒为 0，实得 %d", got)
	}
	if _, ok := s.Acquire("c"); !ok {
		t.Fatalf("腾出槽位后 c 应能占位（重试是一次全新的派发）")
	}
	if got := s.Status().Used; got != 2 {
		t.Fatalf("c 占位后 used=%d，期望 2", got)
	}
}

func TestSlotChain(t *testing.T) {
	cases := map[int]string{0: "0", 1: "1->0", 3: "3->2->1->0"}
	for free, want := range cases {
		if got := slotChain(free); got != want {
			t.Fatalf("slotChain(%d)=%q，期望 %q", free, got, want)
		}
	}
}

func TestSlotManagerReclaimStalled(t *testing.T) {
	s := NewSlotManager(2)
	base := time.Now()
	s.Acquire("alive")
	s.Acquire("stuck")

	// 让 stuck 的活动时间停在阈值之外，alive 保持新鲜。
	s.mu.Lock()
	s.inUse["stuck"] = base.Add(-(StallAfter + time.Minute))
	s.inUse["alive"] = base
	s.mu.Unlock()

	stalled := s.Stalled(base)
	if len(stalled) != 1 || stalled[0] != "stuck" {
		t.Fatalf("应只认出 stuck，实得 %v", stalled)
	}

	reclaimed := s.ReclaimStalled(base)
	if len(reclaimed) != 1 || reclaimed[0] != "stuck" {
		t.Fatalf("应回收 stuck，实得 %v", reclaimed)
	}
	if got := s.Status().Used; got != 1 {
		t.Fatalf("回收后 used=%d，期望 1", got)
	}
	if _, held := s.inUse["alive"]; !held {
		t.Fatalf("活跃槽位不该被回收")
	}
}

func TestSlotManagerNilIsUnlimited(t *testing.T) {
	var s *SlotManager
	if _, ok := s.Acquire("x"); !ok {
		t.Fatalf("nil 槽位应视为不限并发")
	}
	s.Release("x")
	s.Touch("x")
	if got := s.Status().Chain; got != "unlimited" {
		t.Fatalf("nil 槽位状态应为 unlimited，实得 %q", got)
	}
}

// TestCoordinatorSlotsFullRejectsNotQueue 锁定"满载 = 拒绝，不排队"。
//
// 原名 TestCoordinatorSlotsFullReturnsQueued 与实现相反：Register 在收回
// Acquire 的 false 之后会立刻 Release，把 id 从队列里摘掉，运行期 Queue 恒空。
// 名字留着会继续误导后来的人（以及任何按名字找行为的搜索），所以一并改掉。
//
// 断言三件事：
//  1. 满载时返回 ErrSlotsFull（可被 IsSlotsFull 识别）；
//  2. 队列里**没有**残留——否则 UI 会显示一个永远不会启动的"排队中"；
//  3. SlotStatus.Full = true（这是 UI 唯一该看的满载信号）。
func TestCoordinatorSlotsFullRejectsNotQueue(t *testing.T) {
	c := NewCoordinator("t")
	c.SetSlots(NewSlotManager(1))
	if _, err := c.Register("root", "", "root", true, nil); err != nil {
		t.Fatalf("根注册失败: %v", err)
	}
	if _, err := c.Register("child1", "root", "worker", false, nil); err != nil {
		t.Fatalf("第一个子代理应注册成功: %v", err)
	}
	if _, err := c.Register("child2", "root", "worker", false, nil); err == nil {
		t.Fatalf("第二个子代理应因槽位已满被拒")
	} else if !IsSlotsFull(err) {
		t.Fatalf("错误应可被 IsSlotsFull 识别，实得 %v", err)
	}
	// 关键断言：被拒的 id 不能留在队列里（也绝不能出现在快照的 Queue 字段）。
	if n := c.slots.QueueDepth(); n != 0 {
		t.Fatalf("满载应直接拒绝，队列里不应残留 %d 个 id", n)
	}
	st := c.slots.Status()
	if len(st.Queue) != 0 {
		t.Fatalf("SlotStatus.Queue 应恒为空，实得 %v", st.Queue)
	}
	if !st.Full {
		t.Fatalf("满载时 SlotStatus.Full 必须为 true：UI 靠它显示水位，实得 %+v", st)
	}
	if st.Used != 1 || st.Limit != 1 || st.Free != 0 {
		t.Fatalf("水位快照不对：%+v", st)
	}
	if c.slots.QueueDepth() != 0 {
		t.Fatalf("QueueDepth 应恒为 0")
	}
}

func TestEnsureSlotsDoesNotResetOccupancy(t *testing.T) {
	c := NewCoordinator("t")
	c.EnsureSlots(1)
	if _, err := c.Register("root", "", "root", true, nil); err != nil {
		t.Fatalf("根注册失败: %v", err)
	}
	if _, err := c.Register("child1", "root", "worker", false, nil); err != nil {
		t.Fatalf("第一个子代理应注册成功: %v", err)
	}
	// 每个 agent 的 SessionRunner.Run 都会走到这里。如果它重建槽位表，
	// child1 的占用会被清空，child2 就能偷偷进来——上限被放大成 N×Max。
	c.EnsureSlots(4)
	if _, err := c.Register("child2", "root", "worker", false, nil); !IsSlotsFull(err) {
		t.Fatalf("EnsureSlots 不应重置已占用槽位，child2 仍应被拒，实得 %v", err)
	}
}

func TestPermissionTierParsing(t *testing.T) {
	for _, c := range []struct {
		in   string
		want PermissionTier
		ok   bool
	}{
		{"plan", PermissionTierPlan, true},
		{"只读", PermissionTierPlan, true},
		{"standard", PermissionTierDefault, true},
		{"bypass", PermissionTierBypass, true},
		{"full", PermissionTierBypass, true},
		{"nonsense", "", false},
	} {
		got, ok := ParsePermissionTier(c.in)
		if ok != c.ok || got != c.want {
			t.Fatalf("ParsePermissionTier(%q)=(%q,%v)，期望 (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
	if string(PermissionTierPlan.Mode()) != "plan" {
		t.Fatalf("plan 档位应映射到 norma ModePlan")
	}
	if !PermissionTierPlan.ReadOnly() || PermissionTierBypass.ReadOnly() {
		t.Fatalf("只有 plan 档位是只读")
	}
}

// TestDefaultPermissionTierIsBypass 钉住「默认 = 完全访问」这个产品决定。
//
// 同时守住它与实际执行路径的一致性：EffectivePermissionMode 必须返回
// norma 的 ModeBypass，而不是某个只在设置页好看的枚举值。
func TestDefaultPermissionTierIsBypass(t *testing.T) {
	t.Setenv("ARTEX_PERMISSION_TIER", "")
	if err := SetPermissionTier(string(PermissionTierBypass)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = SetPermissionTier(string(PermissionTierBypass)) })

	if got := CurrentPermissionTier(); got != PermissionTierBypass {
		t.Fatalf("默认档位应为 bypass，实得 %q", got)
	}
	if got := EffectivePermissionMode(); got != permission.ModeBypass {
		t.Fatalf("默认应映射到 ModeBypass，实得 %q", got)
	}

	// 显式收紧后必须立即在全量路径生效（对话页、任务链、导入共用一个入口）。
	if err := SetPermissionTier(string(PermissionTierPlan)); err != nil {
		t.Fatal(err)
	}
	if got := EffectivePermissionMode(); got != permission.ModePlan {
		t.Fatalf("plan 档位应映射到 ModePlan，实得 %q", got)
	}
}
