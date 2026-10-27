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
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	wails "github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

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
	// 与服务器版一致：先把控制台切到 UTF-8（仅 Windows），导入/日志的中文
	// 才不会在 GBK 控制台上乱码。
	boot.EnableUTF8Console()

	// 监听地址的优先级与服务器版一致（-addr > -lan > config.json/env > 回环默认），
	// 共用 config.ResolveListenAddr 单点收口。
	var (
		addr      = flag.String("addr", "", "HTTP 监听地址（空：取 config.json 的 listen / ARTEX_LISTEN，再空则 127.0.0.1:8787；:8787 表示开放局域网）")
		lan       = flag.Bool("lan", false, "开放局域网访问（等于 -addr :8787），供同一网络下的浏览器访问")
		dataDir   = flag.String("data", filepath.Join(config.BaseDir(), "data"), "data directory for SQLite stores (default: data/ next to the executable)")
		proxy     = flag.String("proxy", "127.0.0.1:8788", "traffic recording proxy address (empty to disable)")
		resetPass = flag.Bool("reset-password", false, "重置管理员密码后退出；新密码取自 ARTEX_RESET_NEWPASS，未设置则交互输入")
		importOld = flag.String("import-old", "", "从旧版 WEB 端（artex-rea）安装目录导入数据后退出：artex-hydra.exe -import-old \"C:\\path\\to\\旧目录\"")
	)
	flag.Parse()
	listenAddr := config.ResolveListenAddr(*addr, *lan)

	// 桌面端是 GUI 子系统：Windows 上不会分配控制台，双击也不会弹黑窗。
	// 但 -reset-password 与 -import-old 是一次性命令行操作，用户是在终端里跑
	// 它们、并指望看到输出的。这两种模式先挂回父进程的控制台（有其他平台
	// 实现，非 Windows 上是空操作），输出才能落到用户眼前。
	if *resetPass || *importOld != "" {
		attachParentConsole()
	}

	// 旧版数据导入是一次性离线操作：不占端口、不跑自更新、不起 GUI，
	// 排在所有启动步骤之前（与 -reset-password 同级）。
	if *importOld != "" {
		return runImportOld(*importOld)
	}

	// 密码重置是一次性离线操作：不占端口、不跑自更新、不起后台 worker，
	// 排在所有启动步骤之前（与服务器版一致）。
	if *resetPass {
		return boot.ResetPassword()
	}

	// 启动守卫：拒绝从升级临时文件（.new/.old/.failed/.staged/.part…）启动。
	if err := selfupdate.RefuseUnsafeExecutable(); err != nil {
		// 没有控制台（Windows GUI 子系统）时 stderr 是无效句柄：一条只写
		// stderr 的错误等于「双击后什么都不发生」。桌面端必须弹窗说明。
		messageBoxError("ARTEX-HYDRA 无法启动", err.Error())
		return 2
	}

	// 版本化布局的入口转发：pointer.json 指向的核心程序不是自己时，交给它执行。
	if code, handed := selfupdate.Handoff(); handed {
		return code
	}

	exeDir := config.BaseDir()

	server.BuildVersion = version
	// 进程级日志落盘：GUI 子系统没有控制台，stderr 写不进任何地方。
	// 顺序在 StartLogCapture 之前——sink 启动时就把透传目标换成这里开的文件。
	stopLogFile := setupDesktopLog(exeDir)
	defer stopLogFile()
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
			// 桌面端没有控制台，这条提示只能靠弹窗送达：否则第二次双击
			// 看起来就是「点了没反应」。
			messageBoxInfo("ARTEX-HYDRA 已在运行",
				fmt.Sprintf("桌面端已经开着：http://%s\n\n本次启动直接退出，没有动那个实例。\n要停止它，请回到它的窗口关闭。",
					boot.DisplayAddr(listenAddr)))
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
		// 桌面端不登录：窗口是用户在本机亲手启动的进程，再输一次账号密码不会
		// 增加任何信任边界——那是 Web 端的形态。服务器版（cmd/artex）不走这里，
		// 账号密码登录原样保留。
		DisableAuth: true,
	})
	if err != nil {
		log.Printf("启动失败：%v", err)
		messageBoxError("ARTEX-HYDRA 启动失败", fmt.Sprintf("%v\n\n详细日志：%s", err, desktopLogPath(exeDir)))
		return 1
	}

	// 升级"活下来"的确认（同服务器版）：延迟核对版本号与 /api/health，
	// 不通过就把台账置为 failed，由下一次启动的 Bootstrap 累计失败次数并回滚。
	settle := time.AfterFunc(selfupdate.SettleDelay, func() {
		if err := selfupdate.Settle(version, listenAddr); err != nil {
			log.Printf("[update] %v", err)
		}
	})
	defer settle.Stop()

	// 打开原生窗口并阻塞，直到窗口关闭或更新流程请求重启。
	return runDesktopWindow(ctx, app, listenAddr, shutdown)
}

// 窗口默认尺寸。
//
// 依据（都是公开的平台/行业尺寸约定，不是拍脑袋）：
//
//   - 微软 Win32 UX 指南：支持 800×600 的最小有效分辨率，可调大小窗口
//     "针对 1024×768 的有效分辨率优化"；并且明确要求"避免默认就最大化或
//     接近最大化"的恢复尺寸。→ 所以最小尺寸取 1024×640（1024×768 减去
//     标题栏与任务栏），默认值取明显小于全屏的一档。
//   - 微软"屏幕尺寸与断点"文档把 1008px 以上划为 Large 类，给出的典型窗口
//     尺寸是 1024×640 / 1366×768 / 1920×1080。→ 桌面端的默认宽度落在
//     这个区间里，1280 是 1366 与 1024 之间的居中档。
//   - 1366×768 是笔记本上最常见的分辨率之一，其可用高度只有 768 − 任务栏
//     48 − 标题栏 32 ≈ 688。→ 800 高的默认值在 768 屏上必然出界，所以
//     默认值只作"上限愿望"，运行时按可用区域夹紧。
//
// 折算结果：1280×800 的默认值在大屏上原样使用，在 1366×768 上落到
// ≈1280×688，任何情况下都不出界、也不再"开窗就要拖"。
const (
	windowDefaultWidth  = 1280
	windowDefaultHeight = 800
	windowMinWidth      = 1024
	windowMinHeight     = 640
	// 桌面留白：左右各留一点，避免窗口贴边导致阴影/调整边缘不可点。
	windowScreenSideMargin = 40
	// 纵向预留：典型标题栏 32 + 任务栏 48 + 少量留白。
	windowScreenVerticalReserve = 96
)

// fitWindowToScreen 把窗口夹到当前显示器的可用区域内并居中。
//
// Wails 的 Width/Height 只是创建窗口时的初值，不会替我们考虑"默认值比屏幕
// 还大"的情况——1440×900 在 1366×768 的笔记本上会有一截永远点不到。
// 这里在窗口还没显示时按逻辑像素（Wails 的尺寸单位）重新算一次。
func fitWindowToScreen(ctx context.Context) {
	screens, err := wailsruntime.ScreenGetAll(ctx)
	if err != nil || len(screens) == 0 {
		// 拿不到屏幕信息（极少见）：保持默认值，至少居中，比堆在角落好。
		wailsruntime.WindowCenter(ctx)
		return
	}
	// 优先当前窗口所在屏，其次主屏，最后退到第一块。
	sw, sh := 0, 0
	for _, sc := range screens {
		if sc.IsCurrent {
			sw, sh = sc.Size.Width, sc.Size.Height
			break
		}
	}
	if sw <= 0 || sh <= 0 {
		for _, sc := range screens {
			if sc.IsPrimary {
				sw, sh = sc.Size.Width, sc.Size.Height
				break
			}
		}
	}
	if sw <= 0 || sh <= 0 {
		sw, sh = screens[0].Size.Width, screens[0].Size.Height
	}
	if sw <= 0 || sh <= 0 {
		wailsruntime.WindowCenter(ctx)
		return
	}

	maxW := sw - windowScreenSideMargin
	maxH := sh - windowScreenVerticalReserve
	w, h := windowDefaultWidth, windowDefaultHeight
	if maxW > 0 && w > maxW {
		w = maxW
	}
	if maxH > 0 && h > maxH {
		h = maxH
	}
	// 屏幕比最小尺寸还小时，宁可小于 MinWidth 也要留在屏内：能看见半屏，
	// 比一半按钮在屏幕外可用。
	if w < 1 {
		w = sw
	}
	if h < 1 {
		h = sh
	}

	wailsruntime.WindowSetSize(ctx, w, h)
	wailsruntime.WindowCenter(ctx)
	log.Printf("[desktop] 窗口尺寸 %d×%d（屏幕 %d×%d）", w, h, sw, sh)
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

	err := wails.Run(&options.App{
		Title: "ARTEX-HYDRA",
		// 尺寸取值与依据见 windowDefaultWidth 上方的注释；这里只是初值，
		// OnStartup 会按当前显示器再夹一次（屏幕比默认值小时不留出界）。
		Width:     windowDefaultWidth,
		Height:    windowDefaultHeight,
		MinWidth:  windowMinWidth,
		MinHeight: windowMinHeight,
		// 先隐藏窗口，等尺寸适配完再显示：否则会先按默认尺寸画一帧、
		// 再缩到屏幕内，肉眼可见地"跳一下"。
		StartHidden:      true,
		BackgroundColour: &options.RGBA{R: 13, G: 13, B: 18, A: 1},
		// 窗口内渲染的正是进程内 server 的同一套 SPA：AssetServer 的 Assets 为空，
		// 全部请求（静态页 / API / SSE）都转发给 srv.Handler()，
		// 与浏览器访问 http://127.0.0.1:8787 完全同源同行为。
		// markDesktopShell 只是给文档响应加一个 cookie，让同一份前端代码知道
		// 自己跑在桌面壳里（见 web/src/lib/platform.ts），不改变任何响应体。
		AssetServer: &assetserver.Options{Handler: markDesktopShell(app.Handler())},
		OnStartup: func(ctx context.Context) {
			fitWindowToScreen(ctx)
			wailsruntime.WindowShow(ctx)
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

// desktopLogPath 是桌面端进程日志的位置。
func desktopLogPath(exeDir string) string {
	return filepath.Join(exeDir, "logs", "desktop.log")
}

// setupDesktopLog 把标准日志同时写到 <安装目录>/logs/desktop.log。
//
// 桌面端在 Windows 上链接为 GUI 子系统（-H windowsgui），进程没有控制台，
// os.Stderr 是无效句柄：不落盘的话，双击启动后出问题时**一点线索都没有**。
// 顺序刻意是「先文件、再 stderr」——GUI 子系统下 stderr 写失败不会连累文件。
// 返回的函数用于关闭文件，defer 调用即可。
func setupDesktopLog(exeDir string) func() {
	path := desktopLogPath(exeDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return func() {}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return func() {}
	}
	server.SetPassthrough(io.MultiWriter(f, os.Stderr))
	// stdout 同样没有去处：fmt.Printf 的行（启动提示、单实例提示）也一并进文件。
	os.Stdout = f
	return func() { _ = f.Close() }
}
