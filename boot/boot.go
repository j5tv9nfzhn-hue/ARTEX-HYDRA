// Package boot 承载 ARTEX 服务进程的共享启动骨架：凭据主密钥、进程树收口、
// 双图库打开、server 装配与监听器启动，以及优雅停机。
//
// cmd/artex（服务器版 artex-rea）与 desktop（Wails 桌面壳 artex-hydra）都从这一处
// 启动同构的后端。启动顺序与停机顺序是三条不变量，任何调整都必须同时改两端，
// 不允许任何一边私自重排。
package boot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/config"
	"github.com/Autumn-27/artex/server"
)

// ExitPortInUse 是"端口被占用"的专用退出码：程序已经把占用端口与处置办法打印出来了，
// 启动脚本据此把窗口留住给用户看，但**不再重启**——重启只会立刻再撞同一个端口，
// 一路刷屏，正是升级后旧进程没退干净时看到的"窗口反复闪"。
const ExitPortInUse = 2

// Options 描述一次服务启动所需的全部输入。
type Options struct {
	// Version 是构建版本号，经 server.BuildVersion 上报给 /api/health。
	Version string
	// DataDir 是 SQLite 双图库的数据目录。
	DataDir string
	// ProxyAddr 是流量记录代理地址（空串禁用）。
	ProxyAddr string
	// SkillDir 是技能目录。
	SkillDir string
	// ExeDir 是发行包根目录：凭据主密钥、jwt.key 与数据都相对它定位。
	ExeDir string
	// ListenAddr 是 HTTP 监听地址。
	ListenAddr string
	// ToleratePortInUse 为 true 时监听失败只记日志不致命：桌面壳的窗口渲染走
	// 进程内 Handler、不依赖回环端口，端口被服务器版占用时窗口照常可用。
	ToleratePortInUse bool
	// DisableAuth 打开「桌面壳进程内窗口免登录」：只对 desktop/shell.go 打了标记的
	// 请求生效（server.authExempt），网络来源（同机浏览器、反向代理、局域网设备）
	// 照样校验 JWT。服务器版恒为 false（账号密码登录）。
}

// App 是一次已启动的服务实例，持有监听器、server 与双图库。
type App struct {
	Version    string
	ListenAddr string
	srv        *server.Server
	httpSrv    *http.Server
	mgr        *server.Manager
}

// Handler 返回 server 的完整 HTTP 处理器（API + 静态前端 + SPA 兜底）。
// 桌面壳把它挂到 wails AssetServer.Handler，并在交给它之前打「进程内窗口」标记
// （见 server.MarkDesktopShellRequest）：页面内容与浏览器看到的完全一致，唯一差别
// 是窗口的请求免登录，浏览器的请求仍要账号密码。
func (a *App) Handler() http.Handler {
	if a.srv == nil {
		return http.NotFoundHandler()
	}
	return a.srv.Handler()
}

// Prepare 按固定顺序启动服务：
//
//  1. 凭据主密钥（先于任何存储读写：llm_profiles.api_key 等加密字段没有它读不出来）；
//  2. 进程树收口（Windows Job Object，best-effort，失败只降级）；
//  3. 打开双图库；
//  4. 装配 server（含监听地址回显与非回环绑定警告）；
//  5. 启动监听器。
//
// 返回的 App 必须经 Shutdown 收尾；停机三条不变量（先停新请求、再等在跑回合、
// 最后关库）由 Shutdown 与调用方的 ctx 共同保证。
func Prepare(ctx context.Context, opts Options) (*App, error) {
	if err := server.InstallCredentialKey(opts.ExeDir); err != nil {
		return nil, fmt.Errorf("凭据主密钥初始化失败：%w", err)
	}

	// 进程树收口：把自己放进 kill-on-close Job Object，之后派生的子进程
	// （Bash 工具的 powershell/python 及其多进程子进程、rg、npx…）与主进程同生共死。
	// 失败只降级：退回「子进程可能残留」的旧行为，日志里会说明原因。
	_ = guardProcessTree()

	mgr, err := server.NewManager(opts.DataDir, opts.ProxyAddr)
	if err != nil {
		return nil, fmt.Errorf("open stores: %w", err)
	}

	// workers 是「同时有几个 agent 在跑」的上限。超过物理/逻辑核数时，多个 worker
	// 同时执行 CPU 密集型命令（穷举、爆破、高并发 fuzz）会把 CPU 打满，平台服务与
	// SQLite 写入都会跟着变慢——只提示，不擅自改用户设置。
	if w := mgr.Workers(); w > runtime.NumCPU() {
		log.Printf("[config] workers=%d 超过本机逻辑 CPU 数 %d：并行 worker 同时跑 CPU 密集型脚本会打满 CPU，"+
			"平台与数据库会一起变慢；建议设为不超过 %d", w, runtime.NumCPU(), runtime.NumCPU())
	}

	skillDir := opts.SkillDir
	if abs, err := filepath.Abs(skillDir); err == nil {
		skillDir = abs
	}
	log.Printf(config.T("[config] skill 目录: %s"), skillDir)

	srv := server.New(ctx, mgr, skillDir, opts.DataDir, opts.ExeDir)
	// 必须在 Handler()/监听器之前定下来：requireAuth 是构建期装配的中间件。
	srv.SetAuthDisabled(opts.DisableAuth)
	srv.SetListenAddr(opts.ListenAddr)
	srv.WarnIfExposed(opts.ListenAddr)

	httpSrv := &http.Server{
		Addr:              opts.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Printf("ARTEX-HYDRA %s backend listening on %s (data=%s, workers=%d)",
			opts.Version, opts.ListenAddr, opts.DataDir, mgr.Workers())
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			if isAddrInUse(err) {
				if opts.ToleratePortInUse {
					// 桌面壳：窗口渲染走进程内 Handler，端口被占用不影响使用；
					// Web 入口（浏览器地址）指向已运行的那个实例。
					log.Printf("[boot] 监听 %s 失败：端口已被占用。桌面窗口不受影响；Web 入口指向已运行的实例。", opts.ListenAddr)
					return
				}
				writePortInUse(os.Stderr, opts.ListenAddr, err)
				os.Exit(ExitPortInUse)
			}
			log.Fatalf("serve: %v", err)
		}
	}()

	return &App{
		Version:    opts.Version,
		ListenAddr: opts.ListenAddr,
		srv:        srv,
		httpSrv:    httpSrv,
		mgr:        mgr,
	}, nil
}

// Shutdown 按停机三条不变量收尾，别调换：
//
//  1. 先停止接受新请求（不再有新的 agent 回合）；
//  2. 等在跑的回合收尾，超时强断（grace 由调用方的 ctx 给出）；
//  3. 关闭双图库，之后才轮到自重启拉起来的新进程开库。
func (a *App) Shutdown(ctx context.Context) error {
	if a.httpSrv != nil {
		_ = a.httpSrv.Shutdown(ctx)
	}
	if a.mgr != nil {
		return a.mgr.Close()
	}
	return nil
}

// ShutdownContext deliberately does not derive from signalCtx. If it did, the
// parent's plain context.Canceled could win the race before AbortShutdown was
// attached to the child, losing the diagnostic cause in every running Agent.
func ShutdownContext(signalCtx context.Context) (context.Context, context.CancelCauseFunc) {
	ctx, shutdown := context.WithCancelCause(context.Background())
	go func() {
		select {
		case <-signalCtx.Done():
			shutdown(agent.AbortShutdown)
		case <-ctx.Done():
		}
	}()
	return ctx, shutdown
}

// DisplayAddr 把监听地址转成浏览器能点的形式（0.0.0.0 / :: 换成 127.0.0.1）。
func DisplayAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	switch {
	case addr == "":
		return "127.0.0.1:8787"
	case strings.HasPrefix(addr, ":"):
		return "127.0.0.1" + addr
	case strings.HasPrefix(addr, "0.0.0.0:"):
		return "127.0.0.1:" + strings.TrimPrefix(addr, "0.0.0.0:")
	case strings.HasPrefix(addr, "[::]:"):
		return "127.0.0.1:" + strings.TrimPrefix(addr, "[::]:")
	default:
		return addr
	}
}

// isAddrInUse 判断监听失败是不是"端口已被占用"。
//
// Windows 的 WSAEADDRINUSE 与 Linux 的 EADDRINUSE 在 Go 里都映射到
// syscall.EADDRINUSE；这里再按文本兜一次底，防止中间某层把错误链包掉。
func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "only one usage of each socket address") ||
		strings.Contains(msg, "address already in use")
}

// writePortInUse 把"端口被占用"讲成人话，并给出下一步。
//
// 升级后最容易踩的一脚：旧版本进程还活着（旧版本没有单实例锁），新进程拿到了锁
// 却绑不上端口。以前启动脚本会把这种退出当成"异常"无限重启，每一轮都撞同一个
// 端口，日志刷屏、窗口反复闪，用户完全看不出发生了什么。
func writePortInUse(w io.Writer, addr string, err error) {
	port := strings.TrimSpace(addr)
	if i := strings.LastIndex(port, ":"); i >= 0 {
		port = port[i+1:]
	}
	fmt.Fprintf(w, "\n[artex] 无法监听 %s：端口已被占用。\n", DisplayAddr(addr))
	fmt.Fprintf(w, "  底层错误：%v\n\n", err)
	fmt.Fprintln(w, "  最常见的原因：升级之后旧版本进程还没退干净（旧版本没有单实例锁）。")
	fmt.Fprintln(w, "  处理办法：")
	fmt.Fprintf(w, "    1) 看是谁占着：netstat -ano | findstr :%s\n", port)
	fmt.Fprintln(w, "    2) 结束它：    taskkill /PID <PID> /F")
	fmt.Fprintln(w, "    3) 或者接管：  start.bat -takeover    （start-lan.bat 的 -lan 也会接管）")
	fmt.Fprintln(w, "\n  本次启动已退出，不会自动重试。")
}
