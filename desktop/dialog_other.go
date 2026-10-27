//go:build !windows

package main

import (
	"fmt"
	"os"
)

// 非 Windows 平台本来就从终端启动，直接写 stderr 就是最好的"弹窗"。
func messageBoxError(title, body string) {
	fmt.Fprintf(os.Stderr, "[%s] %s\n", title, body)
}

func messageBoxInfo(title, body string) {
	fmt.Printf("[%s] %s\n", title, body)
}
