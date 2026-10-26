package boot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"

	"github.com/Autumn-27/artex/agent"
)

func TestShutdownContextPreservesNamedCause(t *testing.T) {
	signalCtx, signalCancel := context.WithCancel(context.Background())
	ctx, shutdown := ShutdownContext(signalCtx)
	defer shutdown(nil)
	signalCancel()
	<-ctx.Done()
	if code, _, _, ok := agent.AbortReason(ctx); !ok || code != "shutdown" {
		t.Fatalf("code=%q ok=%v, want shutdown", code, ok)
	}
}

// TestIsAddrInUse 覆盖「端口被占用」的判定：syscall 错误链、Windows 原文、
// 以及完全无关的错误。这条判定的下游是专用退出码 2 —— 判错会让启动脚本
// 把一次端口冲突当成普通崩溃，又回到「重启—再撞」的老路上。
func TestIsAddrInUse(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"syscall", &net.OpError{Op: "listen", Net: "tcp", Err: syscall.EADDRINUSE}, true},
		{"windows-text", errors.New("listen tcp 0.0.0.0:8787: bind: Only one usage of each socket address (protocol/network address/port) is normally permitted."), true},
		{"linux-text", errors.New("listen tcp :8787: bind: address already in use"), true},
		{"wrapped", fmt.Errorf("serve: %w", syscall.EADDRINUSE), true},
		{"other", errors.New("permission denied"), false},
	}
	for _, c := range cases {
		if got := isAddrInUse(c.err); got != c.want {
			t.Fatalf("%s: isAddrInUse=%v want %v", c.name, got, c.want)
		}
	}
}

// TestReportPortInUseExtractsPort 确认提示里带出的是端口号本身（不是 :8787 整个
// 字符串），netstat / taskkill 的命令才能被用户直接复制。
func TestReportPortInUseExtractsPort(t *testing.T) {
	var buf bytes.Buffer
	writePortInUse(&buf, "0.0.0.0:8787", errors.New("bind: address already in use"))
	out := buf.String()
	if !strings.Contains(out, "netstat -ano | findstr :8787") {
		t.Fatalf("提示里没有可复制的 netstat 命令：%q", out)
	}
	if !strings.Contains(out, "不会自动重试") {
		t.Fatalf("提示里没有说明「不再重试」：%q", out)
	}
}
