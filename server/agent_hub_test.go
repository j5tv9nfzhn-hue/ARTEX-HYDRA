package server

import (
	"testing"

	"github.com/Autumn-27/artex/agent"
)

// TestAgentHubReplayAndResync 守住"两条流解耦 + 断线可续传"的服务端契约：
// 游标能补齐就补，补不齐必须显式告知 resync，而不是默默丢帧让界面停在旧状态。
func TestAgentHubReplayAndResync(t *testing.T) {
	h := newAgentHub()
	for i := 0; i < 3; i++ {
		h.publish(AgentEvent{Kind: "agent.state", AgentID: "a1", State: "running", Summary: "tick"})
	}

	replay, _, ok, cancel := h.subscribe(0)
	defer cancel()
	if !ok || len(replay) != 3 {
		t.Fatalf("since=0 应回放全部 3 条，实得 ok=%v n=%d", ok, len(replay))
	}
	if replay[0].Seq >= replay[2].Seq {
		t.Fatalf("seq 必须单调递增：%+v", replay)
	}

	replay, _, ok, cancel2 := h.subscribe(2)
	defer cancel2()
	if !ok || len(replay) != 1 || replay[0].Seq != 3 {
		t.Fatalf("since=2 应只回放 seq=3，实得 ok=%v %+v", ok, replay)
	}

	// 游标被环形缓冲淘汰：必须返回 ok=false，调用方据此先发全量 snapshot。
	for i := 0; i < agentHubRing+10; i++ {
		h.publish(AgentEvent{Kind: "agent.state", AgentID: "a1", State: "running"})
	}
	if _, _, ok, cancel3 := h.subscribe(1); ok {
		cancel3()
		t.Fatal("过期游标应返回 ok=false（触发 resync）")
	}
}

// TestAgentHubAttachSnapshotsCoordinator 验证只读投影真的接上了调度层：
// attach 之后注册的 agent 要能出现在 snapshot 里，且槽位状态可读。
func TestAgentHubAttachSnapshotsCoordinator(t *testing.T) {
	h := newAgentHub()
	coord := agent.NewCoordinator("task-1")
	coord.SetSlots(agent.NewSlotManager(2))
	h.attach(coord)

	if _, err := coord.Register("root", "", "root", true, nil); err != nil {
		t.Fatalf("根注册失败: %v", err)
	}
	if _, err := coord.Register("child", "root", "worker", false, nil); err != nil {
		t.Fatalf("子代理注册失败: %v", err)
	}

	snap := h.snapshot()
	if len(snap.Agents) != 2 {
		t.Fatalf("快照应有 2 个 agent，实得 %d", len(snap.Agents))
	}
	for _, a := range snap.Agents {
		if a.TaskID != "task-1" {
			t.Fatalf("agent %s 的 task_id 应为 task-1，实得 %q", a.ID, a.TaskID)
		}
	}
	slots, ok := snap.Slots["task-1"]
	if !ok {
		t.Fatalf("快照缺少 task-1 的槽位状态")
	}
	if slots.Limit != 2 || slots.Used != 1 {
		t.Fatalf("槽位应为 limit=2 used=1，实得 %+v", slots)
	}

	// 子代理注册会通过 OnChange 发出 agent.spawn，hub 必须已经收到。
	replay, _, _, cancel := h.subscribe(0)
	defer cancel()
	found := false
	for _, ev := range replay {
		if ev.Kind == "agent.spawn" && ev.AgentID == "child" && ev.ParentID == "root" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("未收到 child 的 agent.spawn 事件：%+v", replay)
	}
}

// TestFilterSnapshotByTask 守住"停靠栏只显示当前会话所属任务"：
// 过滤后既不能混入别的任务，也不能把槽位状态丢掉（否则排队提示会消失）。
func TestFilterSnapshotByTask(t *testing.T) {
	snap := AgentSnapshot{
		Seq: 7,
		Agents: []agent.AgentView{
			{ID: "a", TaskID: "t1"},
			{ID: "b", TaskID: "t2"},
			{ID: "c", TaskID: "t1"},
		},
		Slots: map[string]agent.SlotStatus{
			"t1": {Limit: 4, Used: 2, Free: 2, Chain: "2->1->0"},
			"t2": {Limit: 2, Used: 1, Free: 1, Chain: "1->0"},
		},
	}
	got := filterSnapshot(snap, "t1")
	if len(got.Agents) != 2 {
		t.Fatalf("t1 应只剩 2 个 agent，实得 %d", len(got.Agents))
	}
	for _, a := range got.Agents {
		if a.TaskID != "t1" {
			t.Fatalf("混入了别的任务：%+v", a)
		}
	}
	if len(got.Slots) != 1 || got.Slots["t1"].Chain != "2->1->0" {
		t.Fatalf("t1 的槽位快照应保留，实得 %+v", got.Slots)
	}
	if _, ok := got.Slots["t2"]; ok {
		t.Fatalf("t2 的槽位不该出现")
	}
	// 空过滤 = 不过滤（后端仍支持全量视图，便于排障）。
	if all := filterSnapshot(snap, ""); len(all.Agents) != 3 {
		t.Fatalf("空 task_id 应返回全量，实得 %d", len(all.Agents))
	}
}
