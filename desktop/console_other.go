//go:build !windows

package main

// 非 Windows 平台没有"GUI 子系统"这回事：从终端启动就继承终端，
// 从桌面启动器启动也不会有弹窗问题。空操作。
func attachParentConsole() {}
