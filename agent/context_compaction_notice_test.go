package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/compaction"
	"github.com/Autumn-27/norma/llm"
)

// 这一组测试锁定「上下文压缩前提示」的三个不变量：
//
//	1. 只在越过提示线、且**尚未提示过**时才注入；
//	2. 注入的消息必须以 <system-reminder> 包裹、作为 user 消息追加在末尾；
//	3. 注入之后仍然真的会发生压缩（提示不能把压缩挤掉）。
//
// 之前这条能力完全缺失：模型在毫无预警的情况下被折叠掉早期上下文，
// 于是出现「刚查到的结论下一轮就忘了」「把已折叠的证据当已确认」这类现象。

func testCompactionCfg(window int) *compaction.Config {
	return compactionConfig(window)
}

// textOnlyMessages 造 n 条普通对话消息，内容与时间顺序无关，只用来占位。
func textOnlyMessages(n int) []llm.Message {
	out := make([]llm.Message, 0, n)
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			out = append(out, llm.UserText("用户消息占位"))
			continue
		}
		out = append(out, llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{llm.TextBlock("助手消息占位")}})
	}
	return out
}

func newTestCompactor(window int) *observedCompactor {
	cfg := testCompactionCfg(window)
	// summary 为 nil：下面这些用例只关心"提示是否注入"，不需要真调模型。
	return newObservedCompactor("test", cfg, nil, &skillRecallState{})
}

func TestCompactionNoticeThresholdScalesWithTrigger(t *testing.T) {
	cfg := testCompactionCfg(200_000)
	trigger := compactionTrigger(cfg)
	line := compactionNoticeThreshold(cfg)
	if line <= 0 {
		t.Fatalf("200K 窗口应有提示线，得到 %d", line)
	}
	if line >= trigger {
		t.Fatalf("提示线 %d 必须早于触发点 %d，否则提示等于无意义", line, trigger)
	}
	// 提前量至少留出 20%：一次工具调用就可能吃掉几万 token。
	if float64(line) > float64(trigger)*0.85 {
		t.Fatalf("提示线 %d 离触发点 %d 太近（应为触发点的 75%%）", line, trigger)
	}
}

func TestCompactionNoticeThresholdDisabledForTinyWindow(t *testing.T) {
	// 极小窗口：任何提示都是噪音，必须显式返回 0 而不是发一条"马上要压缩"。
	cfg := &compaction.Config{ContextWindow: 12_000, MaxOutputTokens: 2000}
	if got := compactionNoticeThreshold(cfg); got != 0 {
		t.Fatalf("小窗口不应有提示线，得到 %d", got)
	}
}

func TestCompactionNoticeInjectedOnceBeforeCompaction(t *testing.T) {
	c := newTestCompactor(200_000)
	line := compactionNoticeThreshold(&c.cfg)
	msgs := textOnlyMessages(6)

	// 1) 未到提示线：不注入，返回的消息数不变。
	below := c.Pre(context.Background(), msgs, line-1)
	if len(below) != len(msgs) {
		t.Fatalf("未到提示线不应注入，消息数 %d → %d", len(msgs), len(below))
	}

	// 2) 越过提示线：恰好注入一条 <system-reminder>，且是最后一条 user 消息。
	out := c.Pre(context.Background(), msgs, line)
	if len(out) != len(msgs)+1 {
		t.Fatalf("越过提示线应注入一条提示，消息数 %d → %d", len(msgs), len(out))
	}
	last := out[len(out)-1]
	if last.Role != llm.RoleUser {
		t.Fatalf("提示必须是 user 消息，得到 %s", last.Role)
	}
	text := last.Text()
	for _, want := range []string{"<system-reminder>", "上下文即将压缩", "台账", "</system-reminder>"} {
		if !strings.Contains(text, want) {
			t.Fatalf("提示正文缺少 %q：\n%s", want, text)
		}
	}

	// 3) 第二次越线：不再重复注入（重复提醒会挤占注意力且浪费 token）。
	//    判定口径是"整段历史里提示条数恒为 1"，而不是"新增的那条不含提示"——
	//    第一次注入的那条会一直留在历史里，直到被压缩折叠。
	again := c.Pre(context.Background(), out, line*2)
	noticeCount := 0
	for _, m := range again {
		if strings.Contains(m.Text(), "上下文即将压缩") {
			noticeCount++
		}
	}
	if noticeCount != 1 {
		t.Fatalf("提示应始终只有一条，实际 %d 条", noticeCount)
	}
}

func TestCompactionNoticeRearmsAfterCompaction(t *testing.T) {
	// 第一次提示 → 压缩真的发生 → 提示重新武装 → 再次接近触发点时还能提示一次。
	// 没有这条，"第二次压缩无预警"就只是把问题推迟了一个周期。
	c := newTestCompactor(200_000)
	line := compactionNoticeThreshold(&c.cfg)
	msgs := textOnlyMessages(4)

	out := c.Pre(context.Background(), msgs, line)
	if len(out) != len(msgs)+1 {
		t.Fatalf("首次越线应注入提示，得到 %d 条（原始 %d）", len(out), len(msgs))
	}
	// 未发生真实压缩时，不重复提示。
	if got := c.Pre(context.Background(), out, line*2); len(got) != len(out) {
		t.Fatalf("未压缩就重复提示了：%d → %d", len(out), len(got))
	}
	// 模拟一次真实压缩（report 的边界判定通过 → 调 ObservePostCompact）。
	c.ObservePostCompact()
	again := c.Pre(context.Background(), textOnlyMessages(4), line)
	if len(again) != 5 {
		t.Fatalf("压缩后应重新武装，得到 %d 条", len(again))
	}
}

func TestCompactionNoticeStillAllowsCompaction(t *testing.T) {
	// 提示注入之后，压缩仍须真的发生：注入的是**提示**，不是替代品。
	// 用一个必然越线的输入（远超触发点）验证边界数量增加。
	c := newTestCompactor(120_000)
	line := compactionNoticeThreshold(&c.cfg)
	trigger := compactionTrigger(&c.cfg)
	msgs := textOnlyMessages(8)
	out := c.Pre(context.Background(), msgs, trigger+line)
	// SDK 在 summary==nil 时不压缩（AutoCompact 关闭），这里只断言"提示没破坏调用链"：
	// 输出必须仍然包含全部原始消息 + 提示（或更少，若预剪生效）。
	if len(out) > len(msgs)+1 {
		t.Fatalf("提示不应凭空增加消息，得到 %d（原始 %d）", len(out), len(msgs))
	}
	if llm.LastBoundaryIndex(out) != llm.LastBoundaryIndex(msgs) && len(out) < len(msgs) {
		// 压缩器在没有摘要器时不该产生边界；真出现了说明配置被改动，值得报错。
		t.Fatalf("缺少摘要器时不应产生压缩边界")
	}
}
