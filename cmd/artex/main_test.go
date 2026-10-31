package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"unicode"
)

// TestPrintBannerLocalized verifies that the startup banner printed to stdout is
// Simplified Chinese and carries no leftover Hangul. This is the first thing a
// user sees when running ARTEX, so it must stay in Chinese.
// The "[config] ..." log lines in run() are intentionally out of scope — they
// are logs, which the localization brief ranks lowest (backlog Z2).
func TestPrintBannerLocalized(t *testing.T) {
	out := captureStdout(t, func() { printBanner(":8787") })
	t.Logf("启动横幅示例：\n%s", out)

	hasHan := false
	for _, r := range out {
		if unicode.Is(unicode.Hangul, r) {
			t.Fatalf("启动横幅仍有韩文残留 (%q)：%q", r, out)
		}
		if unicode.Is(unicode.Han, r) {
			hasHan = true
		}
	}
	if !hasHan {
		t.Fatalf("启动横幅未包含中文：%q", out)
	}

	// 断言的是「横幅必须是中文且在讲这件事」，不是某个固定的产品口号。
	// 原来钉的是 "自主渗透测试系统"，文案改成 "AI 自主渗透测试与自适应编排指挥中枢"
	// 之后测试变红，但横幅本身完全正确 —— 这种把营销文案写进断言的写法
	// 只会制造假红灯。这里改钉稳定的事实性字段（品牌/版本/监听地址）与
	// 「渗透测试」这个产品定位词。
	for _, want := range []string{"ARTEX-HYDRA", "自主渗透测试", "版本", "监听地址", ":8787"} {
		if !strings.Contains(out, want) {
			t.Errorf("启动横幅缺少 %q：%q", want, out)
		}
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns whatever
// fn wrote. printBanner uses fmt.Print*, which resolves os.Stdout at call time,
// so swapping it here captures the banner.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	_ = w.Close()
	os.Stdout = orig
	return <-done
}
