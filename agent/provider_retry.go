package agent

// 建连重试由本项目接管（对齐 Strix 的 DEFAULT_MODEL_RETRY）。
//
// 为什么不让 SDK 做：norma 的 llm.Config 只能选「它的内置梯队」（0.5s 起、×2、封顶 8s）
// 或「一个固定间隔」——没有初始延迟 / 倍数 / 上限三个旋钮，表达不出 Strix 的「2s 起、
// ×2、封顶 90s」。唯一能让两边梯度逐次相同的办法是本项目自己跑这个循环，并且把 SDK
// 那层关掉（Config.NewProvider 传 MaxRetries=-1）：两层叠加会让一次瞬时故障变成十次
// 模型调用、退避总时长翻倍，比任何一边单独跑都更糟。
//
// 重试范围与 SDK 的 doStream 同一口径（那里也只重试「还没拿到 200」的建连阶段）：
//   - 有状态码时只重试 408 / 429 / 5xx，再补 529（Anthropic 过载；外层「同 provider
//     安全窗口」重试早就把它当瞬时故障，这里不该比那边更保守）；
//   - 没有状态码时只重试传输层故障（net.Error）。这一条正好等于 SDK 的重试集：
//     http.Client.Do 的失败一律包在 *url.Error 里，而 *url.Error 实现了 net.Error；
//     请求构造失败、以及 200 之后才暴露的解码类错误都不是 net.Error，两边都不重放
//     （后者由外层 server/task_llm.go 的同 provider 重试与 llmrepair 接管）；
//   - ctx 取消不重试；402 也不重试——额度耗尽由录制代理把「429 + 余额文案」改写成 402
//     （见 quotaAwareHTTPClient），属于确定性拒绝，重放改变不了答案，只会把「密钥失效 /
//     余额不足」这类结论推迟到 60 多秒之后才告诉操作者。

import (
	"context"
	"iter"
	"time"

	"github.com/Autumn-27/artex/llmclass"
	"github.com/Autumn-27/norma/llm"
)

// Strix DEFAULT_MODEL_RETRY 的梯度：2s 起、每次翻倍、封顶 90s。次数见
// defaultConnectAttempts（5）。与 Strix 唯一的形式差别是抖动：那边 jitter=False，
// 这里也不加抖动，好让两边每一次的等待完全相同。
const (
	defaultRetryInitialDelay = 2 * time.Second
	defaultRetryMultiplier   = 2.0
	defaultRetryMaxDelay     = 90 * time.Second
)

// retryLadderDelay 返回第 attempt 次重试前要等多久（attempt 从 0 起，即首次重试等 2s）。
//
// 用循环而不是位移：次数由配置层给（允许到 20），位移在极端值上会溢出，而这里只需要
// 「翻倍到封顶就停」。
func retryLadderDelay(attempt int) time.Duration {
	return ladderDelay(attempt, defaultRetryInitialDelay, defaultRetryMultiplier, defaultRetryMaxDelay)
}

// ladderDelay 是可配版本的退避梯度：起始延迟 × 倍数^attempt，封顶。
//
// 倍数是 float64：设置页允许填 1.5 这种「半档」退避（2s → 3s → 4.5s），整数倍不够用。
func ladderDelay(attempt int, initial time.Duration, multiplier float64, max time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if initial <= 0 {
		initial = defaultRetryInitialDelay
	}
	if multiplier <= 1 {
		multiplier = defaultRetryMultiplier
	}
	if max <= 0 {
		max = defaultRetryMaxDelay
	}
	d := float64(initial)
	for i := 0; i < attempt; i++ {
		if d >= float64(max) {
			return max
		}
		d *= multiplier
	}
	if d > float64(max) {
		return max
	}
	return time.Duration(d)
}

// 重试、server 的安全窗口重试都按同一个约定读它，各自再写一份正则迟早会漂移。
func modelErrorStatus(err error) int {
	return llmclass.StatusOf(err)
}

// retryableModelCallError 报告一次模型调用失败是否值得原样重放。
//
// 口径来自 llmclass（统一判定，见该包的头注）：只有「瞬时故障」才原地重试——
// 限流、5xx、系统繁忙、传输层故障。端点级故障（余额不足、封号、鉴权失效、
// 上游不可用）不重试，直接上抛给外层的端点轮询；请求本身不合法（400、上下文
// 超长）同样不重试。
func retryableModelCallError(err error) bool {
	return llmclass.Classify(err) == llmclass.VerdictRetry
}

// retryProvider 在建连层重放失败的模型调用（见文件头）。
type retryProvider struct {
	inner    llm.Provider
	attempts int
	// backoff 返回第 attempt 次重试前的等待（attempt 从 0 起）。
	backoff func(attempt int) time.Duration
}

// wrapProviderRetry 按 RetryConfig 给 Provider 套一层建连重试。
//
// 关掉这层（负数）或没有可套的 provider 时原样返回 inner，不做无谓包装。
func wrapProviderRetry(inner llm.Provider, r RetryConfig) llm.Provider {
	if inner == nil {
		return nil
	}
	attempts, backoff := retryPolicy(r)
	if attempts <= 0 {
		return inner
	}
	return &retryProvider{inner: inner, attempts: attempts, backoff: backoff}
}

// retryPolicy 解析这次调用用哪套建连重试：次数与退避梯度。
//
// 次数：未配置(0) → defaultConnectAttempts；负数 → 关掉这层；>0 → 用该值。
// 梯度：未配置 → Strix 的 2s/×2/90s；设置页配了梯度三旋钮就用它；配了固定间隔 →
// 用那个固定值（这个旋钮的原有语义就是「拿固定间隔替换指数退避」，见
// db.RetryRule.Interval）。
func retryPolicy(r RetryConfig) (attempts int, backoff func(int) time.Duration) {
	attempts = defaultConnectAttempts
	if r.ConnectAttempts != 0 {
		attempts = r.ConnectAttempts
	}
	if attempts < 0 {
		attempts = 0
	}
	initial, multiplier, max := r.ConnectInitial, r.ConnectMultiplier, r.ConnectMax
	backoff = func(attempt int) time.Duration {
		return ladderDelay(attempt, initial, multiplier, max)
	}
	if r.ConnectInterval > 0 {
		fixed := r.ConnectInterval
		backoff = func(int) time.Duration { return fixed }
	}
	return attempts, backoff
}

// Complete 重放非流式调用：它要么整体成功、要么整体失败，不存在「已经交付了一半输出」
// 的问题，所以任何瞬时失败都能原样重发。
func (p *retryProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	var (
		msg   llm.Message
		sr    string
		usage llm.Usage
		err   error
	)
	for attempt := 0; ; attempt++ {
		msg, sr, usage, err = p.inner.Complete(ctx, req)
		if err == nil || ctx.Err() != nil || attempt >= p.attempts || !retryableModelCallError(err) {
			return msg, sr, usage, err
		}
		if !sleepCtx(ctx, p.backoff(attempt)) {
			return llm.Message{}, "", llm.Usage{}, ctx.Err()
		}
	}
}

// Stream 重放流式调用，但只在「一个事件都还没交给调用方」时重放。
//
// 这条边界与 SDK、以及本项目的「同 provider 安全窗口」重试一致：一旦 yield 过事件，
// 输出就可能已经被下游（界面、transcript）记录，重发会让同一段输出出现两次。此时把
// 错误原样上抛，交给更外层的故障转移与收尾逻辑。
func (p *retryProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		for attempt := 0; ; attempt++ {
			delivered := false
			var lastErr error
			for ev, err := range p.inner.Stream(ctx, req) {
				if err != nil {
					lastErr = err
					break
				}
				delivered = true
				if !yield(ev, nil) {
					return // 调用方自己停了，不再重试
				}
			}
			if lastErr == nil {
				return // 正常结束
			}
			if delivered || ctx.Err() != nil || attempt >= p.attempts || !retryableModelCallError(lastErr) {
				yield(llm.StreamEvent{}, lastErr)
				return
			}
			if !sleepCtx(ctx, p.backoff(attempt)) {
				yield(llm.StreamEvent{}, ctx.Err())
				return
			}
		}
	}
}

// sleepCtx 等 d，可被 ctx 中断。返回 false 表示 ctx 已结束（等待前或等待中）。
//
// 以变量形式暴露没有意义——退避梯度本身由 retryPolicy 注入，测试改梯度用
// RetryConfig.ConnectInterval 即可，不需要再留一个测试钩子。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
