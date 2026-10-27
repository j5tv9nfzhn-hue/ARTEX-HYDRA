"use client";

import type { ReactNode } from "react";
import * as React from "react";

import { AppSidebar } from "@/app/(main)/_components/sidebar/app-sidebar";
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import { UINotifier } from "@/components/ui-notifier";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";
import { getClientCookie } from "@/lib/cookie.client";
import {
  SIDEBAR_COLLAPSIBLE_VALUES,
  SIDEBAR_VARIANT_VALUES,
  type SidebarCollapsible,
  type SidebarVariant,
} from "@/lib/preferences/layout";
import { PREFERENCE_DEFAULTS } from "@/lib/preferences/preferences-config";
import { cn } from "@/lib/utils";

import { MainContent } from "./_components/main-content";

// Reads a layout-critical preference from the browser cookie (falls back to the
// default during static-export prerender where document is unavailable). Kept
// fully client-side so the app can be statically exported — no Server Actions /
// next/headers.
function readPref<T extends string>(key: string, allowed: readonly T[], fallback: T): T {
  if (typeof document === "undefined") return fallback;
  const value = getClientCookie(key);
  return value && (allowed as readonly string[]).includes(value) ? (value as T) : fallback;
}

export default function Layout({ children }: Readonly<{ children: ReactNode }>) {
  // Client-side auth gate — replaces the Next proxy/middleware that static export
  // disables. No token → 先问后端要不要凭据；确实要（服务器版 / 浏览器入口）才
  // 回 /login。render nothing until confirmed so no protected UI (or its API calls)
  // flashes for a logged-out visitor.
  const [authed, setAuthed] = React.useState(false);
  React.useEffect(() => {
    if (auth.getToken()) {
      setAuthed(true);
      return;
    }
    // 没有 token 不等于要登录：桌面壳的窗口本来就没有 token，也不该有。
    // 问一次后端「这条连接要不要凭据」——窗口的请求带进程内标记，会回
    // auth_disabled=true；浏览器（含桌面端 -lan 之后的局域网入口）为 false。
    // 这样即使 artex_shell cookie 丢了，窗口也不会掉进登录页。
    api
      .authStatus()
      .then((st) => {
        if (st.auth_disabled) {
          setAuthed(true);
          return;
        }
        // 客户端守卫认为未登录时，必须同时清掉 cookie：否则 proxy.ts 仅凭
        // “cookie 存在”就把我们从 /login 又重定向回主界面，与本守卫来回弹跳
        // 形成无限重定向 → 白屏（cookie 与 localStorage 不一致时触发）。
        auth.clearToken();
        window.location.href = "/login";
      })
      .catch(() => {
        auth.clearToken();
        window.location.href = "/login";
      });
  }, []);

  const defaultOpen = typeof document === "undefined" ? true : getClientCookie("sidebar_state") !== "false";
  const variant = readPref<SidebarVariant>(
    "sidebar_variant",
    SIDEBAR_VARIANT_VALUES,
    PREFERENCE_DEFAULTS.sidebar_variant,
  );
  const collapsible = readPref<SidebarCollapsible>(
    "sidebar_collapsible",
    SIDEBAR_COLLAPSIBLE_VALUES,
    PREFERENCE_DEFAULTS.sidebar_collapsible,
  );

  if (!authed) return null;

  return (
    <SidebarProvider
      defaultOpen={defaultOpen}
      style={
        {
          "--sidebar-width": "calc(var(--spacing) * 62)",
        } as React.CSSProperties
      }
    >
      <AppSidebar variant={variant} collapsible={collapsible} />
      <SidebarInset
        className={cn(
          "[html[data-content-layout=centered]_&>*]:mx-auto",
          "[html[data-content-layout=centered]_&>*]:w-full",
          "[html[data-content-layout=centered]_&>*]:max-w-screen-2xl",
          "peer-data-[variant=inset]:border",
          "[--dashboard-header-height:--spacing(12)]",
          "min-w-0 overflow-x-hidden",
        )}
      >
        <MainContent>{children}</MainContent>
      </SidebarInset>
      {/* 全局应用内通知：挂在 SidebarProvider 内、MainContent 外。
          MainContent 对任务详情页是 full-bleed 直接返回 children，只有挂在这一层
          才能保证「任何页面（含子页面）都收得到」。 */}
      <UINotifier />
    </SidebarProvider>
  );
}
