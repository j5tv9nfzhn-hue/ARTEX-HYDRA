package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/norma/permission"
)

// TestPermissionTierAPIRoundTrip 走真实 HTTP mux，验证「前端控件 → 接口 → 全局档位」
// 这条链路真的闭合：
//
//  1. 首启 GET 必须回显默认档位 bypass（而不是空值），前端才画得出选中项；
//  2. PUT plan 之后，agent.EffectivePermissionMode() 必须立刻变成 ModePlan
//     —— 这是"设置生效"的唯一可信证据，接口返回 200 只说明写库成功；
//  3. UI 依赖 read_only 字段决定是否显示"只读会拒绝写操作"的警示；
//  4. 非法档位必须 400 且不改变现状（否则一次手抖就会把整机放行/锁死）。
func TestPermissionTierAPIRoundTrip(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	td := t.TempDir()
	s := New(context.Background(), m, td, td, td)
	h := s.Handler()
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	// 全局档位是进程级状态，用例结束必须回滚，否则会污染同包其他用例。
	t.Cleanup(func() { _ = agent.SetPermissionTier(string(agent.PermissionTierBypass)) })

	do := func(method, path string, body any) (int, map[string]any) {
		var r *http.Request
		if body != nil {
			b, _ := json.Marshal(body)
			r = httptest.NewRequest(method, path, bytes.NewReader(b))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	code, out := do("GET", "/api/permission-tier", nil)
	if code != http.StatusOK {
		t.Fatalf("GET 状态码 %d，期望 200（body=%v）", code, out)
	}
	if got, _ := out["tier"].(string); got != string(agent.PermissionTierBypass) {
		t.Fatalf("首启默认档位应为 bypass，实得 %q", got)
	}
	if ro, _ := out["read_only"].(bool); ro {
		t.Fatalf("bypass 不应是只读")
	}
	if got, _ := out["default_tier"].(string); got != string(agent.PermissionTierBypass) {
		t.Fatalf("default_tier 应为 bypass，实得 %q", got)
	}
	if opts, _ := out["options"].([]any); len(opts) != 3 {
		t.Fatalf("选项应为 3 个，实得 %d", len(opts))
	}

	code, out = do("PUT", "/api/permission-tier", map[string]any{"tier": "plan"})
	if code != http.StatusOK {
		t.Fatalf("PUT plan 状态码 %d（body=%v）", code, out)
	}
	if got := agent.EffectivePermissionMode(); got != permission.ModePlan {
		t.Fatalf("切到 plan 后全局执行模式应为 ModePlan，实得 %q", got)
	}
	if ro, _ := out["read_only"].(bool); !ro {
		t.Fatalf("plan 的 read_only 应为 true")
	}

	// 非法值：必须 400，且不能把刚设好的 plan 冲掉。
	code, _ = do("PUT", "/api/permission-tier", map[string]any{"tier": "root"})
	if code != http.StatusBadRequest {
		t.Fatalf("非法档位应返回 400，实得 %d", code)
	}
	if got := agent.CurrentPermissionTier(); got != agent.PermissionTierPlan {
		t.Fatalf("非法请求不应改变现状，实得 %q", got)
	}

	// 切回完全访问（产品的默认档），并确认执行模式同步回来。
	code, out = do("PUT", "/api/permission-tier", map[string]any{"tier": "bypass"})
	if code != http.StatusOK {
		t.Fatalf("PUT bypass 状态码 %d（body=%v）", code, out)
	}
	if got := agent.EffectivePermissionMode(); got != permission.ModeBypass {
		t.Fatalf("切回 bypass 后应为 ModeBypass，实得 %q", got)
	}
}
