package agent

import "strings"

// proxyEnv 构造 Bash 子命令的出站代理环境；留空即直连（返回 nil）。
//
// 不回退进程自己的 HTTP_PROXY/HTTPS_PROXY：CHANGELOG 记过一次事故——运维在
// shell 里导出的代理会被子命令悄悄继承，流量去了哪在 UI 上看不出来。要代理
// 必须显式配置。
//
// ALL_PROXY / all_proxy 是必需的：curl 只从这两个变量读 socks5，只设
// HTTP(S)_PROXY 会让 curl 直连，抓包与审计形同虚设。大小写两份都给，
// 与 tools_pentest.go 的读取顺序保持一致。
//
// CA 只在录制态注入：MITM 的 CA 是本机私有的，塞给不需要它的命令只会让
// TLS 握手失败。
func proxyEnv(proxyAddr, proxyCACert string) []string {
	addr := strings.TrimSpace(proxyAddr)
	if addr == "" {
		return nil
	}
	env := []string{
		"HTTP_PROXY=" + addr,
		"HTTPS_PROXY=" + addr,
		"ALL_PROXY=" + addr,
		"all_proxy=" + addr,
		"http_proxy=" + addr,
		"https_proxy=" + addr,
	}
	if ca := strings.TrimSpace(proxyCACert); ca != "" {
		env = append(env,
			"SSL_CERT_FILE="+ca,
			"CURL_CA_BUNDLE="+ca,
			"REQUESTS_CA_BUNDLE="+ca,
			"NODE_EXTRA_CA_CERTS="+ca,
		)
	}
	return env
}
