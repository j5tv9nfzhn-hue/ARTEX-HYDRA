//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// messageBox 用系统模态对话框把信息送到用户眼前。
//
// 桌面端是 GUI 子系统（-H windowsgui），没有控制台：任何"只往 stdout/stderr
// 打一行"的失败提示，双击启动的用户都看不见。启动守卫拒绝启动、单实例已被
// 占用、后端起不来这三种情况都必须能看见，所以统一走这里。
func messageBox(title, body string, flags uint32) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	proc := user32.NewProc("MessageBoxW")

	text, err := windows.UTF16PtrFromString(body)
	if err != nil {
		return
	}
	caption, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	// hwnd 传 0：启动早期还没有窗口，用桌面作为父窗口即可。
	_, _, _ = proc.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(caption)), uintptr(flags))
}

func messageBoxError(title, body string) {
	const mbIconError, mbSetForeground, mbTopmost = 0x00000010, 0x00010000, 0x00040000
	messageBox(title, body, mbIconError|mbSetForeground|mbTopmost)
}

func messageBoxInfo(title, body string) {
	const mbIconInfo, mbSetForeground, mbTopmost = 0x00000040, 0x00010000, 0x00040000
	messageBox(title, body, mbIconInfo|mbSetForeground|mbTopmost)
}
