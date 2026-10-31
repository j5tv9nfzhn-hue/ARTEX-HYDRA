package agent

import (
	"strings"
	"testing"
)

func TestLayerPlanForRoles(t *testing.T) {
	plan := LayerPlanFor("worker", 0)
	if len(plan) != 3 {
		t.Fatalf("layers=%d", len(plan))
	}
	kinds := map[LayerKind]bool{}
	for _, p := range plan {
		kinds[p.Layer] = true
		if p.Role != "worker" {
			t.Fatalf("role=%s", p.Role)
		}
		if p.Layer == LayerWindow {
			w, _ := p.Detail["window_tokens"].(int)
			if w != defaultWindowK*1000 {
				t.Fatalf("default window=%d", w)
			}
		}
	}
	for _, k := range []LayerKind{LayerToolClip, LayerWindow, LayerGraphDigest} {
		if !kinds[k] {
			t.Fatalf("missing layer %s", k)
		}
	}

	plan2 := LayerPlanFor("planner", 24000)
	for _, p := range plan2 {
		if p.Layer == LayerWindow {
			w, _ := p.Detail["window_tokens"].(int)
			if w != 24000 {
				t.Fatalf("window=%d", w)
			}
		}
	}
}

// DescribeLayerPlan 是运维看到的唯一自我说明，必须同时给出：
//   - 每层的真实状态（三层在 chat 下都是启用，不是旁路）；
//   - 窗口数字（window_tokens 只在窗口层有）；
//   - 该层到底做了什么（Detail.note 必须打出来，否则只剩一个英文枚举名）。
//
// 原断言要求出现"旁路"，但 LayerPlanFor 对 chat 返回的三层全是 Enabled=true ——
// 断言与实现矛盾，是拆改时留下的错测试。
func TestDescribeLayerPlan(t *testing.T) {
	s := DescribeLayerPlan(LayerPlanFor("chat", 8000))
	for _, want := range []string{"tool_output_clip", "conversation_window", "cold_graph_digest", "启用", "window=8000", "单条工具结果裁剪"} {
		if !strings.Contains(s, want) {
			t.Fatalf("desc 缺少 %q：%q", want, s)
		}
	}
	// 旁路只在 Enabled=false 时出现，且要被明确标出来 —— 运维靠它判断某层是否被关掉。
	bypass := DescribeLayerPlan([]LayerPlan{{Layer: LayerToolClip, Role: "chat", Enabled: false,
		Detail: map[string]any{"note": "临时关闭以对比成本"}}})
	if !strings.Contains(bypass, "旁路") || !strings.Contains(bypass, "临时关闭以对比成本") {
		t.Fatalf("旁路层未被标明：%q", bypass)
	}
}

func TestChainCompactionObserver(t *testing.T) {
	var seen []string
	chain := ChainCompactionObserver(
		func(ev CompactionEvent) { seen = append(seen, "a:"+ev.Stage) },
		nil,
		func(ev CompactionEvent) {
			seen = append(seen, "b:"+ev.Stage)
			panic("observer must not break the chain")
		},
		func(ev CompactionEvent) { seen = append(seen, "c:"+ev.Stage) },
	)
	chain(CompactionEvent{Stage: "start", Phase: "minor"})
	if len(seen) != 3 || seen[0] != "a:start" || seen[2] != "c:start" {
		t.Fatalf("seen=%v", seen)
	}
}
