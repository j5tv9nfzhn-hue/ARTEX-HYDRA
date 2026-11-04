package llmclass

// 统一分类口径的回归测试。
//
// 这一层决定「原地重试」还是「换模型/供应商」，判错的代价是双向的：把端点级故障
// 当成瞬时故障，用户会盯着一个已经欠费的端点白等一分钟；把瞬时故障当成端点级故障，
// 则会反复换供应商，把本来能用的额度换光。所以两类都要钉住。
//
// 特别要盯住的是**文案语义先于状态码**：中转站常用 429 表达余额不足、用 200 表达
// 「系统繁忙」，只看状态码会把「该立刻换端点」误判成「原地重试 5 次」。

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
)

// statusErr 造一个与 norma 文案同格式的错误（"<前缀>: status <code>: <body>"）。
func statusErr(code int, body string) error {
	return fmt.Errorf("openai: status %d: %s", code, body)
}

func TestClassifyByStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Verdict
	}{
		{"限流 429", statusErr(429, "rate limit exceeded"), VerdictRetry},
		{"请求超时 408", statusErr(408, "request timeout"), VerdictRetry},
		{"服务端错误 500", statusErr(500, "internal error"), VerdictRetry},
		{"网关错误 502", statusErr(502, "bad gateway"), VerdictRetry},
		{"不可用 503", statusErr(503, "service unavailable"), VerdictRetry},
		{"超时 504", statusErr(504, "gateway timeout"), VerdictRetry},
		{"过载 529", statusErr(529, "overloaded_error"), VerdictRetry},
		{"余额不足 402", statusErr(402, "payment required"), VerdictSwitch},
		{"鉴权失败 401", statusErr(401, "invalid api key"), VerdictSwitch},
		{"禁止 403", statusErr(403, "forbidden"), VerdictSwitch},
		{"模型不存在 404", statusErr(404, "model not found"), VerdictSwitch},
		{"请求不合法 400", statusErr(400, "bad request"), VerdictFail},
		{"参数不合法 422", statusErr(422, "unprocessable entity"), VerdictFail},
		{"上下文超长 413", statusErr(413, "context length exceeded"), VerdictFail},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.err); got != c.want {
				t.Fatalf("%v：应判为 %s，实得 %s", c.err, c.want, got)
			}
		})
	}
}

func TestClassifyPrefersSemanticsOverStatus(t *testing.T) {
	// 这三条是本包存在的全部理由：只看状态码会全判错。
	cases := []struct {
		name string
		err  error
		want Verdict
	}{
		{"429 里说余额不足 → 换端点", statusErr(429, "insufficient balance"), VerdictSwitch},
		{"429 里说额度已用尽 → 换端点", statusErr(429, "额度已用尽"), VerdictSwitch},
		{"200 带繁忙文案 → 原地重试", errors.New("openai: 服务繁忙，请稍后重试"), VerdictRetry},
		{"200 带系统繁忙 → 原地重试", errors.New("openai: system busy, try again later"), VerdictRetry},
		{"200 说上游不可用 → 换端点", errors.New("openai: upstream unavailable"), VerdictSwitch},
		{"200 说账号停用 → 换端点", errors.New("openai: 账号已停用"), VerdictSwitch},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.err); got != c.want {
				t.Fatalf("%v：应判为 %s，实得 %s", c.err, c.want, got)
			}
		})
	}
}

func TestClassifyQuotaVersusRateLimit(t *testing.T) {
	// 「quota exceeded」既可能是余额耗尽，也可能是速率限制——Google 系 API 常把普通限流
	// 写成 quota exceeded。带速率限制措辞的必须留在当前端点（瞬时），否则每次速率
	// 限制都会把用户踢到另一家供应商。
	if got := Classify(errors.New("openai: quota exceeded for quota metric 'Generate requests per minute'")); got != VerdictRetry {
		t.Fatalf("速率限制应判为 retry，实得 %s", got)
	}
	if got := Classify(errors.New("openai: quota exceeded for this organization")); got != VerdictSwitch {
		t.Fatalf("账号额度耗尽应判为 switch，实得 %s", got)
	}
}

func TestClassifyContextOverflowIsFatal(t *testing.T) {
	// 上下文超长重试同一份请求必然还失败，必须靠压缩。
	for _, msg := range []string{
		"openai: maximum context length is 200000 tokens",
		"openai: prompt is too long",
		"openai: 上下文超长",
	} {
		if got := Classify(errors.New(msg)); got != VerdictFail {
			t.Fatalf("%q：应判为 fail，实得 %s", msg, got)
		}
	}
}

func TestClassifyCancellationIsFatal(t *testing.T) {
	// 用户停的任务，换端点只是白烧额度。
	if got := Classify(context.Canceled); got != VerdictFail {
		t.Fatalf("取消应判为 fail，实得 %s", got)
	}
	if got := Classify(fmt.Errorf("wrapped: %w", context.DeadlineExceeded)); got != VerdictFail {
		t.Fatalf("超时包装应判为 fail，实得 %s", got)
	}
}

func TestClassifyTransportFailure(t *testing.T) {
	// 传输层故障原地重试有意义。
	transport := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}
	if got := Classify(transport); got != VerdictRetry {
		t.Fatalf("传输层故障应判为 retry，实得 %s", got)
	}
	// 响应解码类错误原地重试没意义，但换个供应商可能能解（各家 SSE 格式不同）。
	if got := Classify(errors.New("unexpected end of JSON input")); got != VerdictSwitch {
		t.Fatalf("解码类错误应判为 switch，实得 %s", got)
	}
}

func TestClassifyNilIsNone(t *testing.T) {
	if got := Classify(nil); got != VerdictNone {
		t.Fatalf("nil 应判为 none，实得 %s", got)
	}
}

func TestClassifyWithManualKeywords(t *testing.T) {
	// 人工增补：接上没见过的供应商措辞。
	rules := Rules{RetryKeywords: []string{"请稍后再试", "vendor-x busy"}}
	cases := []struct {
		msg  string
		want Verdict
	}{
		{"openai: 请稍后再试", VerdictRetry},
		{"openai: vendor-x busy", VerdictRetry},
		{"openai: 某个没见过的措辞", VerdictSwitch},
	}
	for _, c := range cases {
		if got := ClassifyWith(errors.New(c.msg), rules); got != c.want {
			t.Fatalf("%q：应判为 %s，实得 %s", c.msg, c.want, got)
		}
	}
}

func TestClassifyWithDisabledDefaultKeyword(t *testing.T) {
	// 人工禁用：某个内置词在这台机器上被误判，可以摘掉。
	//
	// 用 retry 词来验：禁用 "system busy" 之后，同一句话不该再被判成 retry——它没有
	// 状态码、也不是传输层故障，于是落到「既不是已知重试也不是已知端点级」的默认分支
	// （switch）。若不禁用，它是 retry。两者可区分，才测得出「禁用真的生效了」。
	rules := Rules{DisableRetry: []string{"system busy"}}
	if got := ClassifyWith(errors.New("openai: system busy"), rules); got != VerdictSwitch {
		t.Fatalf("禁用后 system busy 不应再判为 retry，实得 %s", got)
	}
	// 未禁用的那条仍然生效。
	if got := ClassifyWith(errors.New("openai: service busy"), rules); got != VerdictRetry {
		t.Fatalf("未禁用的词仍应生效，实得 %s", got)
	}
}

func TestSetRulesAffectsClassify(t *testing.T) {
	// 进程级热更新：设置页保存后立即对所有判定路径生效。
	//
	// 探针的两个约束，缺一个测试就失去意义：
	//
	//  1. 必须带状态码（这里用 500 → 默认判 retry）。没有状态码时，清空词表后它会
	//     落到「无状态码、非传输层 → switch」的默认分支（该分支的存在与用途见
	//     TestClassifyWithDisabledDefaultKeyword 的注释），于是「清空后不再命中」
	//     与「清空后落到默认分支」在断言上无法区分——写得再严也测不出热更新。
	//  2. 措辞本身不能命中任何内置词表（也不能是「额度耗尽」这类额度信号），
	//     否则清空人工词表后它仍会因内置口径判 switch，同样无法区分。
	//
	// 满足这两点后，清空前后的判据分别是 switch 与 retry，热更新才真正可验证。
	defer SetRules(Rules{})
	SetRules(Rules{SwitchKeywords: []string{"vendor-y 账户受限"}})
	if got := Classify(statusErr(500, "vendor-y 账户受限")); got != VerdictSwitch {
		t.Fatalf("进程级词表应立即生效（语义先于状态码），实得 %s", got)
	}
	SetRules(Rules{})
	if got := Classify(statusErr(500, "vendor-y 账户受限")); got == VerdictSwitch {
		t.Fatalf("清空词表后不应再命中该词，实得 %s", got)
	}
}

func TestIsQuotaExhaustedStrictness(t *testing.T) {
	// 只认明确的余额/计费/额度耗尽信号。
	if !IsQuotaExhausted("insufficient balance") {
		t.Fatal("insufficient balance 应算额度耗尽")
	}
	if !IsQuotaExhausted("余额不足") {
		t.Fatal("余额不足 应算额度耗尽")
	}
	if IsQuotaExhausted("too many requests") {
		t.Fatal("普通限流不算额度耗尽")
	}
	if IsQuotaExhausted("openai: status 401: invalid api key") {
		t.Fatal("鉴权失败不算额度耗尽")
	}
	if IsQuotaExhausted("openai: status 500: quota metric rate limit") {
		t.Fatal("5xx 里的 quota 字样不算额度耗尽")
	}
}

func TestStatusOf(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{statusErr(429, "x"), 429},
		{fmt.Errorf("anthropic: wrap: %w", statusErr(529, "y")), 529},
		{errors.New("dial tcp: connection refused"), 0},
	}
	for _, c := range cases {
		if got := StatusOf(c.err); got != c.want {
			t.Fatalf("%v：应解析出 %d，实得 %d", c.err, c.want, got)
		}
	}
}

func TestVerdictString(t *testing.T) {
	for v, want := range map[Verdict]string{
		VerdictNone: "none", VerdictFail: "fail", VerdictRetry: "retry",
		VerdictSwitch: "switch", VerdictQuality: "quality",
	} {
		if got := v.String(); got != want {
			t.Fatalf("%d 应显示为 %q，实得 %q", v, want, got)
		}
	}
}
