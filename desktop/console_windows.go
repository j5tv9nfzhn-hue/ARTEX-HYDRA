//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// attachParentConsole 把本进程挂回"启动它的那个控制台"。
//
// 桌面端编译为 GUI 子系统（-H windowsgui）是为了双击时不弹黑窗；代价是
// 标准输出句柄无效。但 -reset-password / -import-old 是命令行用法，用户是
// 在终端里敲的，必须能看见输出——所以只在这两种模式下显式 AttachConsole
// 到父进程的控制台，并把 std 句柄重新指过去。
//
// 直接由 Explorer 双击（没有父控制台）时 AttachConsole 会失败，此时保持
// 静默即可：GUI 路径的错误由 messageBox 负责。
func attachParentConsole() {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	attach := kernel32.NewProc("AttachConsole")
	getStd := kernel32.NewProc("GetStdHandle")

	const attachParentProcess = ^uintptr(0) // ATTACH_PARENT_PROCESS = -1
	if r, _, _ := attach.Call(attachParentProcess); r == 0 {
		return
	}

	// STD_OUTPUT_HANDLE = -11, STD_ERROR_HANDLE = -12（以补码形式传给 Win32）。
	if h, _, _ := getStd.Call(^uintptr(10)); h != 0 && h != uintptr(windows.InvalidHandle) {
		os.Stdout = os.NewFile(h, "CONOUT$")
	}
	if h, _, _ := getStd.Call(^uintptr(11)); h != 0 && h != uintptr(windows.InvalidHandle) {
		os.Stderr = os.NewFile(h, "CONOUT$")
	}
}
