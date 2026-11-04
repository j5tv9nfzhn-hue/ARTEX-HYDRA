package mcpbuiltin

// 这些用例证明「保留下来的内置工具真的能跑」：读样本、抽字符串、识别文件、抽 IOC。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func call(t *testing.T, fn func(context.Context, json.RawMessage) (string, error), args any) string {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := fn(context.Background(), raw)
	if err != nil {
		t.Fatalf("工具返回错误：%v", err)
	}
	return out
}

func TestBuiltinServersExposed(t *testing.T) {
	for _, name := range []string{"artex-binscan"} {
		s, ok := ByName(name)
		if !ok {
			t.Fatalf("缺少内置服务器 %s", name)
		}
		if s.Domain == "" || s.Title == "" {
			t.Fatalf("%s 缺元数据", name)
		}
		for _, tl := range s.Tools {
			if tl.Name == "" || tl.Description == "" || tl.Schema == nil || tl.Handler == nil {
				t.Fatalf("%s/%s 定义不完整", name, tl.Name)
			}
		}
	}
}

func TestStringsAndIdentify(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sample.bin")
	body := []byte("HEADER\x00\x00ARTEX-MARKER-9f3c\x00http://evil.example/c2\x00")
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	so := call(t, stringsScan, map[string]any{"path": p, "min_len": 4})
	if !strings.Contains(so, "ARTEX-MARKER-9f3c") {
		t.Fatalf("字符串扫描没有命中标记：\n%s", so)
	}
	io := call(t, fileIdentify, map[string]any{"path": p})
	if !strings.Contains(io, "SHA256") {
		t.Fatalf("file_identify 应给出哈希：\n%s", io)
	}
	ioc := call(t, iocExtract, map[string]any{"path": p})
	if !strings.Contains(ioc, "evil.example") {
		t.Fatalf("IOC 抽取应命中域名：\n%s", ioc)
	}
}

func TestPEInfoOnSelf(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("只在 Windows 上验证 PE")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("取不到自身路径：%v", err)
	}
	out, err := peInfo(context.Background(), mustJSON(map[string]any{"path": exe}))
	if err != nil {
		t.Fatalf("解析自身 PE 失败：%v", err)
	}
	if !strings.Contains(out, "架构") || !strings.Contains(out, "节：") {
		t.Fatalf("PE 报告缺关键段落：\n%s", out)
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("marshal: %v", err))
	}
	return b
}
