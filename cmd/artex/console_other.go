//go:build !windows

package main

// consoleAttached 在非 Windows 平台恒为 false：Unix 由 start.sh 负责「Ctrl-C 停止」，
// 不需要、也不该在退出前停几秒。
//
// 这个符号对 linux/darwin 构建是必需的：holdConsole（main.go）不带构建标签、全平台编译，
// 而 Windows 侧的实现在 console_windows.go（//go:build windows）。缺了这一半，
// GOOS=linux/darwin 会直接报 undefined: consoleAttached，被 release.yml 的
// gate → go build 拦下。
//
// 原同文件中的 enableUTF8Console 已随 Windows 侧实现下线（4c79a759）成为死代码，不再保留。
func consoleAttached() bool { return false }
