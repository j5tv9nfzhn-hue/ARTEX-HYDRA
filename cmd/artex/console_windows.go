//go:build windows

package main

import (
	"golang.org/x/sys/windows"
)

// consoleAttached 报告 stdout 是否真的接在一个 Windows 控制台上。
//
// 被重定向到文件/管道（计划任务、`start.bat > run.log`、CI）时返回 false ——
// 那些场景下"留住窗口"没有意义，还会把自动化卡住。
func consoleAttached() bool {
	if windows.Stdout == 0 {
		return false
	}
	var mode uint32
	return windows.GetConsoleMode(windows.Stdout, &mode) == nil
}
