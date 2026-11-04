package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInputErrorHintCarriesRepairInfo(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"mode"},
		"properties": map[string]any{
			"mode": map[string]any{
				"type":        "string",
				"description": "执行模式：chain | replay | bola | race | timing | diff",
			},
			"url": map[string]any{"type": "string", "description": "目标 URL"},
		},
	}
	in := json.RawMessage(`{"url":"http://a/b"}`)
	err := &fakeErr{"input: missing required property \"mode\""}
	got := inputErrorHint("http_sequence", schema, in, err)
	for _, want := range []string{
		`missing required property "mode"`,
		"本次实际收到字段：url",
		"必填但缺失：mode",
		"chain | replay | bola | race | timing | diff",
		"不要原样重试",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("提示缺少 %q，实际：\n%s", want, got)
		}
	}
}

type fakeErr struct{ s string }

func (e *fakeErr) Error() string { return e.s }

func TestInputErrorHintTypeMismatch(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"strip_headers": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "要剥掉的请求头名称列表",
			},
		},
	}
	in := json.RawMessage(`{"strip_headers":{"Cookie":true}}`)
	got := inputErrorHint("http_sequence", schema, in, &fakeErr{"identities[0].strip_headers: expected array"})
	for _, want := range []string{"identities[0].strip_headers 要求 array", "array<string>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("提示缺少 %q，实际：\n%s", want, got)
		}
	}
}

func TestRe2RepeatHint(t *testing.T) {
	if hint := re2RepeatHint(`(?s)<div>(.{0,2200})</div>`); hint == "" || !strings.Contains(hint, "1000") {
		t.Fatalf("应提示 RE2 上限，实际 %q", hint)
	}
	if hint := re2RepeatHint(`a{0,10}`); hint != "" {
		t.Fatalf("合法量词不该提示，实际 %q", hint)
	}
}

func TestLocalPathErrorListsNearbyFiles(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"output-1792496464-27.txt", "output-1792496464-28.txt", "other.log"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ts := &ToolSet{}
	missing := filepath.Join(dir, "output-1792496464-29.txt")
	msg := ts.localPathError("读取文件", "cmd-output/output-1792496464-29.txt", missing)
	for _, want := range []string{"路径不存在", "output-1792496464-28.txt", "output-1792496464-27.txt"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("提示缺少 %q，实际：\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "other.log") {
		t.Fatalf("不该把不相关文件列进来：\n%s", msg)
	}
}

func TestLocalPathErrorFallsBackToWorkdir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "wordlist.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := &ToolSet{workDir: dir}
	msg := ts.localPathError("读取 wordlist_file", "wl/probe.txt", filepath.Join(dir, "wl", "probe.txt"))
	if !strings.Contains(msg, "工作目录") {
		t.Fatalf("应回落到工作目录列表：\n%s", msg)
	}
}

// TestElementErrorHint 校验错误指向数组元素时，必须带上该元素级别的字段清单。
func TestElementErrorHint(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"assets": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":     "object",
					"required": []any{"type"},
					"properties": map[string]any{
						"type": map[string]any{
							"type":        "string",
							"enum":        []string{"root_domain", "ip", "subdomain", "app", "service", "endpoint"},
							"description": "资产类型",
						},
						"url":    map[string]any{"type": "string", "description": "完整 URL"},
						"method": map[string]any{"type": "string", "description": "HTTP 方法"},
					},
				},
			},
		},
	}
	in := []byte(`{"assets":[{"url":"https://a/b","method":"POST","description":"x"}]}`)
	got := inputErrorHint("insert_assets", schema, in, &fakeErr{`assets[0]: missing required property "type"`})
	for _, want := range []string{
		"第 1 个元素实际收到字段：description, method, url",
		"该元素必填：type",
		"endpoint",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("元素级提示缺少 %q，实际：\n%s", want, got)
		}
	}

	// 顶层字段错误（不指向元素）时不叠加元素提示。
	if hint := elementErrorHint(nil, in, &fakeErr{`input: missing required property "assets"`}); hint != "" {
		t.Fatalf("无 props 时不该生成元素提示：%q", hint)
	}
	// 越界索引不 panic、不生成提示。
	if hint := elementErrorHint(schema["properties"].(map[string]any), in, &fakeErr{`assets[7]: missing required property "type"`}); hint != "" {
		t.Fatalf("越界索引不该生成提示：%q", hint)
	}
}
