package agent

import (
	"context"
	"iter"
	"strings"
	"time"

	"github.com/Autumn-27/norma/llm"
)

// toolInputEvery 是工具入参帧的节流窗口。与文本帧同一个节奏（约 25 帧/秒）：
// 参数通常几十到几千字符，40ms 一帧既看得出「在打字」，又不会把 SSE 刷爆。
const toolInputEvery = 40 * time.Millisecond

// maxToolCallsPerTurn 是单轮（模型一次响应）允许下发的工具调用数上限。
//
// 「一轮」的边界就是 Stream 的一次调用：harness 每个 turn 只调一次 Stream
// （norma harness/query.go 的 streamAndExecute，一次 CallModel 出一条 assistant
// 响应），计数器是闭包局部变量，因此天然每轮归零。SEMessageStart 上的重置只是
// 补一条同流重放的情况——注意 OpenAI 的两条适配器（openai / openai-responses）
// 根本不发 message_start，只有 Anthropic 发，所以绝不能把轮次边界寄托在它身上。
//
// 语义与 Strix 的 max_tool_calls_per_turn 对齐：它是**单次响应的安全阀**，不是
// 整个任务的终身额度——下一轮响应重新计数。拦的是「一轮里炸开」这种失控形态：
// 模型偶尔会在一次响应里铺开几十次调用（逐个文件 cat、批量端口扫描），既把界面
// 刷爆，也容易一轮就把上下文撑满。掐在这里之后，模型下一轮会看到前 32 次的结果
// 并据此收敛，而不是被一条报错打断。
//
// 为什么是「丢弃」而不是「报错」：超限的调用在这一层就不往下转发，harness 的
// llm.Accumulator 收不到 SEToolUseStart，就不会为它生成 tool_use 块，自然也不会
// 执行（Accumulator 只在 SEToolUseStart 上登记块）。模型那一侧看到的是「这一轮只
// 发出去了 32 次调用」，与 Strix 的 filter_items 口径一致——不额外注入工具报错去
// 污染它的推理链，也不需要 fork SDK 去加钩子。
const maxToolCallsPerTurn = 32

// wrapToolStream 给 Provider 套一层「工具入参截流」，让界面在工具真正执行之前
// 就能逐字看到模型正在生成的参数；顺带把单轮工具调用数钳在 maxToolCallsPerTurn 以内。
//
// 为什么在这一层：norma 的 llm 层本来就在逐帧发 tool_input_delta（Anthropic 的
// input_json_delta、OpenAI 的 function.arguments 都是按 SSE chunk 出来的），是
// harness 把它攒到 tool_use 结束才一次性吐一整块。ARTEX 自己的 Provider 装饰链
// （llmrec、限流都在同一层）位于 harness 之下、SSE 之上，正好截得到原始增量——
// 因此不必 fork SDK，norma 升级也不会把这里改掉。
//
// 只发两种帧，都是「累计值」而不是增量，界面拿到直接整体替换：
//
//	tool_start {ToolUseID, Tool}         一次调用的参数开始流
//	tool_input {ToolUseID, Tool, Text}   截至目前的完整参数 JSON
//
// 已知边界：OpenAI 的并行工具调用会在同一条消息里按 index 交错下发参数，而
// llm.StreamEvent 不带 index（Accumulator 也是同一个限制），所以交错时后一段
// 参数会被挂到最近一次 tool_start 上。串行调用（含 Anthropic 全部情况、以及
// 绝大多数实际请求）不受影响；落库口径与最终参数始终以 harness 定稿的
// tool_use 为准，实时帧只影响「生成中」的观感。
func wrapToolStream(inner llm.Provider) llm.Provider {
	if inner == nil {
		return nil
	}
	return &toolStreamProvider{inner: inner}
}

type toolStreamProvider struct{ inner llm.Provider }

func (p *toolStreamProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return p.inner.Complete(ctx, req)
}

func (p *toolStreamProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		var (
			toolID, toolName string
			input            strings.Builder
			lastEmit         time.Time
			turnCalls        int  // 本轮（本次响应）已经放行的工具调用数
			dropped          bool // 当前这次调用是否因超限被丢弃
		)
		reset := func() {
			toolID, toolName = "", ""
			input.Reset()
			lastEmit = time.Time{}
		}
		for ev, err := range p.inner.Stream(ctx, req) {
			if err == nil {
				switch ev.Type {
				case llm.SEMessageStart:
					// 清掉上一段的名字与参数缓冲。同 provider 重试会在同一条流里
					// 重放整段输出，从这里重新开始因此不会串台；额度也一并归零。
					// 这只是兜底：真正的轮次边界是 Stream 本身的一次调用，OpenAI
					// 适配器不发这个事件，靠的是闭包变量每轮新建。
					reset()
					turnCalls, dropped = 0, false
				case llm.SEToolUseStart:
					reset()
					turnCalls++
					if turnCalls > maxToolCallsPerTurn {
						// 超限：整次调用连同它的参数帧一起丢掉。必须连参数帧一起丢——
						// Accumulator 把 SEToolInputJSON 追加到「最近登记的那次调用」
						// 上，只丢头不丢参数的话，第 33 次调用的 {"i":33} 会被接到第
						// 32 次调用的后面，把那次本来合法的入参撑成一段非法 JSON。
						dropped = true
						continue
					}
					dropped = false
					toolID, toolName = ev.ToolID, ev.ToolName
					emitLive(ctx, LiveDelta{Kind: "tool_start", Tool: toolName, ToolUseID: toolID})
				case llm.SEToolInputJSON:
					if dropped {
						continue
					}
					input.WriteString(ev.Text)
					if now := time.Now(); now.Sub(lastEmit) >= toolInputEvery {
						lastEmit = now
						emitLive(ctx, LiveDelta{Kind: "tool_input", Tool: toolName, ToolUseID: toolID, Text: input.String()})
					}
				}
			}
			if !yield(ev, err) {
				return
			}
			if err != nil {
				return
			}
		}
	}
}
