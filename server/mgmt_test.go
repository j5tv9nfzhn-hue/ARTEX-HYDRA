package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

// TestMgmtAPI exercises the database-backed management API through the real mux.
func TestMgmtAPI(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("database unavailable (%v) — skipping management API test", err)
	}
	if m.pg == nil {
		t.Fatal("数据库不可用")
	}
	td := t.TempDir()
	s := New(context.Background(), m, td, td, td)
	h := s.Handler()
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}

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
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	// agents 清单的两个端点必须分开看（这是 11-07 重构后的产品口径）：
	//
	//	GET /api/agents          只列「会话 Agent」——coord_root / coord_child /
	//	                         worker / goals 全部被 orchestrationAgentKeys 过滤掉，
	//	                         它们在全新库里就是 0 条；断言"至少 5 个"是 planner
	//	                         时代的口径，planner 下线后必然红。
	//	GET /api/orchestration   运行时模板：根、子代理、共用运行配置、目标拆解。
	//	                         当前世代内部有 4 条，这里是它们该被验证的地方。
	code, out := do("GET", "/api/agents", nil)
	if code != 200 {
		t.Fatalf("GET agents: %d", code)
	}
	if ags, _ := out["agents"].([]any); len(ags) != 0 {
		t.Fatalf("会话 Agent 清单不应包含运行时模板，实得 %d 条: %v", len(ags), ags)
	}

	// 运行时模板：必须正好覆盖当前世代四个角色，且不含已下线的 planner 等。
	code, out = do("GET", "/api/orchestration", nil)
	if code != 200 {
		t.Fatalf("GET orchestration: %d", code)
	}
	tags, _ := out["agents"].([]any)
	seen := map[string]bool{}
	for _, a := range tags {
		if m, ok := a.(map[string]any); ok {
			if k, _ := m["key"].(string); k != "" {
				seen[k] = true
			}
		}
	}
	for _, want := range []string{"coord_root", "coord_child", "worker", "goals"} {
		if !seen[want] {
			t.Errorf("运行时模板缺少 %q，实得 %v", want, keysOf(seen))
		}
	}
	for _, gone := range []string{"planner", "mainagent", "reviewer"} {
		if seen[gone] {
			t.Errorf("已下线的角色 %q 不应再出现在运行时模板里", gone)
		}
	}

	// 提示词读写用当前世代真实存在的角色 coord_root（seedPrompts 保证有行）。
	const promptKey = "coord_root"
	// invalid template var → 400 (catalog whitelist)
	code, out = do("PUT", "/api/agents/"+promptKey+"/prompt", map[string]string{"template": "hi {{.Nope}}"})
	if code != 400 {
		t.Fatalf("bad var should be 400, got %d (%v)", code, out)
	}

	// valid template using seeded catalog vars → 200。
	// Goal / Scope 都是 coord_root 模板真正会消费的变量（agent/prompts_coord.go），
	// 用它们才能证明"白名单里有的变量可以保存"，而不是碰巧没被拦。
	code, _ = do("PUT", "/api/agents/"+promptKey+"/prompt", map[string]string{"template": "你是编排者，目标：{{.Goal}}，范围 {{.Scope}}"})
	if code != 200 {
		t.Fatalf("valid prompt save: %d", code)
	}

	// preview renders with catalog examples
	code, out = do("POST", "/api/agents/"+promptKey+"/prompt/preview", map[string]any{})
	if code != 200 {
		t.Fatalf("preview: %d", code)
	}
	if rendered, _ := out["rendered"].(string); rendered == "" || rendered == "你是编排者，目标：{{.Goal}}，范围 {{.Scope}}" {
		t.Fatalf("preview did not substitute: %q", out["rendered"])
	}

	// mcp create → visible to coord_root → resource-side sees it → delete clears it
	// Remove any leftover from prior run to keep the test idempotent.
	m.pg.Exec(`DELETE FROM mcp_servers WHERE name = 't-itest'`)
	code, out = do("POST", "/api/mcp", map[string]any{"name": "t-itest", "transport": "stdio", "command": "x", "enabled": true})
	if code != 200 {
		t.Fatalf("create mcp: %d", code)
	}
	mid := int64(out["id"].(float64))

	ag, _ := m.pg.GetAgentByKey(promptKey)
	if ag == nil {
		t.Fatalf("%s agent missing", promptKey)
	}
	code, _ = do("PUT", "/api/agents/"+promptKey+"/visibility", map[string]any{"mcp": []int64{mid}, "skill": []int64{}})
	if code != 200 {
		t.Fatalf("set visibility: %d", code)
	}
	code, out = do("GET", "/api/visibility/mcp/"+itoaTest(mid), nil)
	if code != 200 {
		t.Fatalf("resource visibility: %d", code)
	}
	if agents, _ := out["agents"].([]any); len(agents) != 1 || agents[0].(string) != itoaTest(ag.ID) {
		t.Fatalf("resource-side should list %s, got %v", promptKey, out["agents"])
	}

	// cleanup
	do("DELETE", "/api/mcp/"+itoaTest(mid), nil)
	m.pg.Exec(`DELETE FROM agent_prompts WHERE agent_id=$1`, ag.ID)
	m.pg.Exec(`UPDATE agents SET current_prompt_id=NULL WHERE id=$1`, ag.ID)
}

func itoaTest(n int64) string {
	b := []byte{}
	if n == 0 {
		return "0"
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// keysOf 把 map 的 key 排序成切片，用于让失败信息里的"实得"稳定可读。
func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
