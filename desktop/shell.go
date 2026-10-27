package main

import (
	"net/http"
	"strings"

	"github.com/Autumn-27/artex/server"
)

// desktopShellCookie 是「本次文档响应来自桌面壳」的标记，供前端唯一的平台
// 边界模块（web/src/lib/platform.ts）读取。
//
// 为什么用 cookie 而不是注入 <script>：AssetServer 里跑的是已经静态导出的
// index.html，改写响应体要缓冲整页、还要重算 Content-Length；加一个响应头
// 就够，且不碰响应体。
//
// 只标记文档请求：/api/* 与 /mcp 是机器接口，不需要这个标记，也不该被改动。
const desktopShellCookie = "artex_shell"

func markDesktopShell(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 进程内标记：server.authExempt 只认它（请求头能被局域网客户端伪造，
		// context 值不能）。「窗口免登录」就落在这一个标记上。
		r = server.MarkDesktopShellRequest(r)
		if isDocumentRequest(r) {
			http.SetCookie(w, &http.Cookie{
				Name:     desktopShellCookie,
				Value:    "desktop",
				Path:     "/",
				SameSite: http.SameSiteLaxMode,
			})
		}
		next.ServeHTTP(w, r)
	})
}

// isDocumentRequest 判断这次请求是不是「浏览器/WebView 在取一个页面」。
func isDocumentRequest(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/"):
		return false
	case r.URL.Path == "/mcp":
		return false
	case strings.HasPrefix(r.URL.Path, "/_next/"):
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}
