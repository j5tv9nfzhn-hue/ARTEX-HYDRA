package server

import (
	"testing"

	"github.com/Autumn-27/artex/agent"
)

// TestTaskStatusForOutcome 锁定任务终局的映射表。
//
// 背景：settleCoordTask 曾经把 Outcome=="" 与 "met" 一起判成 done，于是
// "根 agent 崩了 / 根本没交卷"与"任务达成"在库里长得一模一样 —— UI 显示
// 完成、统计算成功、报告里却没有一条结论。这条测试是那次修复的护栏：
// 任何"没有结论"的取值都必须落到 failed / paused，不得回到 done。
func TestTaskStatusForOutcome(t *testing.T) {
	cases := []struct {
		outcome string
		want    string
		why     string
	}{
		{"met", "done", "根宣布达成"},
		{"partial", "done", "有结论但未闭环，未闭环项进摘要"},
		{"open_proof_gap", "done", "有结论、留了证据缺口"},
		{"blocked", "failed", "根宣布目标不可达"},
		{outcomeFailed, "failed", "等待根结束时返回错误"},
		{outcomeCanceled, "paused", "用户暂停/停止，可恢复"},
		{outcomeNoBrief, "failed", "根进终态但没交卷"},
		{string(agent.StateCrashed), "failed", "AwaitRoot 兜底 Brief 直接写 AgentState"},
		{string(agent.StateFailed), "failed", "同上"},
		{string(agent.StateCompleted), "done", "根正常收工（Brief 不合规时不改写结论）"},
		{string(agent.StateStopped), "paused", "被上层终止"},
		{"", "failed", "零值 Brief 绝不能算成功"},
		{"something-new", "failed", "未知 outcome 保守处理"},
	}
	for _, c := range cases {
		if got := taskStatusForOutcome(c.outcome); got != c.want {
			t.Errorf("taskStatusForOutcome(%q) = %q, want %q（%s）", c.outcome, got, c.want, c.why)
		}
	}
}

// TestTerminalStatusNeverRunning 是这条修复的"验收口径"：
// 不论根 agent 以何种方式结束，折出来的任务状态都必须落在
// done / failed / paused 三者之一 —— 不允许继续 running。
//
// 之所以单独列一张表：running 是"看起来还在干活"的状态，任务页会一直转圈、
// 槽位不会回收、用户也不会去点重试。把"绝不留在 running"写成断言，比在
// 注释里写"不会卡住"可靠。
func TestTerminalStatusNeverRunning(t *testing.T) {
	all := []string{
		"met", "partial", "open_proof_gap", "blocked",
		outcomeFailed, outcomeCanceled, outcomeNoBrief,
		string(agent.StateCompleted), string(agent.StateCrashed),
		string(agent.StateFailed), string(agent.StateStopped), "",
	}
	for _, outcome := range all {
		st := taskStatusForOutcome(outcome)
		switch st {
		case "done", "failed", "paused":
		default:
			t.Fatalf("outcome %q 折出非终态 %q：任务会永久停在 running", outcome, st)
		}
	}
}
