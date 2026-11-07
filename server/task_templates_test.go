package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
)

func TestTaskTemplateHTTPCRUD(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s := New(context.Background(), m, t.TempDir(), t.TempDir(), t.TempDir())
	h := s.Handler()
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}

	do := func(method, path string, body any) (int, map[string]any) {
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	name := fmt.Sprintf("http-template-%d", time.Now().UnixNano())
	code, created := do(http.MethodPost, "/api/task-templates", map[string]any{
		"name": name, "description": "description", "goal": "goal",
	})
	if code != http.StatusCreated {
		t.Fatalf("create status=%d body=%v", code, created)
	}
	id := int64(created["id"].(float64))
	t.Cleanup(func() { _, _ = m.pg.DeleteTaskTemplate(id) })

	code, duplicate := do(http.MethodPost, "/api/task-templates", map[string]any{
		"name": "  " + name + "  ", "description": "other", "goal": "other",
	})
	if code != http.StatusConflict {
		t.Fatalf("duplicate status=%d body=%v", code, duplicate)
	}

	code, updated := do(http.MethodPatch, fmt.Sprintf("/api/task-templates/%d", id), map[string]any{"goal": "patched goal"})
	if code != http.StatusOK || updated["goal"] != "patched goal" || updated["description"] != "description" {
		t.Fatalf("patch status=%d body=%v", code, updated)
	}
	code, _ = do(http.MethodPatch, fmt.Sprintf("/api/task-templates/%d", id), map[string]any{
		"goal": strings.Repeat("界", db.MaxTaskTemplateTextRunes+1),
	})
	if code != http.StatusBadRequest {
		t.Fatalf("overlong patch status=%d, want %d", code, http.StatusBadRequest)
	}
	code, _ = do(http.MethodPost, "/api/task-templates", map[string]any{
		"name": "oversized", "description": strings.Repeat("x", maxTaskTemplateRequestBytes+1), "goal": "goal",
	})
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized create status=%d, want %d", code, http.StatusRequestEntityTooLarge)
	}
	code, listed := do(http.MethodGet, "/api/task-templates", nil)
	if code != http.StatusOK || listed["templates"] == nil {
		t.Fatalf("list status=%d body=%v", code, listed)
	}
	code, deleted := do(http.MethodDelete, fmt.Sprintf("/api/task-templates/%d", id), nil)
	if code != http.StatusOK || int64(deleted["deleted"].(float64)) != id {
		t.Fatalf("delete status=%d body=%v", code, deleted)
	}
}

func TestConversationPatchReturnsPinState(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s := New(context.Background(), m, t.TempDir(), t.TempDir(), t.TempDir())
	// agent_key 用 coord_root，不再用 mainagent —— 后者已在 db/legacyAgentKeys 里
	// 下线，启动时被 pruneLegacyAgents 删行，CreateConversation 因外键找不到
	// 该 agent 而返回「找不到 Agent」(404)，测试于是在第一行就崩。
	//
	// 这是三处"过期断言"里最容易误判的一处：报错是 HTTP 404，看起来像接口坏了，
	// 实际是测试引用了不存在的角色。coord_root 是对话页/会话页当前真正使用的键
	// （见 server/conversations.go 的 HARNESS 会话绑定）。
	conversation, err := m.pg.CreateConversation("coord_root", "pin through http", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.pg.DeleteConversation(conversation.ID) })
	token, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{"title": "  pinned title  ", "pinned": true})
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/conversations/%d", conversation.ID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", rec.Code, rec.Body.String())
	}
	var updated struct {
		Title    string  `json:"title"`
		Pinned   bool    `json:"pinned"`
		PinnedAt *string `json:"pinned_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Title != "pinned title" || !updated.Pinned || updated.PinnedAt == nil {
		t.Fatalf("unexpected conversation patch response: %+v", updated)
	}

	body, _ = json.Marshal(map[string]any{"title": strings.Repeat("会", maxConversationTitleRunes+1)})
	req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/conversations/%d", conversation.ID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("overlong conversation title status=%d body=%s", rec.Code, rec.Body.String())
	}

	body, _ = json.Marshal(map[string]any{"title": strings.Repeat("x", maxConversationRequestBytes+1)})
	req = httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/conversations/%d", conversation.ID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized conversation patch status=%d body=%s", rec.Code, rec.Body.String())
	}

	body, _ = json.Marshal(map[string]any{
		"agent_key": "coord_root", "title": strings.Repeat("会", maxConversationTitleRunes+1),
	})
	req = httptest.NewRequest(http.MethodPost, "/api/conversations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("overlong conversation create status=%d body=%s", rec.Code, rec.Body.String())
	}

	body, _ = json.Marshal(map[string]any{
		"agent_key": "coord_root", "title": strings.Repeat("x", maxConversationRequestBytes+1),
	})
	req = httptest.NewRequest(http.MethodPost, "/api/conversations", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized conversation create status=%d body=%s", rec.Code, rec.Body.String())
	}
}
