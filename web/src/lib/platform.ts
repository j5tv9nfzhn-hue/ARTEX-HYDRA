/**
 * 平台边界（唯一一处「前端怎么知道自己跑在哪个壳里」的判定）。
 *
 * 同一份静态导出产物会被两个宿主加载：
 *
 *   - 服务器版 artex-rea（cmd/artex）：浏览器访问，账号密码登录；
 *   - 桌面版 artex-hydra（desktop/）：Wails 原生窗口，窗口内不需要登录。
 *
 * 判定依据是桌面壳给文档响应加的 cookie（desktop/shell.go 的 markDesktopShell）。
 * 只有那一个 Go 文件会写它，前端也只有本文件读它——以后要加「平板端」「内嵌
 * iframe」等宿主，改这里一处即可，不要在页面里散落 `location.hostname === ...`
 * 之类的猜测。
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

/** 是否运行在桌面原生窗口里。静态导出的预渲染阶段没有 document，一律按 false。 */
export const IS_DESKTOP = readCookie(SHELL_COOKIE) === "desktop";
