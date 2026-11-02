"use client";

import * as React from "react";

import { useViewStore } from "@/stores/views/view-store";

/**
 * 全应用「行 → 面板」委托器（仅 v2 生效）。
 *
 * 所有列表页的详情链接都是 `/xxx/detail?id=<id>` 这一约定，因此不必逐个改
 * 41 个页面文件：在 document 上做一次事件委托，v2 下把这类点击从「路由跳转」
 * 改写成「打开右侧详情面板」，URL 不变 —— 这正是 STRIX 的下钻模型。
 *
 * 安全边界：
 *   - 只在 `<html data-ui="v2">` 时生效，v1 零影响；
 *   - 放行新标签页/中键/修饰键点击（保留"整页打开"能力）；
 *   - 放行已被 preventDefault 的点击，不抢其它组件的交互；
 *   - 解析不出 kind 或 id 时原样跳转（降级而非拦截）。
 */
export function RowPanelBinder() {
  const openPanel = useViewStore((s) => s.openPanel);

  React.useEffect(() => {
    if (typeof document === "undefined") return;

    const onClick = (event: MouseEvent) => {
      if (document.documentElement.getAttribute("data-ui") !== "v2") return;
      if (event.defaultPrevented || event.button !== 0) return;
      if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;

      const target = event.target as HTMLElement | null;
      const anchor = target?.closest?.('a[href*="/detail"]') as HTMLAnchorElement | null;
      if (!anchor) return;

      let url: URL;
      try {
        url = new URL(anchor.href, window.location.origin);
      } catch {
        return;
      }

      const matched = url.pathname.match(/\/([a-z-]+)\/detail\/?$/);
      const id = url.searchParams.get("id");
      if (!matched || !id) return;

      event.preventDefault();
      openPanel(matched[1], id);
    };

    document.addEventListener("click", onClick);
    return () => document.removeEventListener("click", onClick);
  }, [openPanel]);

  return null;
}
