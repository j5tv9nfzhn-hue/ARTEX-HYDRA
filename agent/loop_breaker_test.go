package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
)

func TestLoopBreakerGatesBeforeFourthSameContextCall(t *testing.T) {
	lb := newLoopBreaker()
	in := json.RawMessage(`{"url":"https://example.invalid/api/123?q=one","method":"GET"}`)
	for i := 0; i < 3; i++ {
		release, blocked := lb.before(context.Background(), "http_request", in)
		if blocked != nil {
			t.Fatal("premature block")
		}
		result := actool.Text(`{"status":422,"body":"rejected","body_truncated":false}`)
		result.Extra = []llm.Message{llm.UserText("preserve guidance")}
		result.Content = append(result.Content, llm.ContentBlock{Type: llm.BlockThinking, Thinking: "preserve non-text"})
		result = lb.inspect(context.Background(), "http_request", in, result, nil)
		release()
		if len(result.Extra) != 1 || result.Content[1].Type != llm.BlockThinking {
			t.Fatal("result structure lost")
		}
	}
	release, blocked := lb.before(context.Background(), "http_request", json.RawMessage(`{"url":"https://example.invalid/api/123?q=one"}`))
	release()
	if blocked == nil || !blocked.IsError || !strings.Contains(blocked.Flatten(), "未执行外部调用") {
		t.Fatal("fourth request not blocked")
	}
	for _, other := range []json.RawMessage{
		json.RawMessage(`{"url":"https://example.invalid/api/123?q=two"}`),
		json.RawMessage(`{"url":"https://example.invalid/api/124?q=two"}`),
		json.RawMessage(`{"url":"https://example.invalid/api/123?q=two","headers":{"Authorization":"Bearer other"}}`),
	} {
		release, blocked = lb.before(context.Background(), "http_request", other)
		release()
		if blocked != nil {
			t.Fatal("unrelated context blocked")
		}
	}
}

func TestLoopBreakerDoesNotInterpretOpaqueOrTruncatedOutput(t *testing.T) {
	lb := newLoopBreaker()
	in := json.RawMessage(`{"url":"https://example.invalid/api"}`)
	for i := 0; i < 5; i++ {
		release, blocked := lb.before(context.Background(), "http_request", in)
		if blocked != nil {
			t.Fatal("truncated result tripped gate")
		}
		lb.inspect(context.Background(), "http_request", in, actool.Text(`{"status":500,"body":"partial","body_truncated":true}`), nil)
		release()
	}
	if loopTargetKey("Bash", json.RawMessage(`{"command":"echo 422"}`)) != "" {
		t.Fatal("opaque command must not be a hard-gate target")
	}
	if loopTargetKey("http_fuzz", json.RawMessage(`{"url_template":"https://example.invalid/{{FUZZ}}"}`)) == "" {
		t.Fatal("real fuzz schema not recognized")
	}
}

func TestLoopBreakerExactCallGateCoversStructuredTools(t *testing.T) {
	in := json.RawMessage(`{"mode":"bola","url":"https://example.invalid/api/orders/1","method":"GET","timeout_ms":1000}`)
	same := json.RawMessage(`{"timeout_ms":2000,"method":"GET","url":"https://example.invalid/api/orders/1","mode":"bola"}`)
	if loopCallKey("http_sequence", in) == "" || loopCallKey("http_sequence", in) != loopCallKey("http_sequence", same) {
		t.Fatal("字段顺序/超时不同不应绕过精确调用键")
	}
	if loopCallKey("Bash", json.RawMessage(`{"command":"echo 422"}`)) != "" {
		t.Fatal("Bash 是控制面恢复手段，不进入精确调用止损")
	}

	lb := newLoopBreaker()
	for i := 0; i < loopStrikeLimit; i++ {
		release, blocked := lb.before(context.Background(), "http_sequence", in)
		if blocked != nil {
			t.Fatalf("第 %d 次就提前拦截", i+1)
		}
		lb.inspect(context.Background(), "http_sequence", in, actool.Errorf("upstream unavailable"), nil)
		release()
	}
	release, blocked := lb.before(context.Background(), "http_sequence", same)
	release()
	if blocked == nil || !blocked.IsError {
		t.Fatal("第 4 次完全相同的失败调用应被拦截")
	}
	release, blocked = lb.before(context.Background(), "http_sequence", json.RawMessage(`{"mode":"bola","url":"https://example.invalid/api/orders/2","method":"GET"}`))
	release()
	if blocked != nil {
		t.Fatal("不同入参不应被精确调用键误拦")
	}
}

func TestLoopBreakerExactCallGateCountsCallErrors(t *testing.T) {
	in := json.RawMessage(`{"target":"https://example.invalid/mcp"}`)
	lb := newLoopBreaker()
	for i := 0; i < loopStrikeLimit; i++ {
		release, blocked := lb.before(context.Background(), "mcp_remote_probe", in)
		if blocked != nil {
			t.Fatalf("第 %d 次就提前拦截", i+1)
		}
		lb.inspect(context.Background(), "mcp_remote_probe", in, actool.Result{}, errors.New("dial timeout"))
		release()
	}
	release, blocked := lb.before(context.Background(), "mcp_remote_probe", in)
	release()
	if blocked == nil || !blocked.IsError {
		t.Fatal("同入参连续 Call error 后第 4 次应被拦截")
	}
}

func TestLoopTargetLabelFindsNestedSequenceURL(t *testing.T) {
	var in any
	if err := json.Unmarshal([]byte(`{"mode":"chain","steps":[{"url":"https://api.example.test/api/orders?token=secret"}]}`), &in); err != nil {
		t.Fatal(err)
	}
	got := loopTargetLabel("http_sequence", in)
	if got != "http_sequence https://api.example.test/api/orders" {
		t.Fatalf("嵌套 URL 标签不对（且不得带查询值）：%q", got)
	}
}

func TestSkillHistoryRestoresOnlyPairedSuccess(t *testing.T) {
	msgs := []llm.Message{
		{Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: "ok", Name: "Skill", Input: json.RawMessage(`{"skill":"api"}`)}, {Type: llm.BlockToolUse, ID: "bad", Name: "Skill", Input: json.RawMessage(`{"name":"camoufox"}`)}, {Type: llm.BlockToolUse, ID: "pending", Name: "Skill", Input: json.RawMessage(`{"name":"expert-triage-pivot"}`)}}},
		{Content: []llm.ContentBlock{llm.ToolResultText("ok", "loaded", false), llm.ToolResultText("bad", "failed", true)}},
	}
	var got []string
	seedUnlockFromHistory(msgs, func(name string) { got = append(got, CanonicalSkillAlias(name)) })
	// 三条 tool_use 里只有 ok 有对应的成功 tool_result：
	//   ok      → 恢复
	//   bad     → 结果是 error，不恢复
	//   pending → 根本没有结果（这一轮被打断），不恢复
	//
	// 断言的是「恰好恢复一条，且这条是 'api' 对应的规范名」，而不是把规范名
	// 写死成 "api-recon" —— 别名表会随技能库改名（现在 api → performing-api-
	// inventory-and-discovery），把下游映射结果钉在测试里会让每次改名都变红，
	// 而它根本不是这条测试要守的行为。
	if len(got) != 1 {
		t.Fatalf("只应恢复成功且已回填的那一条，实际恢复 %d 条：%v", len(got), got)
	}
	if want := CanonicalSkillAlias("api"); got[0] != want {
		t.Fatalf("恢复的技能 %q 与 'api' 的规范名 %q 不一致", got[0], want)
	}
	// 另外两个绝不能出现：它们的 tool_result 分别是失败和缺失。
	for _, bad := range []string{CanonicalSkillAlias("camoufox"), CanonicalSkillAlias("expert-triage-pivot")} {
		if strings.Contains(strings.Join(got, ","), bad) {
			t.Fatalf("失败的/未回填的技能被恢复了：%v", got)
		}
	}
}

func TestFuzzOutcomeRequiresActualCompleteCounters(t *testing.T) {
	for _, raw := range []string{`{"total_tested":1}`, `{"hits_count":-1,"total_tested":1,"error_count":0}`} {
		if sig, _ := classifyNegativeOutcome("http_fuzz", actool.Text(raw)); sig != "" {
			t.Fatal("incomplete counters classified as negative outcome")
		}
	}
	for _, raw := range []string{`{"hits_count":0,"total_tested":2,"error_count":0}`, `{"hits_count":0,"total_tested":2,"error_count":2}`} {
		if sig, _ := classifyNegativeOutcome("http_fuzz", actool.Text(raw)); sig == "" {
			t.Fatal("actual negative fuzz counters not classified")
		}
	}
}
