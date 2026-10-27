/**
 * 平台边界（唯一一处「前端怎么知道自己跑在哪个壳里」的判定）。
 *
 * 同一份静态导出产物会被两个宿主加载：
 *
 *   - 服务器版 artex-rea（cmd/artex）：浏览器访问，账号密码登录；
 *   - 桌面版 artex-hydra（desktop/）：Wails 原生窗口，窗口内不需要登录。
 *
 * 判定优先用桌面壳给文档响应加的 cookie（desktop/shell.go 的 markDesktopShell），
 * 其次是 Wails 宿主特征（见 isWailsHost）。只有本文件读这两个信号——以后要加
 * 「平板端」「内嵌 iframe」等宿主，改这里一处即可，不要在页面里散落
 * `location.hostname === ...` 之类的猜测。
 *
 * 「窗口免登录」的边界（别写错）：免登录只对窗口成立，因为只有窗口的请求带得上
 * 后端的进程内标记（server.authExempt）。桌面端一旦用 `-lan` 绑定局域网，同一个
 * 后端用浏览器（本机或局域网）访问照样要账号密码——那是服务器版的语义，不是缺陷。
 */
const SHELL_COOKIE = "artex_shell";

function readCookie(name: string): string {
  if (typeof document === "undefined") return "";
  for (const part of document.cookie.split(";")) {
    const [k, ...rest] = part.trim().split("=");
    if (k === name) return decodeURIComponent(rest.join("="));
  }
  return "";
}

/**
 * 是否由 Wails 桌面壳托管（不依赖 cookie 的兜底判定）。
 *
 * 为什么需要兜底：cookie 是桌面壳加在文档响应上的，但 webview 不一定采纳
 * Set-Cookie（自定义 scheme 下尤其如此），用户也可能清掉它。一旦丢了，
 * 窗口就会掉进登录页——这正是「启动 EXE 出现授权登录页」的那条路径。
 *
 * 宿主特征跑不掉，且服务器版/局域网访问里不可能出现：
 *   - Windows（WebView2）：页面源是 http://wails.localhost
 *   - macOS / Linux（WKWebView / webkit2gtk）：自定义 scheme，protocol 为 wails:
 */
function isWailsHost(): boolean {
  if (typeof window === "undefined") return false;
  const { protocol, hostname } = window.location;
  return protocol === "wails:" || hostname === "wails.localhost";
}

/** 是否运行在桌面原生窗口里。静态导出的预渲染阶段没有 window，一律按 false。 */
export const IS_DESKTOP = readCookie(SHELL_COOKIE) === "desktop" || isWailsHost();
