"use client";

import type { ReactNode } from "react";
import * as React from "react";

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
import { UIShell } from "@/ui/shell";


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
    // 没有 token 就是要登录：纯 Web 部署下 auth_disabled 恒为 false，
    // 仍问一次后端是为了区分「未初始化」与「未登录」两种情形。
    api
      .authStatus()
      .then(() => {
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
    <UIShell variant={variant} collapsible={collapsible} defaultOpen={defaultOpen}>
      {children}
    </UIShell>
  );
}
