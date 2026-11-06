package agent

import (
	"errors"
	"testing"
)

// 声明窗口的解析：默认 200K，超过可信上限按上限算并标记 clamped。
// 这条是本次上下文修复的核心——1000K 的声明以前会原样当窗口用，
// 阈值被抬到 800K，压缩永远不触发。
func TestResolveContextWindowTokens(t *testing.T) {
	cases := []struct {
		declared    int
		want        int
		wantClamped bool
	}{
		{0, 200000, false},     // 未填 → 默认 200K
		{128, 128000, false},   // 正常声明
		{200, 200000, false},   // 正好在上限
		{256, 256000, false},   // 显式声明不再被 200K 默认值静默降低
		{1000, 1000000, false}, // 显式 1M 原样发出（只受 maxWindowK 约束）
		{8, 32000, false},      // 低于下限 → 抬到 32K（不 clamp，属于纠偏）
		{-5, 200000, false},    // 负数按未填处理
	}
	for _, c := range cases {
		got, clamped := ResolveContextWindowTokens(c.declared)
		if got != c.want || clamped != c.wantClamped {
			t.Errorf("声明 %dK：got (%d, %v)，want (%d, %v)", c.declared, got, clamped, c.want, c.wantClamped)
		}
	}
}

// 信任上限只约束「用户没填」的假设值，不约束显式声明。
func TestContextWindowCapOnlyAffectsAssumedWindow(t *testing.T) {
	t.Setenv("ARTEX_CONTEXT_WINDOW_CAP_K", "256")
	// 显式 392K 必须原样发出——被 200K/256K 默认值静默降低是比"多压几次"更糟的
	// 故障：没有报错、没有日志，只表现为上下文莫名变短。
	if got, clamped := ResolveContextWindowTokens(392); got != 392000 || clamped {
		t.Errorf("显式 392K 应原样发出，got (%d, %v)", got, clamped)
	}
	// 没填时走默认窗口（200K）；上限只负责把它压低，不会把它抬高到 256K
	if got, _ := ResolveContextWindowTokens(0); got != 200000 {
		t.Errorf("未填时应按默认 200K，got %d", got)
	}
	// 非法上限值回落默认 200K
	t.Setenv("ARTEX_CONTEXT_WINDOW_CAP_K", "abc")
	if got, _ := ResolveContextWindowTokens(0); got != 200000 {
		t.Errorf("非法上限时未填应按 200K，got %d", got)
	}
}

// 触发点 = min(80%, 窗口 − 预留)，且不低于窗口的 1/4。
// 绝对上限（120K）只对假设窗口生效：显式声明的大窗口按真实 80% 触发。
func TestCompactionTriggerTokens(t *testing.T) {
	cases := []struct{ window, want int }{
		{32000, 24000},
		{128000, 96000},
		// 假设窗口（≤ ContextWindowCapK）仍受 120K 绝对线约束：宁可多压几次，
		// 也不能让一个虚高的声明把阈值抬到 80 万。
		{200000, 120000},
		// 显式大窗口（> 200K 信任上限）按真实 80% 触发，120K 的线不再作废用户配置。
		{400000, 320000},
		{1000000, 800000},
	}
	for _, c := range cases {
		if got := CompactionTriggerTokens(c.window); got != c.want {
			t.Errorf("窗口 %d：触发点 %d，want %d（%.1f%%）", c.window, got, c.want, float64(got)*100/float64(c.window))
		}
	}
}

// 溢出识别 + 从报错里捞真实窗口。
func TestContextOverflowDetection(t *testing.T) {
	cases := []struct {
		msg   string
		want  bool
		limit int
	}{
		{`openai: status 400: {"error":{"message":"This model's maximum context length is 131072 tokens."}}`, true, 131072},
		{`Your input exceeds the context window of this model. Please adjust your input and try again.`, true, 0},
		{`exceed_context_size_error: request (8588 tokens) exceeds the available context size (8192 tokens)`, true, 8192},
		{`openai: status 413: Request Entity Too Large`, true, 0},
		{`openai: status 429: rate limit exceeded`, false, 0},
		{`connection reset by peer`, false, 0},
		{``, false, 0},
	}
	for _, c := range cases {
		var err error
		if c.msg != "" {
			err = errors.New(c.msg)
		}
		if got := IsContextOverflowError(err); got != c.want {
			t.Errorf("%q：IsContextOverflow=%v，want %v", c.msg, got, c.want)
		}
		if got := ProviderContextLimit(err); got != c.limit {
			t.Errorf("%q：ProviderContextLimit=%d，want %d", c.msg, got, c.limit)
		}
	}
}
