package agent

// 协调器的三条硬约束（见 coordinator.go 文件头）在这里各有一条测试。
//
// 这个文件为什么必须存在：重构后旧的 planner/reviewer/worker 测试随角色一起
// 下线，新链路的调度语义一度只写在注释里。注释不会失败，也不会阻止下一个人
// 把 `!rt.resumable` 从 Send 里"顺手"改掉——那会让「给终态 agent 发消息必须
// 报错」这条约束静默失效，而没有任何一处会变红。
//
// 三条不变量全部是纯内存的：不起 LLM、不碰数据库、不需要联网，因此可以在任何
// 环境里跑（`go test ./agent/ -run TestCoordinator`）。
//
//	strix 的对应测试：tests/test_agent_graph_coordination.py
//	                    tests/test_wait_dedupe.py
//	对应关系见 promptcatalog/coord_switch 的对照表。

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// 不变量 1：消息只能投递给活着的 agent。
//
// 终态 agent 的邮箱不会再有人读。往里面塞消息有两个后果：调用方以为指令送达
// 了（而它永远不会被执行），以及父级在 wait 上等一个永远不会来的回复。
// 所以必须当场报 ErrUnreachable。
func TestCoordinatorUnreachableAfterTerminal(t *testing.T) {
	c := NewCoordinator("task-1")
	if _, err := c.Register("root", "", "root", true, nil); err != nil {
		t.Fatalf("register root: %v", err)
	}
	if _, err := c.Register("child", "root", "child", false, nil); err != nil {
		t.Fatalf("register child: %v", err)
	}
	if _, err := c.Send("root", "child", MsgInstruction, "动工"); err != nil {
		t.Fatalf("活着的子 agent 必须能收到消息: %v", err)
	}
	if err := c.Finish("child", Brief{State: StateCompleted, Outcome: "confirmed", Summary: "确认了一处注入"}); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if v, _ := c.View("child"); v == nil || v.State != StateCompleted {
		t.Fatalf("child 状态未落为 completed: %+v", v)
	}

	// 未知 agent 与终态 agent 都必须返回 ErrUnreachable（可被 errors.Is 识别，
	// 调用方据此决定"如实告知"而不是重试）。
	if _, err := c.Send("root", "child", MsgInstruction, "再去看一眼"); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("向终态 agent 发消息应返回 ErrUnreachable，实际 %v", err)
	}
	if _, err := c.Send("root", "nobody", MsgQuery, "在吗"); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("向不存在的 agent 发消息应返回 ErrUnreachable，实际 %v", err)
	}
	// 根自己还活着：投递给自己是合法的（自问自答的提醒）。
	if _, err := c.Send("root", "root", MsgInformation, "自我提醒"); err != nil {
		t.Fatalf("向活着的根发消息失败: %v", err)
	}
}

// 不变量 2：一轮只能等一次。
//
// 这条是拦「把 wait 写成 while 循环」的唯一可靠手段：提示词里的劝导不可靠，
// 而"第二次拿不到等待权"是确定行为。注意 wait_for_agents 工具会把每次成功
// 等待取回的消息一并返回，所以"等过"不等于"等到了消息"。
func TestCoordinatorWaitDedupedPerTurn(t *testing.T) {
	c := NewCoordinator("task-2")
	if _, err := c.Register("root", "", "root", true, nil); err != nil {
		t.Fatalf("register root: %v", err)
	}
	if _, err := c.Register("child", "root", "child", false, nil); err != nil {
		t.Fatalf("register child: %v", err)
	}
	c.MarkTurn("root")

	// 第一次：子还活着且暂时没有消息 → 等满窗口（timeout）。这已经算"等过"。
	first, _ := c.WaitFor(context.Background(), "root", []string{"child"}, 60*time.Millisecond)
	if first != WaitTimeoutFx {
		t.Fatalf("首次 wait 在无消息时应走 timeout，实际 %q", first)
	}

	// 同轮第二次：必须立即返回 already_waited，而不是再阻塞一个完整窗口。
	start := time.Now()
	second, msgs := c.WaitFor(context.Background(), "root", []string{"child"}, 5*time.Second)
	if second != WaitAlreadyWaited {
		t.Fatalf("同轮第二次 wait 应为 already_waited，实际 %q", second)
	}
	if len(msgs) != 0 {
		t.Fatalf("already_waited 不应带回消息: %+v", msgs)
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("第二次 wait 阻塞了 %s —— 它必须立即返回", el)
	}

	// 新的一轮重新获得等待权，并且邮箱里的消息要随等待结果交回调用方。
	c.MarkTurn("root")
	if _, err := c.Send("child", "root", MsgInformation, "子 agent 的结论"); err != nil {
		t.Fatalf("send: %v", err)
	}
	third, got := c.WaitFor(context.Background(), "root", []string{"child"}, 2*time.Second)
	if third != WaitOK {
		t.Fatalf("新一轮 wait 应为 OK，实际 %q", third)
	}
	if len(got) != 1 || got[0].Body != "子 agent 的结论" {
		t.Fatalf("wait 应把邮箱里的消息交回调用方，实际 %+v", got)
	}
}

// 不变量 2 的补充：邮箱里已经有消息时，wait 也必须把它们交回来。
//
// 这是一条**曾经真实存在的缺陷**：WaitFor 在入口处只要看到 pending 非空就
// `return WaitOK, nil` —— 结果是"消息已经到了、wait 却什么也不返回”，
// 子 agent 交卷的报告凭空消失，父级只能看到一个空等待。修复后这条测试守住它。
func TestCoordinatorWaitReturnsPendingImmediately(t *testing.T) {
	c := NewCoordinator("task-2b")
	if _, err := c.Register("root", "", "root", true, nil); err != nil {
		t.Fatalf("register root: %v", err)
	}
	// 子 agent 已经交卷：报告在等待发生之前就进了父级邮箱。
	if _, err := c.Register("child", "root", "child", false, nil); err != nil {
		t.Fatalf("register child: %v", err)
	}
	c.MarkTurn("root")
	if err := c.Finish("child", Brief{State: StateCompleted, Outcome: "ruled_out", Summary: "挡在 WAF 后面"}); err != nil {
		t.Fatalf("finish: %v", err)
	}

	outcome, msgs := c.WaitFor(context.Background(), "root", []string{"child"}, 2*time.Second)
	if outcome != WaitOK {
		t.Fatalf("有待读消息时 wait 应为 OK，实际 %q", outcome)
	}
	if len(msgs) != 1 {
		t.Fatalf("wait 必须把已到达的报告交回来，实际 %d 条：%+v", len(msgs), msgs)
	}
	if msgs[0].From != "child" {
		t.Fatalf("交回的消息来源不对: %+v", msgs[0])
	}
	// 交接是"取走"：第二次 wait 拿到空（且此时子已终结 → 没有活目标可等）。
	c.MarkTurn("root")
	outcome2, msgs2 := c.WaitFor(context.Background(), "root", []string{"child"}, 100*time.Millisecond)
	if outcome2 != WaitNoTargets {
		t.Fatalf("消息已被取走后应报 no_live_targets，实际 %q", outcome2)
	}
	if len(msgs2) != 0 {
		t.Fatalf("同一条报告被交回两次: %+v", msgs2)
	}
}

// 不变量 3：一次终局只通知一次。
//
// 完成报告与崩溃通知携带同一份载荷，谁先占坑谁外发，另一个只落状态。
// 否则父级会收到两条互相矛盾的终局（"做完了" + "崩了"）。
func TestCoordinatorSingleTerminalNotice(t *testing.T) {
	c := NewCoordinator("task-3")
	if _, err := c.Register("root", "", "root", true, nil); err != nil {
		t.Fatalf("register root: %v", err)
	}
	if _, err := c.Register("child", "root", "child", false, nil); err != nil {
		t.Fatalf("register child: %v", err)
	}
	// 正常交卷先到。
	if err := c.Finish("child", Brief{State: StateCompleted, Outcome: "ruled_out", Summary: "挡在 WAF 后面"}); err != nil {
		t.Fatalf("finish: %v", err)
	}
	// 随后执行器的崩溃路径也来抢通知权 —— 必须被静默丢弃。
	c.Crash("child", errors.New("late unwind"), nil)
	// 再补一份交卷同样不应重复外发。
	if err := c.Finish("child", Brief{State: StateCompleted, Outcome: "confirmed", Summary: "重复报告"}); err != nil {
		t.Fatalf("重复 finish 不应报错: %v", err)
	}

	msgs := c.Consume("root")
	n := 0
	for _, m := range msgs {
		if m.From == "child" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("父级收到 %d 条终局通知，期望恰好 1 条（%+v）", n, msgs)
	}
	// 终态不可改写：崩溃不能把 completed 覆盖成 crashed。
	if v, _ := c.View("child"); v == nil || v.State != StateCompleted {
		t.Fatalf("终态被改写了: %+v", v)
	}
	if v, _ := c.View("child"); v != nil && v.Brief != nil && v.Brief.Summary != "挡在 WAF 后面" {
		t.Fatalf("终局报告被后到的交卷覆盖: %+v", v.Brief)
	}
}

// 顺带守一条"图已经跑干"的判定：停掉的 agent 不再占活跃位，全部终结后 Idle。
// 上层据此收尾；这条错了会让任务永远显示在跑。
func TestCoordinatorIdleAfterAllTerminal(t *testing.T) {
	c := NewCoordinator("task-4")
	if _, err := c.Register("root", "", "root", true, nil); err != nil {
		t.Fatalf("register root: %v", err)
	}
	if _, err := c.Register("child", "root", "child", false, nil); err != nil {
		t.Fatalf("register child: %v", err)
	}
	if c.Idle() {
		t.Fatal("还有活跃 agent 时不应 Idle")
	}
	if err := c.Stop("child", "方向已变"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if got := c.ActiveCount(); got != 1 {
		t.Fatalf("停掉子 agent 后活跃数 %d，期望 1", got)
	}
	if err := c.Finish("root", Brief{State: StateCompleted, Outcome: "met", Summary: "收工"}); err != nil {
		t.Fatalf("finish root: %v", err)
	}
	if !c.Idle() {
		t.Fatal("全部终结后应为 Idle")
	}
}

// 落盘契约：Snapshot 必须能导出整张图，且不因并发读写 panic。
// 它今天还没有调用点（见 coord_switch.go 的 B4 记录），但一旦上层开始落盘，
// 这里就是唯一能证明"导出的 JSON 至少结构完好"的地方。
func TestCoordinatorSnapshotIsWellFormed(t *testing.T) {
	c := NewCoordinator("task-5")
	if _, err := c.Register("root", "", "root", true, []string{"web"}); err != nil {
		t.Fatalf("register root: %v", err)
	}
	if _, err := c.Register("child", "root", "web-scan", false, nil); err != nil {
		t.Fatalf("register child: %v", err)
	}
	if err := c.Finish("child", Brief{State: StateCompleted, Outcome: "confirmed", Summary: "注入成立", Evidence: []string{"curl -v"}}); err != nil {
		t.Fatalf("finish: %v", err)
	}
	blob, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(blob) == 0 {
		t.Fatal("snapshot 为空")
	}
	var got map[string]any
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatalf("snapshot 不是合法 JSON: %v", err)
	}
	if got["task_id"] != "task-5" {
		t.Fatalf("snapshot 缺少 task_id: %+v", got)
	}
	if agents, ok := got["agents"].([]any); !ok || len(agents) != 2 {
		t.Fatalf("snapshot 应含两个 agent，实际 %+v", got["agents"])
	}
}

// 并发下的三条不变量：同一张图被 spawn / send / wait / finish 同时打，不得 panic
// 或死锁，且"一次终局只通知一次"必须仍然成立。
func TestCoordinatorConcurrentInvariants(t *testing.T) {
	c := NewCoordinator("task-race")
	if _, err := c.Register("root", "", "root", true, nil); err != nil {
		t.Fatalf("register root: %v", err)
	}
	// 报告有两条出路：被并发 wait 交回调用方，或留在父级邮箱里等下一轮注入。
	// 统计必须把两条都算上——否则并发 wait "偷走"的报告会让计数偏低。
	var mu sync.Mutex
	delivered := map[string]int{}
	note := func(msgs []Message) {
		mu.Lock()
		defer mu.Unlock()
		for _, m := range msgs {
			delivered[m.From]++
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "child-" + string(rune('a'+i))
			if _, err := c.Register(id, "root", "child", false, nil); err != nil {
				t.Errorf("register %s: %v", id, err)
				return
			}
			c.MarkTurn(id)
			_ = c.Finish(id, Brief{State: StateCompleted, Outcome: "confirmed", Summary: "ok " + id})
			c.Crash(id, errors.New("late"), nil) // 必须被静默丢弃
		}(i)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.MarkTurn("root")
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			_, msgs := c.WaitFor(ctx, "root", nil, 50*time.Millisecond)
			note(msgs)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 32; i++ {
			_, _ = c.Snapshot()
		}
	}()
	wg.Wait()
	note(c.Consume("root"))

	// 16 个子 agent 每个恰好一条报告（崩溃通知全被丢弃，且没有重复投递）。
	if len(delivered) != 16 {
		t.Fatalf("收到 %d 个子 agent 的报告，期望 16：%+v", len(delivered), delivered)
	}
	for id, n := range delivered {
		if n != 1 {
			t.Fatalf("%s 收到 %d 条报告，期望恰好 1 条", id, n)
		}
	}
}

// 可恢复语义：被预算打断的 agent 允许上层继续投递（额度调大后重跑），
// 而正常交卷/崩溃的 agent 永久拒收。
//
// 这条守的是一处**曾经恒真的守卫**：Register 早先把 resumable 写死成 true，
// 于是「终态投递必须报错」永远不生效。现在可恢复只由 interrupted（runner 在
// 预算用尽时登记）或注册期显式给出的恢复序号决定。
func TestCoordinatorResumableOnlyWhenInterrupted(t *testing.T) {
	c := NewCoordinator("task-resume")
	if _, err := c.Register("root", "", "root", true, nil); err != nil {
		t.Fatalf("register root: %v", err)
	}
	for _, id := range []string{"done", "budget"} {
		if _, err := c.Register(id, "root", "child", false, nil); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
	}
	// 正常交卷 → 不可恢复
	_ = c.Finish("done", Brief{State: StateCompleted, Outcome: "confirmed", Summary: "做完了"})
	// 预算打断 → runner 标记后交卷，仍可恢复
	c.MarkInterrupted("budget")
	_ = c.Finish("budget", Brief{State: StateCompleted, Outcome: "open_proof_gap", Summary: "预算用尽"})

	if _, err := c.Send("root", "done", MsgInformation, "继续"); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("正常交卷的 agent 应拒收，实际 %v", err)
	}
	if _, err := c.Send("root", "budget", MsgInstruction, "额度已调大，继续"); err != nil {
		t.Fatalf("被打断的 agent 应可继续投递，实际 %v", err)
	}
}
