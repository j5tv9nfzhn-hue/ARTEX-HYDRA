package config

import "strings"

// PermitLanAddr 是 -lan 开关使用的地址：监听全部网卡（IPv4+IPv6）。
// 别的设备只能通过局域网访问服务，而服务默认只监听回环地址。
const PermitLanAddr = ":8787"

// ResolveListenAddr 把监听地址的四层优先级压成最终地址：
//
//	-addr 显式传参 > -lan 开关 > config.json/env(ARTEX_LISTEN) > 127.0.0.1:8787
//
// 之所以把命令行放在最前面：临时开一次局域网不应该被配置文件里的旧值覆盖。
// 服务器版（cmd/artex）与桌面版（desktop）共用这一处，避免两边各写一遍
// 迟早出现「横幅说能连、页面说连不上」。
func ResolveListenAddr(flagAddr string, lan bool) string {
	if v := strings.TrimSpace(flagAddr); v != "" {
		return v
	}
	if lan {
		return PermitLanAddr
	}
	return ListenAddr()
}
