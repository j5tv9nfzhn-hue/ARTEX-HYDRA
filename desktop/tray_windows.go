//go:build windows

package main

// 托盘（关闭窗口 = 收进托盘）。
//
// Wails V2 自己没有托盘能力，这里用 fyne.io/systray：它的 Windows 后端是纯
// syscall 的 Shell_NotifyIcon 实现，不额外拉 cgo 依赖；托盘跑在自己的消息循环
// 里（独立 goroutine），与 Wails 主线程的窗口消息循环互不干扰。
//
// 语义是刻意保守的：
//   - 托盘就绪后，"关闭窗口"= 收进托盘，后端与正在跑的回合都不中断；
//   - 真正的退出只有两条：托盘菜单的「退出」与一键更新触发的重启。两者都先调
//     markQuit()，否则 OnBeforeClose 会把窗口又藏起来，导致退不掉；
//   - 托盘起不来（极少见）就退回"关闭即退出"：绝不把用户关进一个看不见的窗口。

import (
	_ "embed"
	"context"
	"log"
	"sync"
	"sync/atomic"

	"fyne.io/systray"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed assets/artex-hydra.ico
var trayIconICO []byte

var (
	trayOnce     sync.Once
	trayUp       atomic.Bool
	trayQuitting atomic.Bool
)

// trayActive 报告托盘是否已经就绪（只有就绪后才改"关闭"的语义）。
func trayActive() bool { return trayUp.Load() }

// trayIsQuitting 报告本次退出是否已由托盘菜单或一键更新发起。
func trayIsQuitting() bool { return trayQuitting.Load() }

// markQuit 标记"这次是真的要退出"，之后的 OnBeforeClose 不再藏窗口。
func markQuit() { trayQuitting.Store(true) }

// startTray 起托盘。失败/不可用只是没有托盘，窗口照常用。
func startTray(ctx context.Context) {
	trayOnce.Do(func() {
		go systray.Run(func() { onTrayReady(ctx) }, func() { trayUp.Store(false) })
	})
}

func onTrayReady(ctx context.Context) {
	systray.SetIcon(trayIconICO)
	systray.SetTooltip("ARTEX-HYDRA（关闭窗口会收在这里）")
	show := systray.AddMenuItem("显示主窗口", "把窗口调回前台")
	quit := systray.AddMenuItem("退出 ARTEX-HYDRA", "退出程序，窗口与后端一并结束")
	trayUp.Store(true)
	log.Printf("[desktop] 托盘已就绪：关闭窗口会收进托盘，退出请用托盘菜单")
	go func() {
		for {
			select {
			case <-show.ClickedCh:
				wailsruntime.WindowShow(ctx)
				wailsruntime.WindowUnminimise(ctx)
				// 置顶再取消：纯粹为了把窗口抢到前台（Windows 对后台进程
				// 的 SetForegroundWindow 有限制，这一步是常见绕法）。
				wailsruntime.WindowSetAlwaysOnTop(ctx, true)
				wailsruntime.WindowSetAlwaysOnTop(ctx, false)
			case <-quit.ClickedCh:
				markQuit()
				systray.Quit()
				wailsruntime.Quit(ctx)
				return
			}
		}
	}()
}
