package agent

import (
	"context"
	"fmt"
	"iter"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

// scriptProvider 只回放预置事件，用来驱动 toolStreamProvider。
type scriptProvider struct{ events []llm.StreamEvent }

func (p scriptProvider) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return llm.Message{}, "end_turn", llm.Usage{}, nil
}

func (p scriptProvider) Stream(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		for _, ev := range p.events {
			if !yield(ev, nil) {
				return
			}
		}
	}
}

// toolCallScript 生成「一轮响应里 n 次工具调用」的事件流：每次调用一条
// tool_use_start，后面跟两条参数分片，合起来是 {"i":N}。
func toolCallScript(n int) []llm.StreamEvent {
	out := []llm.StreamEvent{{Type: llm.SEMessageStart}}
	for i := 1; i <= n; i++ {
		out = append(out,
			llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: fmt.Sprintf("tu-%d", i), ToolName: "read_file"},
			llm.StreamEvent{Type: llm.SEToolInputJSON, Text: fmt.Sprintf(`{"i":%d`, i)},
			llm.StreamEvent{Type: llm.SEToolInputJSON, Text: "}"},
		)
	}
	return append(out, llm.StreamEvent{Type: llm.SEMessageStop})
}

// drainToolStream 把包装后的流同时喂给真实的 llm.Accumulator（harness 用的就是
// 它）并回收转发出来的事件，这样断言的是「harness 最终会执行哪些调用」，而不是
// 包装器自己声称做了什么。
func drainToolStream(t *testing.T, prov llm.Provider) (starts []string, inputs map[string]string) {
	t.Helper()
	inputs = map[string]string{}
	acc := llm.NewAccumulator()
	for ev, err := range prov.Stream(context.Background(), llm.CompletionRequest{}) {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		acc.Add(ev)
		if ev.Type == llm.SEToolUseStart {
			starts = append(starts, ev.ToolID)
		}
	}
	for _, b := range acc.Message().Content {
		if b.Type == llm.BlockToolUse {
			inputs[b.ID] = string(b.Input)
		}
	}
	return starts, inputs
}

// TestToolStreamCapsCallsPerTurn 是一轮里铺开远多于上限的调用：只有前 32 次进
// Accumulator，其余连头带参数一起消失。
func TestToolStreamCapsCallsPerTurn(t *testing.T) {
	starts, inputs := drainToolStream(t, wrapToolStream(scriptProvider{events: toolCallScript(40)}))
	if len(starts) != maxToolCallsPerTurn {
		t.Fatalf("放行的工具调用数 = %d, 期望 %d", len(starts), maxToolCallsPerTurn)
	}
	for i, id := range starts {
		if want := fmt.Sprintf("tu-%d", i+1); id != want {
			t.Fatalf("第 %d 个放行的调用 = %q, 期望 %q（必须是前 32 个，不能是后 32 个）", i+1, id, want)
		}
	}
	if len(inputs) != maxToolCallsPerTurn {
		t.Fatalf("Accumulator 生成的 tool_use 块 = %d, 期望 %d", len(inputs), maxToolCallsPerTurn)
	}
}

// TestToolStreamCappedInputNotPolluted 是最容易踩坏的一条：只丢 tool_use_start
// 而放过它的 SEToolInputJSON，那些参数会被 Accumulator 追加到第 32 次调用头上，
// 把它撑成非法 JSON——第 32 次调用会因此执行失败或执行错参数。
func TestToolStreamCappedInputNotPolluted(t *testing.T) {
	_, inputs := drainToolStream(t, wrapToolStream(scriptProvider{events: toolCallScript(40)}))
	last := fmt.Sprintf("tu-%d", maxToolCallsPerTurn)
	if got, want := inputs[last], fmt.Sprintf(`{"i":%d}`, maxToolCallsPerTurn); got != want {
		t.Fatalf("最后一次放行的调用入参 = %q, 期望 %q（被丢弃调用的参数泄漏进来了）", got, want)
	}
	for i := 1; i <= maxToolCallsPerTurn; i++ {
		id := fmt.Sprintf("tu-%d", i)
		if got, want := inputs[id], fmt.Sprintf(`{"i":%d}`, i); got != want {
			t.Fatalf("%s 入参 = %q, 期望 %q", id, got, want)
		}
	}
}

// TestToolStreamUnderLimitPassesThrough 是刚好压线和不压线：一个都不该丢。
func TestToolStreamUnderLimitPassesThrough(t *testing.T) {
	for _, n := range []int{1, 2, 31, maxToolCallsPerTurn} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			starts, inputs := drainToolStream(t, wrapToolStream(scriptProvider{events: toolCallScript(n)}))
			if len(starts) != n {
				t.Fatalf("放行的工具调用数 = %d, 期望 %d", len(starts), n)
			}
			if len(inputs) != n {
				t.Fatalf("tool_use 块 = %d, 期望 %d", len(inputs), n)
			}
		})
	}
}

// TestToolStreamLimitResetsEachTurn 证明它是「单次响应的安全阀」而不是终身额度：
// 两条消息各 20 次调用，一条都不该被丢。
func TestToolStreamLimitResetsEachTurn(t *testing.T) {
	events := []llm.StreamEvent{{Type: llm.SEMessageStart}}
	for i := 1; i <= 20; i++ {
		events = append(events,
			llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: fmt.Sprintf("a-%d", i), ToolName: "read_file"},
			llm.StreamEvent{Type: llm.SEToolInputJSON, Text: `{}`},
		)
	}
	events = append(events, llm.StreamEvent{Type: llm.SEMessageStop}, llm.StreamEvent{Type: llm.SEMessageStart})
	for i := 1; i <= 20; i++ {
		events = append(events,
			llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: fmt.Sprintf("b-%d", i), ToolName: "read_file"},
			llm.StreamEvent{Type: llm.SEToolInputJSON, Text: `{}`},
		)
	}
	events = append(events, llm.StreamEvent{Type: llm.SEMessageStop})

	starts, inputs := drainToolStream(t, wrapToolStream(scriptProvider{events: events}))
	if len(starts) != 40 {
		t.Fatalf("两轮共放行的调用数 = %d, 期望 40（额度应每轮重置）", len(starts))
	}
	if starts[0] != "a-1" || starts[19] != "a-20" || starts[20] != "b-1" || starts[39] != "b-20" {
		t.Fatalf("放行顺序不对: 首=%s 第20=%s 第21=%s 末=%s", starts[0], starts[19], starts[20], starts[39])
	}
	if len(inputs) != 40 {
		t.Fatalf("tool_use 块 = %d, 期望 40", len(inputs))
	}
}

// TestToolStreamNonToolEventsSurviveCapping 确认超限只影响工具调用：正文与思考
// 片段照旧透传，不会因为丢事件把一轮输出切掉。
func TestToolStreamNonToolEventsSurviveCapping(t *testing.T) {
	events := []llm.StreamEvent{{Type: llm.SEMessageStart}, {Type: llm.SETextDelta, Text: "开始"}}
	for i := 1; i <= 34; i++ {
		events = append(events,
			llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: fmt.Sprintf("tu-%d", i), ToolName: "read_file"},
			llm.StreamEvent{Type: llm.SEToolInputJSON, Text: `{}`},
		)
	}
	events = append(events, llm.StreamEvent{Type: llm.SETextDelta, Text: "结束"}, llm.StreamEvent{Type: llm.SEMessageStop})

	var text string
	acc := llm.NewAccumulator()
	prov := wrapToolStream(scriptProvider{events: events})
	for ev, err := range prov.Stream(context.Background(), llm.CompletionRequest{}) {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		acc.Add(ev)
		if ev.Type == llm.SETextDelta {
			text += ev.Text
		}
	}
	if text != "开始结束" {
		t.Fatalf("正文 = %q, 期望 %q", text, "开始结束")
	}
	blocks := 0
	for _, b := range acc.Message().Content {
		if b.Type == llm.BlockToolUse {
			blocks++
		}
	}
	if blocks != maxToolCallsPerTurn {
		t.Fatalf("tool_use 块 = %d, 期望 %d", blocks, maxToolCallsPerTurn)
	}
}

// openAIShapeScript 生成 OpenAI 适配器形状的事件流：没有 message_start（openai 与
// openai-responses 两条适配器都不发它，只有 Anthropic 发），末尾是 message_delta +
// message_stop。上限若把轮次边界寄托在 message_start 上，这里就会露馅。
func openAIShapeScript(n int) []llm.StreamEvent {
	out := []llm.StreamEvent{}
	for i := 1; i <= n; i++ {
		out = append(out,
			llm.StreamEvent{Type: llm.SEToolUseStart, ToolID: fmt.Sprintf("tu-%d", i), ToolName: "read_file"},
			llm.StreamEvent{Type: llm.SEToolInputJSON, Text: fmt.Sprintf(`{"i":%d}`, i)},
		)
	}
	return append(out,
		llm.StreamEvent{Type: llm.SEMessageDelta, StopReason: "tool_use"},
		llm.StreamEvent{Type: llm.SEMessageStop},
	)
}

// TestToolStreamCapsWithoutMessageStart 是 OpenAI 形状：流里一条 message_start 都
// 没有，闸门依然要生效。
func TestToolStreamCapsWithoutMessageStart(t *testing.T) {
	starts, inputs := drainToolStream(t, wrapToolStream(scriptProvider{events: openAIShapeScript(40)}))
	if len(starts) != maxToolCallsPerTurn {
		t.Fatalf("放行的工具调用数 = %d, 期望 %d", len(starts), maxToolCallsPerTurn)
	}
	last := fmt.Sprintf("tu-%d", maxToolCallsPerTurn)
	if got, want := inputs[last], fmt.Sprintf(`{"i":%d}`, maxToolCallsPerTurn); got != want {
		t.Fatalf("最后一次放行的入参 = %q, 期望 %q", got, want)
	}
}

// TestToolStreamBudgetResetsPerStreamCall 是「按响应而非终身额度」的真正证明：
// harness 每个 turn 调一次 Stream，同一个 provider 连调两次（模拟两轮），两轮都要
// 拿到完整的 32 额度。若额度退化成终身的，第二轮会一条都不剩。
func TestToolStreamBudgetResetsPerStreamCall(t *testing.T) {
	prov := wrapToolStream(scriptProvider{events: openAIShapeScript(20)})
	for turn := 1; turn <= 3; turn++ {
		starts, inputs := drainToolStream(t, prov)
		if len(starts) != 20 {
			t.Fatalf("第 %d 轮放行的调用数 = %d, 期望 20（额度应每次 Stream 调用重置）", turn, len(starts))
		}
		if len(inputs) != 20 {
			t.Fatalf("第 %d 轮 tool_use 块 = %d, 期望 20", turn, len(inputs))
		}
	}
}
