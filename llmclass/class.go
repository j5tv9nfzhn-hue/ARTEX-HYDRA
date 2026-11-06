// Package llmclass 是模型调用失败的**统一判定口径**。
//
// 为什么需要它：模型供应商千奇百怪，同一件事有几十种说法——限流有 429、也有
// "system busy"；余额不足有 402、也有 "insufficient balance"、甚至 429 里塞一句
// "额度已用尽"；封号可能是 403、也可能是 200 加一句 "账号已停用"。而判定逻辑此前
// 散在五处（llmpool 的 failover/熔断、server 的配额与安全窗口重试、agent 的额度与
// 上下文判定、provider 的建连重试），各自列了一小段关键词与状态码，于是同一类失败
// 在不同层得到不同结论：例如 429 在 llmpool 看来该「换端点」，在 provider 的建连
// 重试里却会先原地重试 5 次（最长 62 秒）才轮到换端点——用户等的是后者，机器付的
// 是前者。
//
// 这个包把判定收敛成一处，并按「处置方式」而不是按「错误来源」分类：
//
//	VerdictRetry   瞬时故障：在同一端点上按梯度重试有意义（限流、5xx、繁忙、传输层）
//	VerdictSwitch  端点级故障：本端点重试无意义，应尽快换一个模型/供应商
//	               （余额不足、封号、鉴权失效、上游不可用）
//	VerdictFail    请求本身有问题：重试与换端点都没意义（400、上下文超长、请求取消）
//	VerdictQuality 响应质量类：由 llmrepair 自己的口径决定是否修复或换端点
//
// 判定顺序是刻意安排的：**文案语义先于状态码**。因为中转站常用 429 表达余额不足、
// 用 200 表达「系统繁忙」，只看状态码会把「立刻该换端点」误判成「原地重试 5 次」。
//
// 关键词表与状态码表都允许人工增补（见 Rules），以便接上没见过的供应商措辞。
package llmclass

import (
	"context"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/Autumn-27/artex/llmrepair"
)

// Verdict 是一次失败后的处置结论。
type Verdict int

const (
	// VerdictNone 不是错误。
	VerdictNone Verdict = iota
	// VerdictFail 请求本身有问题：重试与换端点都没意义，直接上抛。
	VerdictFail
	// VerdictRetry 瞬时故障：在同一端点上按梯度重试有意义；重试耗尽后仍可换端点。
	VerdictRetry
	// VerdictSwitch 端点级故障：本端点重试无意义，应尽快换一个模型/供应商。
	VerdictSwitch
	// VerdictQuality 响应质量类：交给 llmrepair 判定。
	VerdictQuality
	// VerdictOverflow 上下文溢出：请求超出本端点窗口。
	//
	// 它**不是**端点故障——端点本身是健康的，换一个窗口更大的端点就能成功。
	// 因此它既不该触发熔断（那是 VerdictSwitch 的语义），也不该像 VerdictFail
	// 那样把整条链判死。调用方应把它当成"换更大的窗口重试"的信号：按窗口从大到小
	// 重排剩余候选，并把真实窗口记下来供下一次装配使用。
	VerdictOverflow
)

// String 返回人类可读的标签（给日志与设置页回显用）。
func (v Verdict) String() string {
	switch v {
	case VerdictNone:
		return "none"
	case VerdictFail:
		return "fail"
	case VerdictRetry:
		return "retry"
	case VerdictSwitch:
		return "switch"
	case VerdictQuality:
		return "quality"
	case VerdictOverflow:
		return "overflow"
	}
	return "unknown"
}

// Rules 是人工可调的关键词表。
//
// 三张表都是**追加**语义：默认词表在代码里（见 defaultKeywords），这里只加不减，
// 除非显式把某个默认词放进 Disable* 里。追加而不是替换，是为了让「供应商换了个
// 说法」只需要加一个词，而不必把整套默认词表抄一遍（抄一遍必然会漏、必然漂移）。
type Rules struct {
	// RetryKeywords 追加到「同端点重试」词表：限流、繁忙、暂时不可用。
	RetryKeywords []string
	// SwitchKeywords 追加到「换端点」词表：余额不足、封号、鉴权失效、上游不可用。
	SwitchKeywords []string
	// FailKeywords 追加到「直接失败」词表：请求本身不合法。
	FailKeywords []string
	// DisableRetry / DisableSwitch / DisableFail 从对应默认词表里移除某个词。
	DisableRetry  []string
	DisableSwitch []string
	DisableFail   []string
}

// DefaultRules 是内置词表（不含任何人工调整）。
func DefaultRules() Rules { return Rules{} }

// effective 合并默认词表与人工调整，得到本次判定实际使用的三张表。
func (r Rules) effective() (retry, switchKW, fail []string) {
	retry = apply(defaultRetryKeywords, r.RetryKeywords, r.DisableRetry)
	switchKW = apply(defaultSwitchKeywords, r.SwitchKeywords, r.DisableSwitch)
	fail = apply(defaultFailKeywords, r.FailKeywords, r.DisableFail)
	return retry, switchKW, fail
}

// apply 把人工追加与禁用合并到默认词表上。
func apply(base, add, disable []string) []string {
	out := make([]string, 0, len(base)+len(add))
	for _, w := range base {
		if containsFold(disable, w) {
			continue
		}
		out = append(out, w)
	}
	for _, w := range add {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" || containsFold(out, w) {
			continue
		}
		out = append(out, w)
	}
	return out
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// activeRules 是当前生效的人工调整词表。
//
// 用进程级单例而不是把它穿进每个构造路径：分类词表本来就是**全局**语义——同一个
// 供应商的同一种措辞，在哪个端点上遇到都该判成同一类。穿进 agent.Config / llmpool.Pool
// 要改三四处构造点，还会出现「provider 那份生效了、池子那份没生效」的不一致。
var activeRules atomic.Pointer[Rules]

// SetRules 更新生效的人工调整词表。
//
// 调用方（server）在启动与保存设置时调用。并发安全：判定路径只读。
func SetRules(r Rules) {
	activeRules.Store(&r)
}

// CurrentRules 返回当前生效的人工调整词表。
func CurrentRules() Rules {
	if p := activeRules.Load(); p != nil {
		return *p
	}
	return Rules{}
}

// defaultRetryKeywords：瞬时故障的文案。命中即「原地按梯度重试」。
//
// 「限流」类词刻意只放明确表示**速率**的措辞（rate limit / too many requests）；
// 「额度/quota」不在这里——它归端点级（余额不足），判据见 IsQuotaExhausted。
var defaultRetryKeywords = []string{
	"system busy", "server busy", "service busy", "service temporarily unavailable",
	"temporarily unavailable", "temporarily unavailable, please retry",
	"overloaded", "overload_error", "upstream is busy", "try again later",
	"too many requests", "rate limit", "rate_limit_exceeded", "too_many_requests",
	"slow down", "服务繁忙", "系统繁忙", "繁忙中", "系统忙", "请稍后重试", "稍后重试",
	"当前请求量过大", "请求过于频繁", "限流",
}

// defaultSwitchKeywords：端点级故障的文案。命中即「尽快换一个模型/供应商」。
//
// 「上游不可用」归这一类：换供应商比在同一家原地等更可能恢复。
var defaultSwitchKeywords = []string{
	"upstream unavailable", "upstream unavailable:", "no healthy upstream",
	"all upstream", "upstream_error", "provider unavailable", "service unavailable for",
	"invalid api key", "invalid_api_key", "incorrect api key", "unauthorized",
	"authentication", "authentication_error", "invalid token", "token expired",
	"api key expired", "key revoked", "account disabled", "account suspended",
	"account banned", "suspended", "deactivated", "banned",
	"上游不可用", "上游服务不可用", "无可用上游", "密钥无效", "密钥过期",
	"鉴权失败", "认证失败", "未授权", "账号已停用", "账号被禁用", "账号被封禁",
	"已封禁", "封号", "账号异常",
}

// defaultFailKeywords：请求本身不合法。命中即「重试与换端点都没意义」。
var defaultFailKeywords = []string{
	"malformed request", "invalid request", "invalid_request_error",
	"bad request", "unsupported parameter", "unsupported value",
	"请求格式错误", "参数不合法", "请求不合法",
}

// statusRe 从错误文案里取 HTTP 状态码。
//
// norma 的错误文案是 "<前缀>: status <code>: <body>"，llmpool 与 provider 都用同一个
// 约定解析——SDK 没把状态码放进错误类型，只能靠文案，所以这一处必须是唯一来源。
var statusRe = regexp.MustCompile(`\bstatus (\d{3})\b`)

// StatusOf 返回错误携带的 HTTP 状态码，取不到返回 0（还没走到 HTTP 响应）。
func StatusOf(err error) int {
	if err == nil {
		return 0
	}
	m := statusRe.FindStringSubmatch(err.Error())
	if m == nil {
		return 0
	}
	code, convErr := strconv.Atoi(m[1])
	if convErr != nil {
		return 0
	}
	return code
}

// quotaTransientLimit 识别「其实是速率限制、不是余额耗尽」的措辞。
//
// Provider 的 API 常把普通限流写成 "quota exceeded"（尤其 Google 系的 quota metric），
// 这类限制会随时间自行恢复，必须留在当前端点，不能当成余额不足换供应商。
var quotaTransientLimit = regexp.MustCompile(`(?i)(?:\b(?:rpm|tpm|rpd|qps)\b|quota[_\s-]*metric|rate[_\s-]*limit|too many requests|(?:requests?|tokens?)\s+(?:per|/)\s*(?:second|minute)|(?:per|/)\s*(?:second|minute)\s+(?:requests?|tokens?)|generate[_\s-]*requests[_\s-]*per[_\s-]*(?:minute|second)|tokens?[_\s-]*per[_\s-]*(?:minute|second))`)

// nonQuotaHTTPStatus 识别「无论文案多像余额不足都不算」的响应。
//
// 鉴权类与供应商 5xx 永远不换供应商——网关偶尔会在 5xx 的 body 里回显一句含 quota
// 的话，照字面判会得出「余额不足，换一家」，于是把一次上游抖动误判成欠费。
var nonQuotaHTTPStatus = regexp.MustCompile(`(?i)(?:status(?:\s+code)?|http(?:\s+status)?)\s*[=:]?\s*(?:401|403|5\d\d)\b`)

// IsQuotaExhausted 判断文案是否在讲「余额/额度真的耗尽」。
//
// 刻意严格：只认明确的余额、计费、额度耗尽信号。普通 429、鉴权失败、网络故障、
// 服务器故障都**不算**——它们要么归瞬时重试，要么归端点级，与「欠费」是三件事。
func IsQuotaExhausted(message string) bool {
	message = strings.ToLower(message)
	if nonQuotaHTTPStatus.MatchString(message) {
		return false
	}
	if quotaTransientLimit.MatchString(message) {
		return false
	}
	markers := []string{
		"insufficient_quota", "quota_exceeded", "quota exceeded", "quota exhausted",
		"exceeded your current quota", "billing_hard_limit_reached",
		"billing hard limit", "billing_not_active", "credit balance", "insufficient credit",
		"insufficient balance", "balance is too low", "payment required", "status 402",
		"余额不足", "额度不足", "额度已用尽", "额度耗尽", "额度已耗尽", "欠费",
	}
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	// gRPC RESOURCE_EXHAUSTED 同时用于「账号额度」与「请求速率」两种含义，
	// 只有在同一个错误里没有出现速率限制措辞时，才当作真的额度耗尽。
	return strings.Contains(message, "resource_exhausted") &&
		!strings.Contains(message, "rate limit") &&
		!strings.Contains(message, "too many requests")
}

// contextOverflowHints 是「请求超出上下文窗口」的措辞表。
var contextOverflowHints = []string{
	"context length", "context_length", "context window", "context size",
	"maximum context", "too many tokens", "prompt is too long", "input is too long",
	"exceeds the context", "reduce the length", "status 413", "exceed_context_size",
	"上下文超长", "超出上下文", "上下文长度超",
}

// IsContextOverflow 判断错误是不是「请求超出上下文窗口」。
//
// 这类错误重试同一份请求必然还失败，必须靠压缩而不是重试——所以它被判为 VerdictFail。
func IsContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, h := range contextOverflowHints {
		if strings.Contains(s, h) {
			return true
		}
	}
	var apiErr interface{ StatusCode() int }
	if errors.As(err, &apiErr) && apiErr.StatusCode() == 413 {
		return true
	}
	return false
}

// Classify 用**当前生效**的词表判定一次失败的处置方式。
//
// 词表是进程级的（见 SetRules）：设置页改了分类关键词后立即对所有判定路径生效，
// 不需要重建 provider 或池子。
func Classify(err error) Verdict {
	return ClassifyWith(err, CurrentRules())
}

// ClassifyWith 用给定词表判定一次失败的处置方式。
//
// 判定顺序（每一步都可能直接返回，所以顺序本身就是优先级）：
//
//  1. 不是错误 → None
//  2. 调用被取消 → Fail（用户停的任务，换端点只是白烧额度）
//  3. 响应质量类 → Quality（llmrepair 自己有修复与换端点的口径）
//  4. 上下文超长 → Fail
//  5. **文案语义**（先于状态码）：余额耗尽 / 封号鉴权 / 上游不可用 → Switch；
//     限流与繁忙 → Retry
//  6. 状态码：400/413/422 → Fail；401/402/403/404 → Switch；408/429/5xx/529 → Retry
//  7. 没有状态码：传输层故障（net.Error）→ Retry；其余（响应解码类）→ Switch
func ClassifyWith(err error, rules Rules) Verdict {
	if err == nil {
		return VerdictNone
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return VerdictFail
	}
	if llmrepair.IsQualityError(err) {
		return VerdictQuality
	}
	if IsContextOverflow(err) {
		// 溢出单独成一档：上游健康，只是这条请求对当前窗口太长。
		// 判成 VerdictFail 会把"换个大窗口重试"这条唯一正确的出路一起堵死。
		return VerdictOverflow
	}

	retryKW, switchKW, failKW := rules.effective()
	msg := strings.ToLower(err.Error())

	// 文案语义优先于状态码：中转站常用 429 表达余额不足、用 200 表达「系统繁忙」，
	// 只看状态码会把「该立刻换端点」误判成「原地重试 5 次」。
	switch {
	case IsQuotaExhausted(msg):
		return VerdictSwitch
	case containsAny(msg, failKW):
		return VerdictFail
	case containsAny(msg, switchKW):
		return VerdictSwitch
	// quotaTransientLimit 必须在这里接住：IsQuotaExhausted 用它把「其实是速率限制
	// 的 quota 措辞」排除在「额度耗尽」之外，但排除之后若没人认领，这类文案会一路
	// 掉到下面的状态码分支——而它们往往没有状态码（供应商直接把限流写进错误文案），
	// 于是被 code==0 的默认分支判成 Switch，正好把「本该留在当前端点重试」的故障
	// 变成「把用户踢到另一家供应商」。与 IsQuotaExhausted 共用同一条正则，是为了
	// 让「不算额度耗尽」和「算速率限制」永远是同一个口径，不会各改一处而漂移。
	case containsAny(msg, retryKW), quotaTransientLimit.MatchString(msg):
		return VerdictRetry
	}

	switch code := StatusOf(err); {
	case code == 0:
		// 没走到 HTTP 响应。传输层故障（连接重置 / 超时 / EOF）原地重试有意义；
		// 响应解码类错误原地重试没意义，但换个供应商可能能解（各家 SSE 格式不同），
		// 所以判 Switch——与既有 failover 口径一致。
		var netErr net.Error
		if errors.As(err, &netErr) {
			return VerdictRetry
		}
		return VerdictSwitch
	case code == 400, code == 413, code == 422:
		return VerdictFail
	case code == 401, code == 402, code == 403, code == 404:
		return VerdictSwitch
	case code == 408, code == 429:
		return VerdictRetry
	case code >= 500, code == 529:
		return VerdictRetry
	}
	return VerdictFail
}

// containsAny 报告小写文案里是否命中任一关键词。
func containsAny(msg string, keywords []string) bool {
	for _, k := range keywords {
		if k != "" && strings.Contains(msg, k) {
			return true
		}
	}
	return false
}
