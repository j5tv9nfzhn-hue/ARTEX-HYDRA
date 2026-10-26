// Command artex-hydra 是 ARTEX-HYDRA 的 Wails V2 桌面端：进程内启动与
// artex-rea 完全同构的后端（SQLite 双图库、agent 引擎、HTTP API），
// 用原生 webview 窗口渲染同一套 Next.js 前端，同时保留 http://127.0.0.1:8787
// 的浏览器入口（端口被服务器版占用时窗口照常可用）。
//
// 发行包是"入口 + 核心"两件套（与服务器版同一套 selfupdate 布局）：
//
//	artex-hydra[.exe]       入口，永远待在根目录，只按 pointer.json 转发；
//	artex-hydra-core[.exe]  真正干活的核心（本文件编译出的 wails 应用）。
//
// 本文件与 cmd/artex/main.go 的启动序列同构（启动守卫 → 自更新引导 →
// 单实例锁 → 共享 boot.Prepare → settle → 窗口/控制台 → 停机三条不变量），
// 任何顺序调整都必须同步两边。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/boot"
	"github.com/Autumn-27/artex/config"
	"github.com/Autumn-27/artex/selfupdate"
	"github.com/Autumn-27/artex/server"
)

// version 是构建版本号，注入方式与服务器版一致：
// -ldflags "-X main.version=<tag>"。
var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	// 监听地址的优先级与服务器版一致（-addr > -lan > config.json/env > 回环默认），
	// 共用 config.ResolveListenAddr 单点收口。
	var (
		addr      = flag.String("addr", "", "HTTP 监听地址（空：取 config.json 的 listen / ARTEX_LISTEN，再空则 127.0.0.1:8787；:8787 表示开放局域网）")
		lan       = flag.Bool("lan", false, "开放局域网访问（等于 -addr :8787），供手机端 App 连接")
		dataDir   = flag.String("data", filepath.Join(config.BaseDir(), "data"), "data directory for SQLite stores (default: data/ next to the executable)")
		proxy     = flag.String("proxy", "127.0.0.1:8788", "traffic recording proxy address (empty to disable)")
		resetPass = flag.Bool("reset-password", false, "重置管理员密码后退出；新密码取自 ARTEX_RESET_NEWPASS，未设置则交互输入")
	)
	flag.Parse()
	listenAddr := config.ResolveListenAddr(*addr, *lan)

	// 密码重置是一次性离线操作：不占端口、不跑自更新、不起后台 worker，
	// 排在所有启动步骤之前（与服务器版一致）。
	if *resetPass {
		return boot.ResetPassword()
	}

	// 启动守卫：拒绝从升级临时文件（.new/.old/.failed/.staged/.part…）启动。
	if err := selfupdate.RefuseUnsafeExecutable(); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		return 2
	}

	// 版本化布局的入口转发：pointer.json 指向的核心程序不是自己时，交给它执行。
	if code, handed := selfupdate.Handoff(); handed {
		return code
	}

	exeDir := config.BaseDir()

	server.BuildVersion = version
	server.StartLogCapture()

	// 单实例仲裁：同一安装目录只允许一个桌面实例（回环端口互斥只是二次保险）。
	// 桌面端不做端口接管：窗口渲染走进程内 Handler、不依赖回环端口，
	// 绝不去杀另一个实例的进程——要换实例请先关闭窗口。
	if rootDir, rootErr := selfupdate.InstallRoot(); rootErr != nil {
		log.Printf("[boot] 安装目录未知（%v），跳过单实例锁", rootErr)
	} else {
		lockWait := 3 * time.Second
		if selfupdate.RelaunchedChild() {
			lockWait = 20 * time.Second
		}
		release, state, lockErr := selfupdate.AcquireInstance(rootDir, lockWait)
		switch {
		case lockErr != nil:
			log.Printf("[boot] 单实例锁不可用（%v），退回端口互斥", lockErr)
		case state == selfupdate.LockBusy:
			fmt.Printf("\n  ARTEX-HYDRA 桌面端已在运行：http://%s\n", boot.DisplayAddr(listenAddr))
			fmt.Println("  本次启动直接退出，没有动那个实例；要停止它请回到它的窗口关闭。")
			return 0
		default:
			defer release()
			if state == selfupdate.LockAbandoned {
				log.Printf("[boot] 上一个实例未正常退出（锁已被内核回收），本次接管")
			}
		}
	}

	// Self-update bootstrap：处理磁盘上已暂存/已切换的升级（同服务器版）。
	// 必须发生在开库与占端口之前——它可能以「退出并让入口拉起新核心」结束。
	action, upState := selfupdate.Bootstrap(version)
	server.SetBootUpdateState(upState)
	if action == selfupdate.Restart {
		return selfupdate.ExitForRestart("bootstrap")
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, shutdown := boot.ShutdownContext(sigCtx)
	defer shutdown(agent.AbortShutdown)

	// 共享启动骨架：与服务器版同一处、同一顺序（boot.Prepare）。
	// ToleratePortInUse：端口被服务器版占用时窗口照常可用，Web 入口指向已运行的实例。
	app, err := boot.Prepare(ctx, boot.Options{
		Version:           version,
		DataDir:           *dataDir,
		ProxyAddr:         *proxy,
		SkillDir:          config.SkillDir(),
		ExeDir:            exeDir,
		ListenAddr:        listenAddr,
		ToleratePortInUse: true,
	})
	if err != nil {
		log.Fatalf("启动失败：%v", err)
	}

	// 升级"活下来"的确认（同服务器版）：延迟核对版本号与 /api/health，
	// 不通过就把台账置为 failed，由下一次启动的 Bootstrap 累计失败次数并回滚。
	settle := time.AfterFunc(selfupdate.SettleDelay, func() {
		if err := selfupdate.Settle(version, listenAddr); err != nil {
			log.Printf("[update] %v", err)
		}
	})
	defer settle.Stop()

	// 首运行设置令牌落盘：桌面端没有控制台，令牌必须从 setup-info.txt 读。
	// 令牌由 SPA 首屏的 /api/auth/status 触发签发，这里轮询到出现后写入。
	go watchSetupToken(app)

	// 打开原生窗口并阻塞，直到窗口关闭或更新流程请求重启。
	return runDesktopWindow(ctx, app, listenAddr, shutdown)
}

// runDesktopWindow 打开 Wails 原生窗口并阻塞，直到窗口关闭或更新流程请求重启。
// 返回值是进程退出码：0 = 正常关闭；重启协议退出码（75）由 selfupdate 决定，
// 入口进程据此拉起新版本核心。
func runDesktopWindow(ctx context.Context, app *boot.App, listenAddr string, shutdown context.CancelCauseFunc) int {
	// 页面一键更新完成后，server 的重启通道会关闭：让窗口收起，
	// 再走共享停机序列并以重启协议返回。
	restarting := false
	go func() {
		select {
		case <-ctx.Done():
		case <-server.RestartRequested():
			restarting = true
			wailsruntime.Quit(ctx)
		}
	}()

	err := wailsruntime.Run(&options.App{
		Title:            "ARTEX-HYDRA",
		Width:            1440,
		Height:           900,
		MinWidth:         1024,
		MinHeight:        640,
		BackgroundColour: &options.RGBA{R: 13, G: 13, B: 18, A: 1},
		// 窗口内渲染的正是进程内 server 的同一套 SPA：AssetServer 的 Assets 为空，
		// 全部请求（静态页 / API / SSE）都转发给 srv.Handler()，
		// 与浏览器访问 http://127.0.0.1:8787 完全同源同行为。
		AssetServer: &assetserver.Options{Handler: app.Handler()},
		OnStartup: func(ctx context.Context) {
			log.Printf("[desktop] ARTEX-HYDRA 桌面端已启动；Web 入口：http://%s", boot.DisplayAddr(listenAddr))
		},
	})
	if err != nil {
		log.Printf("[desktop] 窗口退出：%v", err)
	}

	// 停机顺序是三条不变量，别调换：
	//   1. 先停新请求与在跑回合（取消 ctx）；
	//   2. 等在跑的回合收尾，超时强断；
	//   3. 关库之后才轮到入口拉起的新核心开库。
	shutdown(agent.AbortShutdown)
	log.Println("shutting down...")
	grace := 5 * time.Second
	if restarting {
		grace = 20 * time.Second
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
	_ = app.Shutdown(shutdownCtx)
	cancel()
	if restarting {
		return selfupdate.ExitForRestart("apply")
	}
	return 0
}

// watchSetupToken 在首运行（管理员密码未设）时把设置令牌写入 setup-info.txt。
//
// 令牌由 SPA 首屏的 /api/auth/status 触发签发；桌面端没有控制台，用户只能从
// 文件里读到它。最多轮询 30 秒（SPA 首屏一定会请求状态），令牌一旦出现立即落盘。
func watchSetupToken(app *boot.App) {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if tok := app.SetupToken(); tok != "" {
			writeSetupInfoFile(tok)
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// writeSetupInfoFile 把首运行设置令牌与指引写入发行根目录的 setup-info.txt。
func writeSetupInfoFile(token string) {
	path := filepath.Join(config.BaseDir(), "setup-info.txt")
	content := fmt.Sprintf(`ARTEX-HYDRA 桌面端首次运行
==========================

管理员密码尚未设置。请在窗口内的 /setup 页面完成初始化，
并在「设置令牌」栏输入下面的值：

    %s

该令牌只出现在本文件与服务端日志中，不会通过网络传输。
完成初始化后，本文件可以删除。
`, token)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		log.Printf("[auth] 写入 %s 失败：%v（令牌仍可在服务端日志中查看）", path, err)
		return
	}
	log.Printf("[auth] 首运行设置令牌已写入 %s", path)
}
