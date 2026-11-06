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

func TestSlotManagerAcquireQueueRelease(t *testing.T) {
	s := NewSlotManager(2)

	st, ok := s.Acquire("a")
	if !ok || st.Used != 1 || st.Free != 1 {
		t.Fatalf("第一个应占位成功：ok=%v status=%+v", ok, st)
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
	if len(st.Queue) != 1 || st.Queue[0] != "c" {
		t.Fatalf("满载时 c 应进队列，实得 %+v", st)
	}
	if st.Chain != "0" {
		t.Fatalf("满载时槽位曲线应为 0，实得 %q", st.Chain)
	}

	s.Release("a")
	// 归还只腾出容量，不自动"提拔"排队者：那次派发已经被上层拒了，
	// 自动占位等于给一个没人接手的 agent 发槽位（泄漏）。队列等到重试时才消费。
	if got := s.QueueDepth(); got != 1 {
		t.Fatalf("归还后队列应保留 c，实得 %d", got)
	}
	if _, ok := s.Acquire("c"); !ok {
		t.Fatalf("腾出槽位后 c 应能占位")
	}
	if got := s.QueueDepth(); got != 0 {
		t.Fatalf("c 占位成功后队列应清空，实得 %d", got)
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

	// 让 stuck 的活动时间停在 11 分钟前，alive 保持新鲜。
	s.mu.Lock()
	s.inUse["stuck"] = base.Add(-11 * time.Minute)
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

func TestCoordinatorSlotsFullReturnsQueued(t *testing.T) {
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
