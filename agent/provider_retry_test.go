package agent

// 建连重试的回归测试（见 provider_retry.go）。
//
// 这一层直接决定「端点抖动时会不会把一次请求放大成十次」，所以两件事都要钉住：
// 梯度与次数必须与 Strix 的 DEFAULT_MODEL_RETRY 逐项相同；重试范围必须只覆盖瞬时故障，
// 且在流式调用上只能覆盖「一个事件都还没交付」的窗口。
//
// 全部用例离线：假的 Provider 按脚本回放，退避用 RetryConfig.ConnectInterval 压到 1ms，
// 否则默认梯度第一次重试就要等 2 秒。

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/norma/llm"
)

// streamRun 描述一次 Stream 的行为：先吐 events，再（可选）以一个错误收尾。
type streamRun struct {
	events []llm.StreamEvent
	err    error
}

// scriptedProvider 按脚本回放：第 i 次调用取第 i 个结果，脚本用完就一直成功。
type scriptedProvider struct {
	calls        int
	completeErrs []error
	streamRuns   []streamRun
	// onComplete 在第 i 次 Complete 时被调用，用来制造「调用过程中 ctx 被取消」。
	onComplete func(i int)
}

func (p *scriptedProvider) Complete(ctx context.Context, req llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	i := p.calls
	p.calls++
	if p.onComplete != nil {
		p.onComplete(i)
	}
	if i < len(p.completeErrs) && p.completeErrs[i] != nil {
		return llm.Message{}, "", llm.Usage{}, p.completeErrs[i]
	}
	return llm.UserText("ok"), "", llm.Usage{}, nil
}

func (p *scriptedProvider) Stream(ctx context.Context, req llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	i := p.calls
	p.calls++
	run := streamRun{events: []llm.StreamEvent{{Type: llm.SETextDelta, Text: "ok"}}}
	if i < len(p.streamRuns) {
		run = p.streamRuns[i]
	}
	return func(yield func(llm.StreamEvent, error) bool) {
		for _, ev := range run.events {
			if !yield(ev, nil) {
				return
			}
		}
		if run.err != nil {
			yield(llm.StreamEvent{}, run.err)
		}
	}
}

// statusErr 造一个与 norma 文案同格式的错误（"<前缀>: status <code>: <body>"）。
func statusErr(code int) error {
	return fmt.Errorf("openai: status %d: fixture body", code)
}

// fastRetry 是只重试、不真等 2 秒的配置：固定 1ms 间隔。
func fastRetry(attempts int) RetryConfig {
	return RetryConfig{ConnectAttempts: attempts, ConnectInterval: time.Millisecond}
}

// collectStream 把一次流式调用收敛成「收到的文本 + 收尾错误」。
func collectStream(t *testing.T, prov llm.Provider) ([]string, error) {
	t.Helper()
	var (
		texts []string
		err   error
	)
	for ev, streamErr := range prov.Stream(context.Background(), llm.CompletionRequest{}) {
		if streamErr != nil {
			err = streamErr
			break
		}
		texts = append(texts, ev.Text)
	}
	return texts, err
}

// TestRetryLadderMatchesStrix 钉住退避梯度：2s 起、每次翻倍、封顶 90s。
func TestRetryLadderMatchesStrix(t *testing.T) {
	want := []time.Duration{
		2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
		32 * time.Second, 64 * time.Second, 90 * time.Second, 90 * time.Second,
	}
	for attempt, w := range want {
		if got := retryLadderDelay(attempt); got != w {
			t.Fatalf("第 %d 次重试前应等 %v，实得 %v", attempt, w, got)
		}
	}
	// 异常入参（负数）不该给出比首次更短的等待。
	if got := retryLadderDelay(-1); got != 2*time.Second {
		t.Fatalf("负数入参应按首次重试算 2s，实得 %v", got)
	}
}

// TestRetryPolicyDefaultsAndOverrides 钉住次数与梯度的解析口径。
func TestRetryPolicyDefaultsAndOverrides(t *testing.T) {
	t.Run("未配置时用 Strix 的默认", func(t *testing.T) {
		attempts, backoff := retryPolicy(RetryConfig{})
		if attempts != defaultConnectAttempts {
			t.Fatalf("默认次数应为 %d，实得 %d", defaultConnectAttempts, attempts)
		}
		if got := backoff(0); got != 2*time.Second {
			t.Fatalf("默认梯度首次应为 2s，实得 %v", got)
		}
		if got := backoff(6); got != 90*time.Second {
			t.Fatalf("默认梯度应封顶 90s，实得 %v", got)
		}
	})
	t.Run("填了次数就用填的", func(t *testing.T) {
		if attempts, _ := retryPolicy(RetryConfig{ConnectAttempts: 3}); attempts != 3 {
			t.Fatalf("应尊重配置的 3 次，实得 %d", attempts)
		}
	})
	t.Run("负数关掉这层", func(t *testing.T) {
		if attempts, _ := retryPolicy(RetryConfig{ConnectAttempts: -1}); attempts != 0 {
			t.Fatalf("负数应关掉重试，实得 %d", attempts)
		}
	})
	t.Run("填了固定间隔就替换梯度", func(t *testing.T) {
		_, backoff := retryPolicy(RetryConfig{ConnectInterval: 250 * time.Millisecond})
		for _, attempt := range []int{0, 1, 5} {
			if got := backoff(attempt); got != 250*time.Millisecond {
				t.Fatalf("固定间隔下第 %d 次重试应为 250ms，实得 %v", attempt, got)
			}
		}
	})
}

// TestCompleteRetriesTransientFailures 断言瞬时失败会被重放，直到成功。
func TestCompleteRetriesTransientFailures(t *testing.T) {
	inner := &scriptedProvider{completeErrs: []error{statusErr(429), statusErr(503), nil}}
	prov := wrapProviderRetry(inner, fastRetry(3))
	if _, _, _, err := prov.Complete(context.Background(), llm.CompletionRequest{}); err != nil {
		t.Fatalf("重试后应当成功，实得 %v", err)
	}
	if inner.calls != 3 {
		t.Fatalf("应调用 3 次（失败 2 次 + 成功 1 次），实得 %d", inner.calls)
	}
}

// TestCompleteStopsAfterExhaustingAttempts 断言次数用尽后把最后一次错误原样返回。
func TestCompleteStopsAfterExhaustingAttempts(t *testing.T) {
	inner := &scriptedProvider{completeErrs: []error{statusErr(503), statusErr(503), statusErr(503), statusErr(503)}}
	prov := wrapProviderRetry(inner, fastRetry(2))
	_, _, _, err := prov.Complete(context.Background(), llm.CompletionRequest{})
	if err == nil || !strings.Contains(err.Error(), "status 503") {
		t.Fatalf("应返回最后一次失败，实得 %v", err)
	}
	if inner.calls != 3 {
		t.Fatalf("2 次重试意味着共 3 次调用，实得 %d", inner.calls)
	}
}

// TestCompleteDoesNotRetryHardFailures 断言确定性拒绝不重试：重放改变不了答案，
// 只会把「余额不足 / 密钥失效」推迟 60 多秒才告诉操作者。
func TestCompleteDoesNotRetryHardFailures(t *testing.T) {
	for _, code := range []int{400, 401, 402, 403, 404, 413, 422} {
		inner := &scriptedProvider{completeErrs: []error{statusErr(code), nil}}
		prov := wrapProviderRetry(inner, fastRetry(5))
		if _, _, _, err := prov.Complete(context.Background(), llm.CompletionRequest{}); err == nil {
			t.Fatalf("status %d 应当直接失败", code)
		}
		if inner.calls != 1 {
			t.Fatalf("status %d 不该重试，实调 %d 次", code, inner.calls)
		}
	}
}

// TestCompleteRetriesTransportFailures 断言传输层故障会被重放。
//
// http.Client.Do 的失败一律包在 *url.Error 里，而 *url.Error 实现了 net.Error——所以
// 「没有状态码时只重试 net.Error」这一条正好覆盖 SDK 重试的那一类，别把连接重置丢在
// 不重试的一侧。
func TestCompleteRetriesTransportFailures(t *testing.T) {
	transport := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
	inner := &scriptedProvider{completeErrs: []error{transport, nil}}
	prov := wrapProviderRetry(inner, fastRetry(2))
	if _, _, _, err := prov.Complete(context.Background(), llm.CompletionRequest{}); err != nil {
		t.Fatalf("传输层故障应当被重放，实得 %v", err)
	}
	if inner.calls != 2 {
		t.Fatalf("应重放一次，实调 %d 次", inner.calls)
	}
}

// TestCompleteDoesNotRetryNonTransportErrors 断言「不是网络故障、也没有状态码」的错误
// 不重放：SDK 同样只重试建连阶段，200 之后才暴露的解码类错误该交给外层的安全窗口重试
// 与 llmrepair，在这一层白等 5 轮（最长 62 秒）只是把结论推迟。
func TestCompleteDoesNotRetryNonTransportErrors(t *testing.T) {
	decode := errors.New("unexpected end of JSON input in response body")
	inner := &scriptedProvider{completeErrs: []error{decode, nil}}
	prov := wrapProviderRetry(inner, fastRetry(5))
	if _, _, _, err := prov.Complete(context.Background(), llm.CompletionRequest{}); err == nil {
		t.Fatal("解码类错误应当直接上抛")
	}
	if inner.calls != 1 {
		t.Fatalf("解码类错误不该重试，实调 %d 次", inner.calls)
	}
}

// TestCompleteStopsWhenContextCancelled 断言调用过程中 ctx 已结束就不再重试。
func TestCompleteStopsWhenContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inner := &scriptedProvider{
		completeErrs: []error{statusErr(429), nil},
		onComplete:   func(int) { cancel() },
	}
	prov := wrapProviderRetry(inner, fastRetry(5))
	if _, _, _, err := prov.Complete(ctx, llm.CompletionRequest{}); err == nil {
		t.Fatal("ctx 已取消时不该继续重试并成功")
	}
	if inner.calls != 1 {
		t.Fatalf("ctx 已取消时不该重试，实调 %d 次", inner.calls)
	}
}

// TestStreamRetriesBeforeFirstEvent 断言「还没交付任何事件」的流失败会被重放。
func TestStreamRetriesBeforeFirstEvent(t *testing.T) {
	inner := &scriptedProvider{streamRuns: []streamRun{
		{err: statusErr(529)},
		{events: []llm.StreamEvent{{Type: llm.SETextDelta, Text: "hello"}}},
	}}
	prov := wrapProviderRetry(inner, fastRetry(2))
	got, streamErr := collectStream(t, prov)
	if streamErr != nil {
		t.Fatalf("重放后应当成功，实得 %v", streamErr)
	}
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("应当只收到重放后那一条输出，实得 %v", got)
	}
	if inner.calls != 2 {
		t.Fatalf("应重放一次，实调 %d 次", inner.calls)
	}
}

// TestStreamDoesNotRetryAfterDelivery 是本层最重要的一条：已经交付过事件就不能重放，
// 否则同一段输出会出现两次（下游把它当两次模型输出记录）。
func TestStreamDoesNotRetryAfterDelivery(t *testing.T) {
	inner := &scriptedProvider{streamRuns: []streamRun{
		{events: []llm.StreamEvent{{Type: llm.SETextDelta, Text: "partial"}}, err: statusErr(503)},
		{events: []llm.StreamEvent{{Type: llm.SETextDelta, Text: "duplicate"}}},
	}}
	prov := wrapProviderRetry(inner, fastRetry(5))
	got, streamErr := collectStream(t, prov)
	if streamErr == nil {
		t.Fatal("交付过输出之后必须把错误上抛，不能重放")
	}
	if len(got) != 1 || got[0] != "partial" {
		t.Fatalf("不该出现第二段输出，实得 %v", got)
	}
	if inner.calls != 1 {
		t.Fatalf("交付过输出之后不该再调用，实调 %d 次", inner.calls)
	}
}

// TestStreamStopsWhenConsumerStops 断言调用方主动停止时不再拉取后续事件、也不重试。
func TestStreamStopsWhenConsumerStops(t *testing.T) {
	inner := &scriptedProvider{streamRuns: []streamRun{
		{events: []llm.StreamEvent{
			{Type: llm.SETextDelta, Text: "first"},
			{Type: llm.SETextDelta, Text: "second"},
		}},
	}}
	prov := wrapProviderRetry(inner, fastRetry(5))
	var got []string
	for ev, err := range prov.Stream(context.Background(), llm.CompletionRequest{}) {
		if err != nil {
			t.Fatalf("不该有错误：%v", err)
		}
		got = append(got, ev.Text)
		break // 调用方只取一条
	}
	if len(got) != 1 || got[0] != "first" {
		t.Fatalf("应当只交付 1 条，实得 %v", got)
	}
	if inner.calls != 1 {
		t.Fatalf("调用方停止后不该再调用，实调 %d 次", inner.calls)
	}
}

// TestWrapProviderRetrySkipsWhenDisabled 断言关掉这层时不套包装、也不重试。
func TestWrapProviderRetrySkipsWhenDisabled(t *testing.T) {
	inner := &scriptedProvider{completeErrs: []error{statusErr(429), nil}}
	prov := wrapProviderRetry(inner, RetryConfig{ConnectAttempts: -1})
	if prov != inner {
		t.Fatal("关掉重试时应当原样返回 inner，不做无谓包装")
	}
	if _, _, _, err := prov.Complete(context.Background(), llm.CompletionRequest{}); err == nil {
		t.Fatal("关掉重试后应当直接返回失败")
	}
	if inner.calls != 1 {
		t.Fatalf("关掉重试后不该重试，实调 %d 次", inner.calls)
	}
}

// TestModelErrorStatusParsesSDKFormat 钉住状态码的取法（与 llmpool.statusOf 同一个约定）。
func TestModelErrorStatusParsesSDKFormat(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{statusErr(429), 429},
		{fmt.Errorf("anthropic: wrap: %w", statusErr(529)), 529},
		{errors.New("dial tcp 127.0.0.1: nothing listening"), 0},
		{errors.New("status code 99"), 0},
	}
	for _, c := range cases {
		if got := modelErrorStatus(c.err); got != c.want {
			t.Fatalf("%v：应解析出 %d，实得 %d", c.err, c.want, got)
		}
	}
}
