//go:build !windows

package boot

// EnableUTF8Console 在 Linux / macOS 上无需处理：终端默认就是 UTF-8，
// 没有 Windows 控制台代码页的问题。保留入口使调用方无需按平台分支。
func EnableUTF8Console() {}
