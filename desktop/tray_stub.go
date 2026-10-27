//go:build !windows

package main

import "context"

// 非 Windows 平台暂时不做托盘：
//
//   - macOS 要托盘必须把 systray 接进现有的 NSApplication 主循环
//     （fyne.io/systray 的 RunWithExternalLoop），否则会和 Wails 抢主线程；
//   - Linux 的托盘走 StatusNotifier/AppIndicator，依赖桌面环境，得逐个实测。
//
// 两条路都要真机验证才敢开，所以这里保持"没有托盘"的语义：关闭窗口即退出，
// 与改造前完全一致。要放开时只改这些函数与 main.go 的 OnBeforeClose 判据。

func trayActive() bool { return false }

func trayIsQuitting() bool { return false }

// markQuit 在非 Windows 上没有托盘要收，留同名空实现让 main.go 一处调用两端通用。
func markQuit() {}

func startTray(context.Context) {}
