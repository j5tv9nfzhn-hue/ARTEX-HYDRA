// Package dedupe 实现漏洞报告的判重：一条候选发现是不是已有发现的同一个漏洞。
//
// 移植自 Strix：strix/report/dedupe.py（check_duplicate、DEDUPE_SYSTEM_PROMPT、
// _prepare_report_for_comparison、_parse_dedupe_response）。
//
// 判重是「落库前」的一道闸门：判为重复就不落库，模型会收到「疑似重复，别再报」的
// 明确回执——这与 Strix 的口径一致（那边是丢弃候选并回一条 duplicate 错误）。
// 判重失败一律 fail-open（放行）：把一条真发现挡在门外，代价远大于多留一条重复。
package dedupe

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/norma/llm"
)

// 载荷与结论的长度上限，移植自 Strix 的解析侧截断。
const (
	maxDuplicateIDChars = 64
	maxReasonChars      = 500
	maxFieldChars       = 8000
)

// Candidate 是一条参与比对的发现。
//
// 字段名沿用 Strix 的比较载荷（title / description / impact / target /
// technical_analysis / poc_description），取值来自 ARTEX 的发现字段：
//
//	Title             ← findings.name（漏洞名称）
//	Description       ← findings.summary（发现摘要，根因描述在这里）
//	Impact            ← severity + CVSS 分
//	Target            ← 受影响资产 id
//	TechnicalAnalysis ← findings.counterevidence（反证：这条可能不成立的地方）
//	PoC               ← findings.evidence（证据 / PoC）
//
// ARTEX 的发现模型里没有 Strix 的 endpoint / method / cve / dependency_metadata，
// 因此载荷里也没有这些字段——没有的东西不能凭空编一个字段名给模型看。
type Candidate struct {
	ID                int64  `json:"id"`
	Title             string `json:"title"`
	Description       string `json:"description"`
	Impact            string `json:"impact"`
	Target            string `json:"target"`
	TechnicalAnalysis string `json:"technical_analysis"`
	PoC               string `json:"poc_description"`
}

// Result 是一次判重的结论。
//
// Error 非空表示这次判重没做成（模型不可用、超时、返回的不是 JSON）。此时
// IsDuplicate 恒为 false，语义是「没有证据说它是重复」，也就是放行。
type Result struct {
	IsDuplicate bool    `json:"is_duplicate"`
	DuplicateID string  `json:"duplicate_id"`
	Confidence  float64 `json:"confidence"`
	Reason      string  `json:"reason"`
	Error       string  `json:"error,omitempty"`
}

// SystemPrompt 是判重裁判的系统提示词，移植自 Strix 的 DEDUPE_SYSTEM_PROMPT。
//
// 规则 1–3 与比较准则逐字照抄：这是判重的判断标准，翻译或改写都会让结论漂移。
// Strix 的规则 4（依赖 CVE 的包身份比对）没有移植——ARTEX 的发现模型里没有
// 「软件包 + CVE」这种对象，那条规则在这里无的放矢，留着只会让模型去找不存在的字段。
const SystemPrompt = `You are an expert vulnerability report deduplication judge.
Your task is to determine if a candidate vulnerability report describes the SAME vulnerability
as any existing report.

CRITICAL DEDUPLICATION RULES:

1. SAME VULNERABILITY means:
   - Same root cause (e.g., "missing input validation" not just "SQL injection")
   - Same affected component/endpoint/file (exact match or clear overlap)
   - Same exploitation method or attack vector
   - Would be fixed by the same code change/patch

2. NOT DUPLICATES if:
   - Different endpoints even with same vulnerability type (e.g., SQLi in /login vs /search)
   - Different parameters in same endpoint (e.g., XSS in 'name' vs 'comment' field)
   - Different root causes (e.g., stored XSS vs reflected XSS in same field)
   - Different severity levels due to different impact
   - One is authenticated, other is unauthenticated

3. ARE DUPLICATES even if:
   - Titles are worded differently
   - Descriptions have different level of detail
   - PoC uses different payloads but exploits same issue
   - One report is more thorough than another
   - Minor variations in technical analysis

COMPARISON GUIDELINES:
- Focus on the technical root cause, not surface-level similarities
- Same vulnerability type (SQLi, XSS) doesn't mean duplicate - location matters
- Consider the fix: would fixing one also fix the other?
- When uncertain, lean towards NOT duplicate

FIELDS TO ANALYZE:
- title, description: General vulnerability info
- impact: Severity and CVSS score
- target: Affected asset ids
- technical_analysis: Root cause details and what may invalidate the finding
- poc_description: How it's exploited

Respond with a single JSON object and nothing else:

{
  "is_duplicate": true,
  "duplicate_id": "12",
  "confidence": 0.95,
  "reason": "Both reports describe SQL injection in /api/login via the username parameter"
}

Or, if not a duplicate:

{
  "is_duplicate": false,
  "duplicate_id": "",
  "confidence": 0.90,
  "reason": "Different endpoints: candidate is /api/search, existing is /api/login"
}

Rules:
- ` + "`is_duplicate`" + ` is a boolean.
- ` + "`duplicate_id`" + ` is the exact id from existing reports, or "" if not a duplicate.
- ` + "`confidence`" + ` is a number between 0 and 1.
- ` + "`reason`" + ` is a specific explanation mentioning endpoint/parameter/root cause.
- Output ONLY the JSON object — no surrounding prose, no code fences.`

// Judge 用一次模型调用做判重。
//
// Provider 与 Model 由宿主注入（和 server/intercept.go 的判定模型同构）：判重可以
// 用主模型，也可以用另一个专用配置——专用配置不会继承主模型的凭据与端点。
type Judge struct {
	Provider llm.Provider
	Model    string
	// Timeout 是这一次判重的上限。默认由调用方给；判重是上报链路上的旁路，
	// 不该把上报卡住。
	Timeout time.Duration
}

// Check 判断候选发现是否与已有发现重复。
//
// 口径与 Strix 的 check_duplicate 一致：
//   - 已有集合为空 → 直接判「不重复」，不调模型（第一条发现没有可比的，调用模型
//     只会白花一次配额）；
//   - 模型调用失败、返回空、或返回的不是 JSON → fail-open：判为不重复，把原因挂
//     在 Error 上并返回 error，由调用方决定放行。Strix 那边同样是 fail-open。
func (j *Judge) Check(ctx context.Context, candidate Candidate, existing []Candidate) (Result, error) {
	if len(existing) == 0 {
		return Result{IsDuplicate: false, Confidence: 1.0}, nil
	}
	if j == nil || j.Provider == nil {
		return Result{IsDuplicate: false}, fmt.Errorf("dedupe: no provider configured")
	}
	payload, err := BuildPayload(candidate, existing)
	if err != nil {
		return Result{IsDuplicate: false}, err
	}
	if j.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, j.Timeout)
		defer cancel()
	}
	temp := 0.0
	req := llm.CompletionRequest{
		System:      []string{SystemPrompt},
		Messages:    []llm.Message{llm.UserText(string(payload))},
		MaxTokens:   1024,
		Temperature: &temp,
		Thinking:    "disabled",
	}
	msg, _, _, err := j.Provider.Complete(ctx, req)
	if err != nil {
		return j.fail(err), err
	}
	text := msg.Text()
	if strings.TrimSpace(text) == "" {
		return j.fail(fmt.Errorf("empty response")), fmt.Errorf("dedupe: empty response")
	}
	result, err := ParseVerdict(text)
	if err != nil {
		return j.fail(err), err
	}
	return result, nil
}

// fail 造出 fail-open 的结论：不是「判定为不重复」，而是「没判成」。
func (j *Judge) fail(err error) Result {
	return Result{
		IsDuplicate: false,
		Confidence:  0,
		Reason:      "Deduplication check failed: " + err.Error(),
		Error:       err.Error(),
	}
}

// BuildPayload 构造送进模型的载荷：{"candidate": …, "existing_reports": […]}。
//
// 一次调用把所有已有报告都放进去（Strix 同口径：不分批、不逐条比对）。已有报告
// 很多时载荷会变大，但拆批会让「同一条漏洞被拆到两批里各判一次不重复」——漏判
// 比多花点 token 贵得多。
func BuildPayload(candidate Candidate, existing []Candidate) ([]byte, error) {
	payload := struct {
		Candidate       map[string]any   `json:"candidate"`
		ExistingReports []map[string]any `json:"existing_reports"`
	}{
		Candidate:       prepare(candidate),
		ExistingReports: make([]map[string]any, 0, len(existing)),
	}
	for _, item := range existing {
		payload.ExistingReports = append(payload.ExistingReports, prepare(item))
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// prepare 把一条发现整理成比较载荷：只留非空字段，超长文本截断。
//
// 移植自 Strix 的 _prepare_report_for_comparison（那里只保留 truthy 值，字符串
// 超过 8000 截断）。id 统一渲染成字符串：模型要能逐字把它抄回 duplicate_id。
func prepare(c Candidate) map[string]any {
	out := map[string]any{}
	if c.ID > 0 {
		out["id"] = strconv.FormatInt(c.ID, 10)
	}
	put := func(key, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		out[key] = clip(value)
	}
	put("title", c.Title)
	put("description", c.Description)
	put("impact", c.Impact)
	put("target", c.Target)
	put("technical_analysis", c.TechnicalAnalysis)
	put("poc_description", c.PoC)
	return out
}

// clip 截断超长文本，移植自 Strix 的截断口径。
func clip(s string) string {
	if len(s) <= maxFieldChars {
		return s
	}
	return s[:maxFieldChars] + "...[truncated]"
}

// ParseVerdict 解析模型回的 JSON 结论，移植自 Strix 的 _parse_dedupe_response。
//
// 容错三步：剥 ``` 围栏 → 取首个 { 到末个 } → json 解析。解析不出来就是判重失败
// （fail-open），不是「判为重复」。
func ParseVerdict(text string) (Result, error) {
	cleaned := stripFence(text)
	start := strings.Index(cleaned, "{")
	end := strings.LastIndex(cleaned, "}")
	if start < 0 || end < start {
		return Result{}, fmt.Errorf("dedupe: no JSON object found in response")
	}
	var raw struct {
		IsDuplicate json.RawMessage `json:"is_duplicate"`
		DuplicateID string          `json:"duplicate_id"`
		Confidence  json.RawMessage `json:"confidence"`
		Reason      string          `json:"reason"`
	}
	if err := json.Unmarshal([]byte(cleaned[start:end+1]), &raw); err != nil {
		return Result{}, fmt.Errorf("dedupe: invalid verdict JSON: %w", err)
	}
	result := Result{
		IsDuplicate: truthy(raw.IsDuplicate),
		DuplicateID: clipTo(raw.DuplicateID, maxDuplicateIDChars),
		Confidence:  number(raw.Confidence),
		Reason:      clipTo(raw.Reason, maxReasonChars),
	}
	if result.IsDuplicate && result.DuplicateID == "" {
		// 判为重复却没指出是哪一条：这条结论没法用，按失败处理（fail-open）。
		return Result{}, fmt.Errorf("dedupe: verdict says duplicate but has no duplicate_id")
	}
	return result, nil
}

// stripFence 剥掉模型可能加上的 ``` 围栏。
func stripFence(text string) string {
	cleaned := strings.TrimSpace(text)
	if !strings.HasPrefix(cleaned, "```") {
		return cleaned
	}
	cleaned = strings.TrimPrefix(cleaned, "```")
	if idx := strings.Index(cleaned, "\n"); idx >= 0 {
		// 第一行可能是 ```json 这种语言标记，丢掉它。
		cleaned = cleaned[idx+1:]
	}
	cleaned = strings.TrimSuffix(strings.TrimSpace(cleaned), "```")
	return strings.TrimSpace(cleaned)
}

// truthy 按 Python 的 bool() 语义解读模型给的 is_duplicate。
//
// Strix 那边写的是 bool(parsed.get("is_duplicate"))，于是字符串 "false" 会被当成
// True——模型偶尔会回字符串。这里按 Python 的真值口径复刻（空串 / "false" / 0 为
// 假，其余为真），既不改变 Strix 的行为，也顺手把最常见的那个误判修掉。
func truthy(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "", "false", "0", "no", "null":
			return false
		}
		return true
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n != 0
	}
	return true
}

// number 解析 confidence；解析不出来取 0.0（Strix 同口径）。
func number(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return f
		}
	}
	return 0
}

// clipTo 按字符数截断字符串。
func clipTo(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}
