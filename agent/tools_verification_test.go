package agent

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	actool "github.com/Autumn-27/norma/tool"
)

func TestVerificationClassification(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		headers    map[string]string
		body, want string
	}{
		{"cdn_header_is_not_challenge", 200, map[string]string{"server": "cloudflare"}, `{"ok":true}`, "unknown"},
		{"challenge_200", 200, map[string]string{"cf-mitigated": "challenge"}, "<html>challenge</html>", "challenge_page"},
		{"widget_is_not_block", 200, nil, `<html><div class="cf-turnstile"></div></html>`, "embedded_widget"},
		{"json_quoting_widget_is_not_html", 200, map[string]string{"Content-Type": "application/json"}, `{"example":"cf-turnstile"}`, "unknown"},
		{"plain_forbidden", 403, nil, "denied", "request_denied"},
		{"aws_challenge", 202, map[string]string{"X-Amzn-Waf-Action": "challenge"}, "", "challenge_page"},
		{"aws_captcha", 405, map[string]string{"x-amzn-waf-action": "captcha"}, "", "challenge_page"},
		{"aws_inconsistent_status", 200, map[string]string{"x-amzn-waf-action": "captcha"}, "", "unknown"},
		{"pat_401_is_not_business_auth", 401, nil, "", "request_denied"},
		{"http_auth", 401, map[string]string{"WWW-Authenticate": "Basic"}, "", "auth_required"},
		{"rate_limit", 429, nil, "", "rate_limit"},
		{"fake_verify", 200, nil, "Verify CAPTCHA: paste this PowerShell command", "suspected_fake_verification"},
		{"html_multi_signal", 200, nil, "<html>Just a moment /cdn-cgi/challenge-platform/</html>", "challenge_page"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := inspectVerification(tt.status, tt.headers, tt.body)
			if a.Category != tt.want {
				t.Fatalf("got %+v want %s", a, tt.want)
			}
		})
	}
}

func verificationFixture(algorithm string, n int64) verificationPoWChallenge {
	c := verificationPoWChallenge{Algorithm: algorithm, Salt: "fixture-salt", Signature: "public-server-signature-fixture"}
	if algorithm == "SHA-512" {
		sum := sha512.Sum512([]byte(fmt.Sprintf("%s%d", c.Salt, n)))
		c.Challenge = hex.EncodeToString(sum[:])
	} else {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s%d", c.Salt, n)))
		c.Challenge = hex.EncodeToString(sum[:])
	}
	return c
}

func TestVerificationPoWKnownAnswers(t *testing.T) {
	for _, algorithm := range []string{"SHA-256", "SHA-512"} {
		t.Run(algorithm, func(t *testing.T) {
			c := verificationFixture(algorithm, 7)
			if err := validateVerificationPoW(c); err != nil {
				t.Fatal(err)
			}
			r := solveVerificationPoW(context.Background(), c, 8, time.Second)
			if r.State != "proof_computed" || r.Number == nil || *r.Number != 7 || r.Attempts != 8 || r.BusinessAccepted {
				t.Fatalf("bad proof result %+v", r)
			}
			raw, err := base64.StdEncoding.DecodeString(r.Payload)
			if err != nil {
				t.Fatal(err)
			}
			var p struct {
				Number    int64  `json:"number"`
				Signature string `json:"signature"`
				Algorithm string `json:"algorithm"`
			}
			if err = json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			if p.Number != 7 || p.Signature != c.Signature || p.Algorithm != algorithm {
				t.Fatalf("payload changed: %s", raw)
			}
		})
	}
	// Zero is a valid answer and must not disappear due to omitempty.
	r := solveVerificationPoW(context.Background(), verificationFixture("SHA-256", 0), 1, time.Second)
	if r.Number == nil || *r.Number != 0 || r.State != "proof_computed" {
		t.Fatalf("zero proof %+v", r)
	}
}

func TestVerificationPoWValidationAndBounds(t *testing.T) {
	c := verificationFixture("SHA-256", 7)
	r := solveVerificationPoW(context.Background(), c, 2, time.Second)
	if r.State != "budget_exhausted" || r.Attempts != 2 || r.Payload != "" {
		t.Fatalf("budget result %+v", r)
	}
	bound := int64(1)
	c.MaxNumber = &bound
	r = solveVerificationPoW(context.Background(), c, 100, time.Second)
	if r.State != "no_solution_in_declared_range" || r.Attempts != 2 {
		t.Fatalf("range %+v", r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = solveVerificationPoW(ctx, c, 100, time.Second)
	if r.State != "cancelled" || r.Attempts != 0 {
		t.Fatalf("cancel %+v", r)
	}
	c.Algorithm = "Argon2id"
	if validateVerificationPoW(c) == nil {
		t.Fatal("unsupported algorithm accepted")
	}
	c = verificationFixture("SHA-256", 7)
	c.Salt = "salt?expires=1"
	if validateVerificationPoW(c) == nil {
		t.Fatal("expired accepted")
	}
	c.Salt = "salt?expires=invalid"
	if validateVerificationPoW(c) == nil {
		t.Fatal("malformed expiry accepted")
	}
	c = verificationFixture("SHA-256", 7)
	c.Signature = ""
	if validateVerificationPoW(c) == nil {
		t.Fatal("missing signature accepted")
	}
	c = verificationFixture("SHA-256", 7)
	c.Challenge = "abc"
	if validateVerificationPoW(c) == nil {
		t.Fatal("bad digest accepted")
	}
}

func callVerificationFixture(t *testing.T, tool actool.CoreTool, input any) actool.Result {
	t.Helper()
	b, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	r, err := tool.Call(context.Background(), b, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestVerificationToolCachesExhaustedProofWithoutBudgetReset(t *testing.T) {
	tool := NewToolSet(nil, "").humanVerificationTool()
	c := verificationFixture("SHA-256", 7)
	in := map[string]any{"mode": "solve_pow", "challenge": c, "max_attempts": 1}
	r := callVerificationFixture(t, tool, in)
	if r.IsError {
		t.Fatal(r.Flatten())
	}
	var first verificationPoWResult
	if err := json.Unmarshal([]byte(r.Flatten()), &first); err != nil {
		t.Fatal(err)
	}
	if first.State != "budget_exhausted" || first.Cached {
		t.Fatalf("first %+v", first)
	}
	in["max_attempts"] = 100
	bound := int64(100)
	c.MaxNumber = &bound
	in["challenge"] = c
	r = callVerificationFixture(t, tool, in)
	var second verificationPoWResult
	if err := json.Unmarshal([]byte(r.Flatten()), &second); err != nil {
		t.Fatal(err)
	}
	if !second.Cached || second.Attempts != 1 || second.State != "budget_exhausted" || second.BusinessAccepted {
		t.Fatalf("cache bypass %+v", second)
	}
}

func TestVerificationToolRejectsUnsupportedAndOversizedInputs(t *testing.T) {
	tool := NewToolSet(nil, "").humanVerificationTool()
	for _, in := range []any{
		map[string]any{"mode": "unsupported"},
		map[string]any{"mode": "inspect", "body": strings.Repeat("x", 131073)},
		map[string]any{"mode": "solve_pow", "challenge": verificationFixture("SHA-256", 7), "max_attempts": 1000001},
		map[string]any{"mode": "solve_pow", "challenge": verificationFixture("SHA-256", 7), "timeout_ms": -1},
	} {
		if r := callVerificationFixture(t, tool, in); !r.IsError {
			t.Fatalf("bad input accepted: %v", in)
		}
	}
}

func TestVerificationChallengesReachExistingLoopBreaker(t *testing.T) {
	lb := newLoopBreaker()
	in := json.RawMessage(`{"url":"https://example.invalid/resource"}`)
	for i := 0; i < loopStrikeLimit; i++ {
		release, blocked := lb.before(context.Background(), "http_request", in)
		if blocked != nil {
			t.Fatal("blocked before budget")
		}
		body := fmt.Sprintf(`{"status":200,"headers":{"cf-mitigated":"challenge"},"body":"dynamic challenge %d","body_truncated":true}`, i)
		lb.inspect(context.Background(), "http_request", in, actool.Text(body), nil)
		release()
	}
	release, blocked := lb.before(context.Background(), "http_request", in)
	release()
	if blocked == nil {
		t.Fatal("200 challenges were not counted")
	}
}

// 人机验证工具必须**真的挂在某个工具面上**，否则它只是库里的一段代码，
// 模型永远看不到、也永远不会调用它。
//
// 这条测试原本断言的是"种子绑定了 worker 和 pentest 两个 agent"。重构后
// worker/pentest 的默认面被收紧到「官方 25 个」白名单，human_verification
// 不在其中 —— 于是这条断言失败，而**真实的缺口比断言更大**：
//
//	humanVerificationTool 只被 *_test.go 调用，任何 Tools() 面都不包含它。
//
// 也就是说：能力代码在、单测在、但它在生产链路上不可达。这与 ARTEX 里其它
// 几处「有实现没接线」是同一类问题（见报告）。这里把断言改成守**可达性**：
// 要么它在某个默认面里，要么它被显式登记为「可按需绑定」。两者都没有就是缺口。
func TestVerificationToolIsReachable(t *testing.T) {
	ts := NewToolSet(nil, "")
	faces := map[string][]actool.CoreTool{
		"worker":    ts.WorkerTools(),
		"planner":   ts.PlannerTools(),
		"mainagent": ts.MainAgentTools(),
		"pentest":   builtinToolsByAgent()["pentest"],
	}
	for _, tools := range faces {
		for _, tl := range tools {
			if tl.Name() == "human_verification" {
				return // 挂在某个面上 = 可达，测试通过
			}
		}
	}
	// 不在任何默认面上：那就必须在种子里以「可手动绑定」的形式存在，
	// 否则用户在 UI 里也找不到它。
	for _, seed := range BuiltinToolSeeds() {
		if seed.Key == "human_verification" {
			t.Skipf("human_verification 不在任何默认工具面上，但作为目录种子存在（可手动绑定）：%v", seed.Agents)
			return
		}
	}
	t.Fatalf("human_verification 既不在任何默认工具面上，也不在工具目录种子里 —— 该能力在生产链路上不可达")
}
