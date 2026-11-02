package dedupe

// 判重的回归测试。
//
// 判重有两个「错了也不报错」的方向，测试要同时钉住：
//   - 把不重复的判成重复 → 真发现被挡在门外，模型白忙一轮；
//   - 把重复的判成不重复 → 重复条目灌满库，报告里同一件事出现三遍。
// 另外还要钉住 fail-open：判重失败必须放行，不能把上报卡死。

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

// fakeProvider 是判重用的假模型：按脚本回一句话。
type fakeProvider struct {
	reply string
	err   error
	calls int
}

func (p *fakeProvider) Stream(_ context.Context, _ llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		yield(llm.StreamEvent{Type: llm.SETextDelta, Text: p.reply}, nil)
	}
}

func (p *fakeProvider) Complete(_ context.Context, _ llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	p.calls++
	if p.err != nil {
		return llm.Message{}, "", llm.Usage{}, p.err
	}
	return llm.UserText(p.reply), "", llm.Usage{}, nil
}

// TestParseVerdictAcceptsFencedAndProse 断言三种常见回包都能解析。
func TestParseVerdictAcceptsFencedAndProse(t *testing.T) {
	cases := []string{
		`{"is_duplicate": true, "duplicate_id": "12", "confidence": 0.95, "reason": "same sql injection"}`,
		"```json\n{\"is_duplicate\": true, \"duplicate_id\": \"12\", \"confidence\": 0.95, \"reason\": \"same sql injection\"}\n```",
		"Here is my verdict:\n{\"is_duplicate\": true, \"duplicate_id\": \"12\", \"confidence\": 0.95, \"reason\": \"same sql injection\"}\nDone.",
	}
	for _, text := range cases {
		result, err := ParseVerdict(text)
		if err != nil {
			t.Fatalf("解析失败：%v（原文 %q）", err, text)
		}
		if !result.IsDuplicate {
			t.Fatalf("应判为重复：%q", text)
		}
		if result.DuplicateID != "12" {
			t.Fatalf("duplicate_id 应为 12，实得 %q", result.DuplicateID)
		}
		if result.Confidence < 0.94 || result.Confidence > 0.96 {
			t.Fatalf("confidence 应为 0.95，实得 %v", result.Confidence)
		}
	}
}

// TestParseVerdictRejectsGarbage 断言解析不出来就是失败（而不是「不重复」也不是「重复」）。
func TestParseVerdictRejectsGarbage(t *testing.T) {
	if _, err := ParseVerdict("I could not determine whether these are the same."); err == nil {
		t.Fatal("没有 JSON 的回包应当解析失败")
	}
	if _, err := ParseVerdict(`{"is_duplicate": "not-a-json"`); err == nil {
		t.Fatal("残缺的 JSON 应当解析失败")
	}
}

// TestParseVerdictStringBoolean 断言字符串布尔按 Python 真值口径解读。
//
// 模型偶尔把 false 写成字符串 "false"；Strix 那边 bool("false") 是 True，于是这条
// 判重会反过来把新发现当成重复。这里按真值口径判为 false。
func TestParseVerdictStringBoolean(t *testing.T) {
	result, err := ParseVerdict(`{"is_duplicate": "false", "duplicate_id": "", "confidence": 0.8, "reason": "different endpoint"}`)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if result.IsDuplicate {
		t.Fatal("字符串 \"false\" 应当判为不重复")
	}
}

// TestParseVerdictRequiresDuplicateID 断言「判为重复却没说是哪一条」按失败处理。
func TestParseVerdictRequiresDuplicateID(t *testing.T) {
	if _, err := ParseVerdict(`{"is_duplicate": true, "duplicate_id": "", "confidence": 0.9, "reason": "same"}`); err == nil {
		t.Fatal("缺 duplicate_id 的重复结论应当解析失败")
	}
}

// TestParseVerdictTruncatesReason 断言理由按 Strix 的口径截断。
func TestParseVerdictTruncatesReason(t *testing.T) {
	long := strings.Repeat("x", 900)
	result, err := ParseVerdict(`{"is_duplicate": false, "duplicate_id": "", "confidence": 0.5, "reason": "` + long + `"}`)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if len(result.Reason) != maxReasonChars {
		t.Fatalf("理由应截断到 %d 字，实得 %d", maxReasonChars, len(result.Reason))
	}
}

// TestBuildPayloadSkipsEmptyFields 断言载荷只带非空字段，且 id 渲染成字符串。
func TestBuildPayloadSkipsEmptyFields(t *testing.T) {
	payload, err := BuildPayload(
		Candidate{ID: 7, Title: "SQL 注入", Description: "参数未过滤"},
		[]Candidate{{ID: 3, Title: "XSS"}},
	)
	if err != nil {
		t.Fatalf("构造载荷失败：%v", err)
	}
	var decoded struct {
		Candidate       map[string]any   `json:"candidate"`
		ExistingReports []map[string]any `json:"existing_reports"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("载荷不是合法 JSON：%v", err)
	}
	if decoded.Candidate["id"] != "7" {
		t.Fatalf("候选 id 应为字符串 \"7\"，实得 %v", decoded.Candidate["id"])
	}
	if _, ok := decoded.Candidate["impact"]; ok {
		t.Fatal("空字段不该出现在载荷里")
	}
	if len(decoded.ExistingReports) != 1 || decoded.ExistingReports[0]["id"] != "3" {
		t.Fatalf("已有报告集合不对：%v", decoded.ExistingReports)
	}
}

// TestBuildPayloadTruncatesLongText 断言超长文本被截断。
func TestBuildPayloadTruncatesLongText(t *testing.T) {
	payload, err := BuildPayload(
		Candidate{ID: 1, Description: strings.Repeat("y", maxFieldChars+50)},
		[]Candidate{{ID: 2}},
	)
	if err != nil {
		t.Fatalf("构造载荷失败：%v", err)
	}
	if !strings.Contains(string(payload), "...[truncated]") {
		t.Fatal("超长文本应被截断并标记")
	}
}

// TestCheckWithoutExistingSkipsModel 断言没有可比对象时不调模型。
func TestCheckWithoutExistingSkipsModel(t *testing.T) {
	provider := &fakeProvider{reply: `{"is_duplicate": true, "duplicate_id": "1", "confidence": 1, "reason": "x"}`}
	judge := &Judge{Provider: provider}
	result, err := judge.Check(context.Background(), Candidate{ID: 1}, nil)
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if result.IsDuplicate {
		t.Fatal("没有可比对象时不该判为重复")
	}
	if provider.calls != 0 {
		t.Fatalf("没有可比对象时不该调模型，实调 %d 次", provider.calls)
	}
}

// TestCheckDuplicateVerdict 断言判为重复时结论完整。
func TestCheckDuplicateVerdict(t *testing.T) {
	provider := &fakeProvider{
		reply: `{"is_duplicate": true, "duplicate_id": "12", "confidence": 0.93, "reason": "same root cause"}`,
	}
	judge := &Judge{Provider: provider}
	result, err := judge.Check(context.Background(),
		Candidate{ID: 20, Title: "SQLi"},
		[]Candidate{{ID: 12, Title: "SQL 注入"}})
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if !result.IsDuplicate || result.DuplicateID != "12" {
		t.Fatalf("结论不对：%+v", result)
	}
	if provider.calls != 1 {
		t.Fatalf("应当只调一次模型，实调 %d 次", provider.calls)
	}
}

// TestCheckFailsOpen 断言判重失败时放行（fail-open）。
//
// 把一条真发现挡在门外的代价，远大于多留一条重复条目。
func TestCheckFailsOpen(t *testing.T) {
	provider := &fakeProvider{err: errors.New("provider down")}
	judge := &Judge{Provider: provider}
	result, err := judge.Check(context.Background(),
		Candidate{ID: 20, Title: "SQLi"},
		[]Candidate{{ID: 12, Title: "SQL 注入"}})
	if err == nil {
		t.Fatal("模型不可用应当返回错误")
	}
	if result.IsDuplicate {
		t.Fatal("判重失败时不该判为重复")
	}
	if result.Error == "" || !strings.Contains(result.Reason, "Deduplication check failed") {
		t.Fatalf("失败结论应当带上原因：%+v", result)
	}
}

// TestCheckFailsOpenOnUnparsableReply 断言回包不是 JSON 时也放行。
func TestCheckFailsOpenOnUnparsableReply(t *testing.T) {
	provider := &fakeProvider{reply: "looks the same to me"}
	judge := &Judge{Provider: provider}
	result, err := judge.Check(context.Background(),
		Candidate{ID: 20, Title: "SQLi"},
		[]Candidate{{ID: 12, Title: "SQL 注入"}})
	if err == nil {
		t.Fatal("回包不是 JSON 应当返回错误")
	}
	if result.IsDuplicate {
		t.Fatal("解析失败时不该判为重复")
	}
}
