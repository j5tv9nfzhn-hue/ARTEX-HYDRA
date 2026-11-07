package agent

// 上下文压缩的可观测装配：把 norma 内置 compaction 包一层。压缩真的发生 / 摘要失败时
// 各留一行日志。
//
// 为什么需要这一层：SDK 的 AutoCompact 有三条完全静默的 "什么都不做" 路径 ——
// summarize 为 nil、连续 3 次摘要失败后 tripped 永久关闭、可摘要区间为空 —— 全都只
// return 不打日志。线上看到的现象就是 "上下文早就顶到窗口了，压缩却没发生"，
// 而日志里一条线索都没有。这一层不改任何阈值与行为，只把证据补上：
//
//   - 摘要失败：前两次报错，第三次起改用本地兜底摘要（SDK 因此不会关掉自动压缩）；
//   - 压缩发生：报输入规模、等效窗口与触发点，便于和 UI 的占用率对账。
//
// 注意：wrapper 只实现 harness.Compactor 的三个方法，**不实现** ContextView。
// 内置压缩是 "就地改写消息数组" 的语义，不能被当成投影型上下文管理器
// （agentcore/noa_off_test.go 专门盯着这条）。

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/compaction"
	"github.com/Autumn-27/norma/llm"
)

// observedCompactor 在保留内置压缩全部行为的前提下加日志。
type observedCompactor struct {
	label string
	cfg   compaction.Config
	inner *compaction.Compactor
	// recall 是同一任务的技能记忆：压缩真的发生时打点，供技能再加载在下一次工具
	// 结果里提醒「正文已被移出，需要重新 Skill」。
	recall *skillRecallState

	// noticeArmed 是「本次会话还没提示过」的闸门。压缩前提示只发一次：
	// 反复提醒会让模型把注意力花在"上下文要没了"上，而它该做的是把结论落地。
	noticeArmed bool
	// noticeSkipped 记录「窗口本来就小、提示线被压成 0」这种情况，只记一次日志。
	noticeSkipped bool
}

func newObservedCompactor(label string, cfg *compaction.Config, summary compaction.Summarizer, recall *skillRecallState) *observedCompactor {
	return &observedCompactor{label: label, cfg: *cfg, inner: compaction.New(*cfg, summary), recall: recall, noticeArmed: true}
}

func (c *observedCompactor) Pre(ctx context.Context, msgs []llm.Message, lastInputTokens int) []llm.Message {
	// 第三阶段：先做无 LLM 预剪（旧工具输出），再交给 SDK 判断要不要摘要。
	// 顺序不能反——先摘要会让模型为即将被丢掉的日志付一次调用费。
	if pruned, n := pruneToolOutputs(msgs, c.cfg.ContextWindow, compactionTrigger(&c.cfg)); n > 0 {
		log.Printf("[compaction] %s: 预剪 %d 条旧工具输出（无 LLM，可逆：重跑命令即可）", c.label, n)
		msgs = pruned
	}
	// 预压缩提示：必须在 inner.Pre **之前**注入 —— 注入的消息要和被折叠的
	// 区间一起进摘要，模型才有机会在压缩发生的那一刻就知道"把结论写进台账"。
	// 若放在 inner.Pre 之后，那条消息会落在压缩边界之外，等于什么都没提醒。
	//
	// 判据用「本轮输入 token 是否已过提示线」而不是 c.inner 的返回值：
	// compaction.Compactor 在未压缩时返回**同一个切片**，拿它判断"是否真的压了"
	// 需要比较底层数组，脆弱且依赖 SDK 内部实现。
	if notice, ok := c.beforeNotice(msgs, lastInputTokens); ok {
		msgs = append(msgs, notice)
	}
	out := c.inner.Pre(ctx, msgs, lastInputTokens)
	c.report(msgs, out, lastInputTokens, "自动")
	return out
}

// compactionNoticeRatio 是"提前提示线"相对压缩触发点的比例。
//
// 0.75 的含义：触发点 120K 时，模型在约 90K 就会收到提示。留出 25% 的余量是因为
// 一次工具调用的结果可能就吃掉几万 token —— 提示发出去时还有空间把关键结论
// 落进台账，而不是等到"下一轮必压"时才说。
const compactionNoticeRatio = 0.75

// compactionNoticeThreshold 返回提示线（token）。窗口/触发点过小时返回 0 = 不提示。
func compactionNoticeThreshold(cfg *compaction.Config) int {
	trigger := compactionTrigger(cfg)
	if trigger <= 0 {
		return 0
	}
	t := int(float64(trigger) * compactionNoticeRatio)
	// 低于 8K 的提示线没有意义：一条工具的返回就可能超过它，提示函会成为噪音。
	if t < 8000 {
		return 0
	}
	return t
}

// beforeNotice 决定本轮是否要插入压缩前提示，并返回那条消息。
func (c *observedCompactor) beforeNotice(msgs []llm.Message, lastInputTokens int) (llm.Message, bool) {
	if c == nil || !c.noticeArmed {
		return llm.Message{}, false
	}
	line := compactionNoticeThreshold(&c.cfg)
	if line <= 0 {
		if !c.noticeSkipped {
			c.noticeSkipped = true
			log.Printf("[compaction] %s: 触发点 %d tok 过小，跳过压缩前提示（提示线 < 8K）",
				c.label, compactionTrigger(&c.cfg))
		}
		return llm.Message{}, false
	}
	if lastInputTokens < line {
		return llm.Message{}, false
	}
	c.noticeArmed = false
	log.Printf("[compaction] %s: 注入压缩前提示（本轮输入 %d tok ≥ 提示线 %d，触发点 %d）",
		c.label, lastInputTokens, line, compactionTrigger(&c.cfg))
	return compactionNoticeMessage(line), true
}

// compactionNoticeMessage 是注入正文。
//
// 形状对齐 SDK 的 todo reminder（llm.UserText + <system-reminder> 包裹）：它作为
// 一条 user 消息追加在历史末尾，模型在下一轮看到，随后一起进入压缩区间。
// 三条要求都是"压缩后仍然可执行"的动作，而不是"珍惜上下文"这类无法执行的话：
//   - 把结论写进台账/待办（压缩后还在）；
//   - 不要在上下文里写大段草稿（那是即将被折叠的部分）；
//   - 需要细节时重跑命令或读文件取回（可逆）。
func compactionNoticeMessage(line int) llm.Message {
	body := strings.Join([]string{
		"<system-reminder>",
		"【上下文即将压缩】本轮输入已接近本会话的压缩触发点（提示线约 " + itoaThousand(line) + " tokens）。",
		"下一轮起，较早的对话与工具输出会被折叠成摘要——被折叠的内容你看不到了。",
		"在压缩发生前，请立刻做三件事：",
		"1. 把手上的结论落进台账（record_fact / report_finding / insert_assets）或待办，不要只留在上下文里；",
		"2. 不要继续在回复里写大段草稿或中间笔记——它们大概率会被折叠掉；",
		"3. 之后需要某条细节时，用 Read / Grep 重新读取文件，或重跑命令取回（工具输出可复现）。",
		"压缩本身不需要你处理，继续当前工作即可。",
		"</system-reminder>",
	}, "\n")
	return llm.UserText(body)
}

// itoaThousand 把 token 数缩写成 "90K" 这种形式，避免提示里出现精确到个位的数字
// 让模型误以为那是硬阈值。纯本地实现，不引入 fmt 依赖到热路径。
func itoaThousand(n int) string {
	k := n / 1000
	if k < 1 {
		return "不足 1K"
	}
	return itoaSmall(k) + "K"
}

func itoaSmall(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func (c *observedCompactor) Reactive(ctx context.Context, msgs []llm.Message) ([]llm.Message, bool) {
	out, ok := c.inner.Reactive(ctx, msgs)
	c.report(msgs, out, 0, "溢出兜底")
	return out, ok
}

func (c *observedCompactor) IsOverflow(err error) bool { return c.inner.IsOverflow(err) }

// report 只在 "多出一条压缩边界" 时打日志 —— 那是压缩真的发生过的唯一证据。
func (c *observedCompactor) report(before, after []llm.Message, lastInputTokens int, trigger string) {
	if c == nil || len(after) <= len(before) {
		return
	}
	if llm.LastBoundaryIndex(after) <= llm.LastBoundaryIndex(before) {
		return
	}
	// 只在这里打点：report 只有「真的多出一条压缩边界」才会走到这行。
	// 同一处重新武装压缩前提示：一次压缩完成后，下一次接近触发点时模型应当
	// 再次收到提醒（否则第二次压缩又是无预警的）。
	c.recall.markCompaction()
	c.ObservePostCompact()
	log.Printf("[compaction] %s: 已压缩（%s）输入=%d tok → API 视图估算 %d tok（等效窗口 %d，触发点 %d）",
		c.label, trigger, lastInputTokens,
		compaction.EstimateTokens(llm.MessagesForAPI(after)),
		c.cfg.ContextWindow, compactionTrigger(&c.cfg))
}

const (
	// compactionSummaryFailBudget 是「连续多少次模型摘要失败后改用本地兜底摘要」。
	// 与 SDK 的 maxAutoCompactFails(=3) 对齐：SDK 收到第 3 个错误就会把这个会话的
	// 自动压缩永久关掉，此后上下文只涨不压，直到溢出把这次运行打死——「压缩自己停了」
	// 就是这条路径。所以第 3 次起我们不再给 SDK 递错误，而是自己兜一段本地摘要，
	// 压缩照常发生，等模型恢复再切回模型摘要。
	compactionSummaryFailBudget = 3
	// compactionSummaryRetryAfter 是兜底模式下重新尝试模型摘要的间隔。
	compactionSummaryRetryAfter = 10 * time.Minute
)

// observedSummarizer 记录摘要调用的成败，并在模型摘要连续失败时用本地兜底摘要顶上。
// base 为 nil 时保持 nil（AutoCompact 关闭），不要替调用方把闭区间打开。
func observedSummarizer(label string, base compaction.Summarizer) compaction.Summarizer {
	if base == nil {
		return nil
	}
	var mu sync.Mutex
	fails := 0
	var skipTill time.Time
	return func(ctx context.Context, msgs []llm.Message) (string, error) {
		mu.Lock()
		cooling := !skipTill.IsZero() && time.Now().Before(skipTill)
		mu.Unlock()
		if !cooling {
			summary, err := base(ctx, msgs)
			if err == nil {
				mu.Lock()
				fails, skipTill = 0, time.Time{}
				mu.Unlock()
				log.Printf("[compaction] %s: 摘要完成 %d 条 → %d 字", label, len(msgs), len([]rune(summary)))
				return summary, nil
			}
			mu.Lock()
			fails++
			n := fails
			if n >= compactionSummaryFailBudget {
				skipTill = time.Now().Add(compactionSummaryRetryAfter)
			}
			mu.Unlock()
			if n < compactionSummaryFailBudget {
				log.Printf("[compaction] %s: 摘要失败（第 %d/%d 次）: %v", label, n, compactionSummaryFailBudget, err)
				return summary, err
			}
			log.Printf("[compaction] %s: 摘要连续失败 %d 次，改为本地兜底摘要，自动压缩不关闭（%s 后重试模型）: %v",
				label, n, compactionSummaryRetryAfter, err)
		}
		return fallbackSummary(msgs), nil
	}
}

// fallbackSummary 在模型摘要不可用时用本地规则生成折叠区间的占位摘要。
//
// 折叠必须留下「摘要」这个位置，否则 SDK 会把它记成一次失败，三次之后永久停掉
// 自动压缩。本地摘要保留每轮的工具名、命令/参数首行与结果首行——足够接着干活，
// 并且显式标明它来自本地规则，避免模型把脚手架当成已核实的事实。
func fallbackSummary(msgs []llm.Message) string {
	var b strings.Builder
	b.WriteString("【自动摘要暂不可用：以下是折叠区间内的关键行，由本地规则提取，不是模型总结】\n")
	lines := 0
	for _, m := range msgs {
		if lines >= 80 {
			break
		}
		for _, blk := range m.Content {
			if lines >= 80 {
				break
			}
			switch blk.Type {
			case llm.BlockToolUse:
				b.WriteString("- 调用 " + blk.Name + " " + clipSummaryLine(string(blk.Input)) + "\n")
				lines++
			case llm.BlockToolResult:
				for _, cb := range blk.Content {
					if lines >= 80 {
						break
					}
					if t := clipSummaryLine(cb.Text); t != "" {
						b.WriteString("- 结果 " + t + "\n")
						lines++
					}
				}
			case llm.BlockThinking:
				if t := clipSummaryLine(blk.Thinking); t != "" {
					b.WriteString("- 思考 " + t + "\n")
					lines++
				}
			default:
				if t := clipSummaryLine(blk.Text); t != "" {
					b.WriteString("- " + t + "\n")
					lines++
				}
			}
		}
	}
	b.WriteString("（需要细节时用证据台账或工具重跑取回，不要凭这段占位摘要下结论。）")
	return b.String()
}

// ObservePostCompact 在压缩真正发生后被调用，用于让「压缩前提示」重新武装。
//
// 为什么需要重新武装：提示的目的是"让模型来得及把结论落地"。一次压缩发生后，
// 上下文已经瘦下来，模型很可能再干很久才接近下一次触发点 —— 那时如果不再提醒，
// 第二次压缩又会是无预警的。所以规则是：**跨越一次真实压缩 = 重新获得一次提示**。
//
// 为什么不让 observedCompactor 自己调：压缩可能由 Pre / Reactive / 溢出兜底三条
// 路径触发，只有 runner 知道"这一轮确实发生过压缩"（它比对 CompactionEpoch）。
// 这里只暴露一个幂等的小开关，避免压缩器反过来依赖 runner 的状态机。
func (c *observedCompactor) ObservePostCompact() {
	if c == nil {
		return
	}
	c.noticeArmed = true
}

// clipSummaryLine 取首行并截断，保证兜底摘要不会自己长成一个新的大上下文。
func clipSummaryLine(s string) string {
	s = strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
	if r := []rune(s); len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return s
}

// attachCompaction 装配内置上下文压缩，等价于 agentcore.NewSession 里那句
//
//	compaction.New(*opts.Compaction, compaction.ProviderSummarizer(opts.Provider, opts.MaxTokens, opts.NonStreaming))
//
// 只是多了一层观测。必须在 opts.Provider / opts.MaxTokens / opts.NonStreaming 就位之后
// 调用；noa 若在之后启用会直接覆盖 opts.Compactor（实验功能优先，符合设计）。
func attachCompaction(ctx context.Context, opts *agentcore.Options, window int, label string, recall *skillRecallState) {
	cfg := compactionConfig(window)
	opts.Compaction = nil // 只留一个上下文管理器，避免 agentcore 报 "两者同时设置"
	opts.Compactor = newObservedCompactor(label, cfg,
		observedSummarizer(label, compaction.ProviderSummarizer(opts.Provider, opts.MaxTokens, opts.NonStreaming)), recall)
}
