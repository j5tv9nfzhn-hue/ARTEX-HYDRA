package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLoopbackRequest 守住「跳过初始化」的唯一一道门。
//
// 这条捷径免令牌、免密码，只要判定放宽一点就是远程接管管理员账户的入口，
// 所以判定条件本身必须被钉死：TCP 对端是回环 **且** 没有任何转发头。
func TestLoopbackRequest(t *testing.T) {
	cases := []struct {
		name string
		addr string
		hdr  map[string]string
		want bool
	}{
		{name: "IPv4 回环", addr: "127.0.0.1:5000", want: true},
		{name: "IPv6 回环", addr: "[::1]:5000", want: true},
		{name: "局域网地址", addr: "192.168.1.9:5000", want: false},
		{name: "公网地址", addr: "203.0.113.7:5000", want: false},
		{name: "XFF 伪造", addr: "127.0.0.1:5000", hdr: map[string]string{"X-Forwarded-For": "1.2.3.4"}, want: false},
		{name: "反代只加 Proto", addr: "127.0.0.1:5000", hdr: map[string]string{"X-Forwarded-Proto": "https"}, want: false},
		{name: "X-Real-IP", addr: "127.0.0.1:5000", hdr: map[string]string{"X-Real-IP": "1.2.3.4"}, want: false},
		{name: "Forwarded", addr: "127.0.0.1:5000", hdr: map[string]string{"Forwarded": "for=1.2.3.4"}, want: false},
		{name: "Via", addr: "127.0.0.1:5000", hdr: map[string]string{"Via": "1.1 nginx"}, want: false},
		{name: "CF 客户端 IP", addr: "127.0.0.1:5000", hdr: map[string]string{"CF-Connecting-IP": "1.2.3.4"}, want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/auth/skip", nil)
			r.RemoteAddr = c.addr
			for k, v := range c.hdr {
				r.Header.Set(k, v)
			}
			if got := loopbackRequest(r); got != c.want {
				t.Fatalf("loopbackRequest(%s, %v) = %v, want %v", c.addr, c.hdr, got, c.want)
			}
		})
	}
}
