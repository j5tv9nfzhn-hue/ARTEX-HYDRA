package server

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestNormalizeCLITargets(t *testing.T) {
	got := normalizeCLITargets([]string{"https://a.example, b.example", " b.example;10.0.0.1\n", ""})
	want := []string{"https://a.example", "b.example", "10.0.0.1"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestCLIPrinterRendersActivity(t *testing.T) {
	var buf bytes.Buffer
	pr := &cliPrinter{w: &buf}

	pr.activity(db.Activity{Worker: "root", Kind: "tool_use", Summary: "nmap -sV target"})
	pr.activity(db.Activity{Worker: "child-1", Kind: "tool_result", Summary: "open 443", IsError: true})
	pr.activity(db.Activity{Worker: "root", Kind: "thinking", Summary: "internal reasoning"})
	pr.activity(db.Activity{Worker: "root", Kind: "usage"})
	pr.activity(db.Activity{Worker: "root", Kind: "result", Summary: "（运行被中断：取消方未附加具名原因）"})

	out := buf.String()
	if !strings.Contains(out, "[root] → nmap -sV target") {
		t.Fatalf("tool_use 未渲染: %q", out)
	}
	if !strings.Contains(out, "[child-1] ← open 443") {
		t.Fatalf("tool_result 未渲染: %q", out)
	}
	if strings.Contains(out, "internal reasoning") {
		t.Fatalf("默认不应输出 thinking: %q", out)
	}
	if strings.Contains(out, "运行被中断") {
		t.Fatalf("交卷产生的 abort 结果不应输出: %q", out)
	}

	pr.verbose = true
	pr.activity(db.Activity{Worker: "root", Kind: "thinking", Summary: "internal reasoning"})
	if !strings.Contains(buf.String(), "internal reasoning") {
		t.Fatalf("verbose 应输出 thinking: %q", buf.String())
	}
}

func TestCLIResultHasFindings(t *testing.T) {
	if (CLIResult{}).HasFindings() {
		t.Fatal("空结果不应判定为有漏洞")
	}
	if !(CLIResult{Findings: []CLIFinding{{Severity: "low"}}}).HasFindings() {
		t.Fatal("有漏洞时应返回 true")
	}
}
