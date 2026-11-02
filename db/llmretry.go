package db

import (
	"encoding/json"
	"time"

	"github.com/Autumn-27/artex/llmclass"
)

// LLM 重试策略：五层重试的「次数 + 间隔」全局配置，见 docs/LLM重试设计.md。
// 存在 settings 表的一个 JSON 值里 —— 它是整机一份的运行参数，不值得为它开一张表；
// 读取走内置默认兜底，所以键不存在(全新库/从未配置过)时行为与写死常量时代完全一致。

const settingLLMRetryPolicy = "llm_retry_policy"

// RetryRule is one layer's knob pair. The zero value means "unset":
//
//	Attempts   0 = 用内置默认次数; -1 = 关闭该层重试; >0 = 用该值
//	IntervalMS 0 = 用该层原本的间隔策略(通常是指数退避); >0 = 改用固定毫秒间隔
//
// -1 是「显式关掉」而不是「0 次」，因为 0 已经被「未配置」占用了。
type RetryRule struct {
	Attempts   int `json:"attempts"`
	IntervalMS int `json:"interval_ms"`
	// 指数梯度的三个旋钮（只有建连层消费，其余层留空即用各自默认）：
	//   InitialMS  起始延迟（0 = 用内置默认 2000）
	//   Multiplier 每次翻倍的倍数（0 = 用内置默认 2）
	//   MaxMS      封顶（0 = 用内置默认 90000，与 Strix 的 DEFAULT_MODEL_RETRY 一致）
	// 之所以放进这一层而不是新建一个结构：梯度与次数、间隔一样，都是「这一层重试
	// 怎么等」的一部分，跟着端点走、也能被单个配置逐字段覆盖。
	InitialMS  int `json:"initial_ms,omitempty"`
	Multiplier int `json:"multiplier,omitempty"`
	MaxMS      int `json:"max_ms,omitempty"`
}

// Interval returns the configured fixed interval, or 0 when unset (caller keeps
// its own default ladder).
func (r RetryRule) Interval() time.Duration {
	if r.IntervalMS <= 0 {
		return 0
	}
	return time.Duration(r.IntervalMS) * time.Millisecond
}

// Or returns the rule with each unset field filled in from fallback. Used to
// layer a profile override on top of the global policy field by field, so a
// profile that only pins the interval still inherits the global count.
func (r RetryRule) Or(fallback RetryRule) RetryRule {
	if r.Attempts == 0 {
		r.Attempts = fallback.Attempts
	}
	if r.IntervalMS == 0 {
		r.IntervalMS = fallback.IntervalMS
	}
	if r.InitialMS == 0 {
		r.InitialMS = fallback.InitialMS
	}
	if r.Multiplier == 0 {
		r.Multiplier = fallback.Multiplier
	}
	if r.MaxMS == 0 {
		r.MaxMS = fallback.MaxMS
	}
	return r
}

// Ladder 返回该层配置的指数梯度参数；未配置的项返回 0，由调用方套用自己的默认。
//
// 三项分开返回而不是一个打包结构：调用方（agent 的建连重试层）才知道自己的默认
// 是多少，db 不该替它决定。
func (r RetryRule) Ladder() (initialMS, multiplier, maxMS int) {
	return r.InitialMS, r.Multiplier, r.MaxMS
}

// retry knob bounds. A count above the cap turns a blip into a token bonfire;
// an interval above an hour outlives any transient failure worth waiting out.
const (
	maxRetryAttempts   = 20
	maxRetryIntervalMS = 3600_000 // 1h
	// maxRetryMultiplier 兜住「手滑填了 1000」——那会让第二次重试就等到天荒地老。
	maxRetryMultiplier = 10
	// minRetryMultiplier 让「不翻倍」也有地方表达（用 IntervalMS 更直接，但有人
	// 就是想只调倍数）。
	minRetryMultiplier = 1
)

// Clamped returns the rule with out-of-range values pulled back into the sane
// band (attempts within [-1, 20], interval within [0, 1h], ladder sane).
func (r RetryRule) Clamped() RetryRule {
	if r.Attempts < -1 {
		r.Attempts = -1
	}
	if r.Attempts > maxRetryAttempts {
		r.Attempts = maxRetryAttempts
	}
	if r.IntervalMS < 0 {
		r.IntervalMS = 0
	}
	if r.IntervalMS > maxRetryIntervalMS {
		r.IntervalMS = maxRetryIntervalMS
	}
	if r.InitialMS < 0 {
		r.InitialMS = 0
	}
	if r.InitialMS > maxRetryIntervalMS {
		r.InitialMS = maxRetryIntervalMS
	}
	if r.Multiplier < minRetryMultiplier {
		r.Multiplier = 0 // 0 = 未配置；1 以上的值才生效
	}
	if r.Multiplier > maxRetryMultiplier {
		r.Multiplier = maxRetryMultiplier
	}
	if r.MaxMS < 0 {
		r.MaxMS = 0
	}
	if r.MaxMS > maxRetryIntervalMS {
		r.MaxMS = maxRetryIntervalMS
	}
	return r
}

// Clamped bounds a profile's override the same way the global policy is bounded,
// so a hand-crafted API payload can't land a value the CHECK constraint rejects.
func (o RetryOverride) Clamped() RetryOverride {
	o.Connect, o.Empty, o.Stream = o.Connect.Clamped(), o.Empty.Clamped(), o.Stream.Clamped()
	return o
}

// LLMRetryPolicy holds the五层 retry configuration. Connect/Empty/Stream are the
// per-request layers (a profile may override them, see LLMProfile.Retry);
// Breaker and Intent are process-wide by nature and live only here.
type LLMRetryPolicy struct {
	// Connect：SDK 建连重试(连接重置/超时/429/5xx，流开始前)。默认 5 次(对齐 Strix 的
	// DEFAULT_MODEL_RETRY，见 agent.defaultConnectAttempts)、指数退避。
	Connect RetryRule `json:"connect"`
	// Empty：SDK 空响应重试(完成但无 content block，仅 openai 格式)。默认 2 次、指数退避。
	Empty RetryRule `json:"empty"`
	// Stream：同 provider 安全窗口重试(未交付输出前的断流重放)。默认 2 次、0.5s 起指数(封顶 4s)。
	Stream RetryRule `json:"stream"`
	// Breaker：轮询熔断。Attempts=连续几次瞬时失败触发熔断(默认 3，-1=瞬时失败不熔断，
	// 硬失败如余额不足/密钥失效仍立即熔断)；IntervalMS=固定冷却时长(0=默认 1/5/30min 梯度)。
	Breaker RetryRule `json:"breaker"`
	// Intent：worker 以 model_error 收场后的整条意图重跑。默认 4 次、3s 起指数退避
	// (封顶 30s，硬失败默认 2 次)；IntervalMS>0 时改用该固定间隔，语义与原实现一致。
	Intent RetryRule `json:"intent"`
}

// Clamped returns the policy with every rule clamped.
func (p LLMRetryPolicy) Clamped() LLMRetryPolicy {
	p.Connect, p.Empty, p.Stream = p.Connect.Clamped(), p.Empty.Clamped(), p.Stream.Clamped()
	p.Breaker, p.Intent = p.Breaker.Clamped(), p.Intent.Clamped()
	return p
}

// LLMRetryPolicy reads the global retry policy. A missing or unparseable value
// yields the zero policy — i.e. every layer on its built-in default.
func (d *DB) LLMRetryPolicy() LLMRetryPolicy {
	var p LLMRetryPolicy
	if d == nil {
		return p
	}
	raw, ok, err := d.GetSetting(settingLLMRetryPolicy)
	if err != nil || !ok || raw == "" {
		return p
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return LLMRetryPolicy{}
	}
	return p.Clamped()
}

// SetLLMRetryPolicy persists the global retry policy (values are clamped first).
func (d *DB) SetLLMRetryPolicy(p LLMRetryPolicy) error {
	raw, err := json.Marshal(p.Clamped())
	if err != nil {
		return err
	}
	return d.SetSetting(settingLLMRetryPolicy, string(raw))
}

// settingLLMErrorRules 存人工增补的错误分类关键词（见 llmclass.Rules）。
//
// 存 settings 表的一格 JSON：它是「接上没见过的供应商措辞」用的少量词，不值得为它
// 开一张表；键不存在（全新库 / 从未配置过）时行为与写死常量时代完全一致。
const settingLLMErrorRules = "llm_error_rules"

// LLMErrorRules 读人工增补的分类关键词。
//
// 缺失或解析失败一律回落到空规则（= 用 llmclass 的内置词表）：解析失败不该让系统
// 失去判定能力，退回内置词表仍然能工作。
func (d *DB) LLMErrorRules() llmclass.Rules {
	var r llmclass.Rules
	if d == nil {
		return r
	}
	raw, ok, err := d.GetSetting(settingLLMErrorRules)
	if err != nil || !ok || raw == "" {
		return r
	}
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return llmclass.Rules{}
	}
	return r
}

// SetLLMErrorRules 保存人工增补的分类关键词。
func (d *DB) SetLLMErrorRules(r llmclass.Rules) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return d.SetSetting(settingLLMErrorRules, string(raw))
}
