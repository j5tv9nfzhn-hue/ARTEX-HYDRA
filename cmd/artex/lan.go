package main

import (
	"fmt"
	"runtime"

	"github.com/Autumn-27/artex/config"
	"github.com/Autumn-27/artex/server"
)

// lanURLs 返回该监听地址在局域网里能被访问到的 URL。
//
// 真正的实现在 server.LanURLs：「系统 → 局域网连接」页算的是同一份东西，
// 两边不能各写一遍，否则迟早出现「横幅说能连、页面说连不上」。
func lanURLs(addr string) []string {
	return server.LanURLs(addr)
}

// printLanHint 把“手机该填什么”直接打在启动横幅下面。
// 不放进 printBanner：横幅本身有单测（TestPrintBannerLocalized）锁着格式。
func printLanHint(addr string) {
	urls := lanURLs(addr)
	if len(urls) == 0 {
		fmt.Printf("  手机端：当前只监听回环地址 %s，App 连不上。\n", addr)
		fmt.Print("          加 -lan（等于 -addr " + config.PermitLanAddr + "）重启即可开放局域网。\n\n")
		return
	}
	fmt.Print("  手机端连接（把地址填进 App 的「服务器地址」，密码用 PC 端登录密码）\n")
	for _, u := range urls {
		fmt.Printf("    %s\n", u)
	}
	if runtime.GOOS == "windows" {
		fmt.Print("  防火墙：首次连不上先放行入站端口，例如\n")
		fmt.Print("    netsh advfirewall firewall add rule name=\"ARTEX-HYDRA\" dir=in action=allow protocol=TCP localport=" + lanPort(addr) + "\n")
	}
	fmt.Print("  安全：只在可信局域网开放；隔离/公网场景请用 TLS 反向代理，不要把端口直接暴露到互联网。\n\n")
}

// lanPort 从监听地址里取端口，取不到就用默认的 8787。
func lanPort(addr string) string {
	return server.LanPort(addr)
}
