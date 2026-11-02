"use client";

import * as React from "react";

import Link from "next/link";
import { usePathname } from "next/navigation";

import { PanelLeftClose, PanelLeftOpen } from "lucide-react";

import { NotificationCenter } from "@/app/(main)/_components/notification-center";
import { SearchDialog } from "@/app/(main)/_components/sidebar/search-dialog";
import { ThemeSwitcher } from "@/app/(main)/_components/sidebar/theme-switcher";
import { UpdateBadge } from "@/app/(main)/_components/update-badge";
import { UINotifier } from "@/components/ui-notifier";
import { serverSource, useResource } from "@/data/serverSource";
import { cn } from "@/lib/utils";
import { useViewStore, type V2View } from "@/stores/views/view-store";
import { getNavItems } from "@/ui/nav";
import { UIVersionSwitcher } from "@/ui/version-switcher";

import { PanelHost } from "./panel-host";
import { RowPanelBinder } from "./row-panel-binder";

import type { AppShellProps } from "../v1/app-shell-v1";

/**
 * v2 外壳 —— 对齐 STRIX 的单列 rail + 面板下钻模型。
 *
 * 与 v1 的关键差异：
 *   1. 不依赖 shadcn `SidebarProvider`（v1 的骨架），自带 rail 与内容区；
 *   2. 导航取 `getNavItems("v2")`：5 个一等域，其余下沉为二级；
 *   3. 下钻用**面板**而非路由：选中实体时开右侧面板（`useViewStore`），
 *      不再靠新增 URL 表达层级 —— 这正是 STRIX 只有 6 个 View 的原因；
 *   4. 取数统一走 `serverSource`（后端零改动）。
 *
 * 路由与页面内容仍与 v1 共用（双轨"共进"）：v2 换的是骨架，不是重写 41 个页面。
 */

const VIEW_BY_NAV_ID: Record<string, V2View> = {
  dashboard: "overview",
  runs: "runs",
  findings: "findings",
  intercept: "intercept",
  settings: "settings",
};

/** 静态导出带尾斜杠，比较前先归一化。 */
function normalizePath(pathname: string): string {
  return pathname.replace(/\/+$/, "") || "/";
}

function isActive(pathname: string, url?: string): boolean {
  if (!url) return false;
  const current = normalizePath(pathname);
  const target = normalizePath(url);
  return current === target || current.startsWith(`${target}/`);
}

export function AppShellV2({ children }: AppShellProps) {
  const pathname = usePathname();
  const [collapsed, setCollapsed] = React.useState(false);
  const [expandedId, setExpandedId] = React.useState<string | null>(null);

  const setView = useViewStore((s) => s.setView);
  const panelOpen = useViewStore((s) => s.panelOpen);

  const groups = getNavItems("v2");
  const items = groups[0]?.items ?? [];

  const health = useResource(() => serverSource.health(), "health");
  const version = (health.data?.version ?? "").replace(/^v(?=\d)/, "");

  return (
    <div data-ui="v2" className="flex min-h-screen w-full">
      <aside
        className={cn(
          "sticky top-0 flex h-screen shrink-0 flex-col overflow-hidden border-r transition-[width] duration-200",
          collapsed ? "w-0" : "w-60",
        )}
      >
        <div className="flex h-12 items-center gap-2 px-3">
          <Link prefetch={false} href="/dashboard" className="flex items-center gap-2">
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src="/logo.png" alt="ARTEX-HYDRA" width={28} height={28} className="shrink-0" />
            <span className="font-semibold text-sm">ARTEX-HYDRA</span>
          </Link>
        </div>
        <nav className="flex-1 overflow-y-auto px-2 py-2">
          {items.map((item) => {
            const Icon = item.icon;
            const hasChildren = Array.isArray(item.subItems) && item.subItems.length > 0;
            const expanded = expandedId === item.id;
            const active = isActive(pathname, "url" in item ? item.url : undefined);
            return (
              <div key={item.id} className="mb-0.5">
                {hasChildren ? (
                  <button
                    type="button"
                    onClick={() => setExpandedId(expanded ? null : item.id)}
                    className={cn(
                      "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm transition-colors",
                      active ? "bg-accent text-accent-foreground" : "hover:bg-accent/50",
                    )}
                  >
                    {Icon ? <Icon className="size-4 shrink-0" /> : null}
                    <span className="truncate">{item.title}</span>
                  </button>
                ) : (
                  <Link
                    prefetch={false}
                    href={"url" in item && item.url ? item.url : "/dashboard"}
                    onClick={() => VIEW_BY_NAV_ID[item.id] && setView(VIEW_BY_NAV_ID[item.id])}
                    className={cn(
                      "flex items-center gap-2 rounded-md px-2 py-1.5 text-sm transition-colors",
                      active ? "bg-accent text-accent-foreground" : "hover:bg-accent/50",
                    )}
                  >
                    {Icon ? <Icon className="size-4 shrink-0" /> : null}
                    <span className="truncate">{item.title}</span>
                  </Link>
                )}
                {hasChildren && expanded ? (
                  <div className="mt-0.5 ml-4 border-l pl-2">
                    {item.subItems?.map((sub) => {
                      const SubIcon = sub.icon;
                      return (
                        <Link
                          key={sub.id}
                          prefetch={false}
                          href={sub.url}
                          className={cn(
                            "flex items-center gap-2 rounded-md px-2 py-1 text-xs transition-colors",
                            isActive(pathname, sub.url)
                              ? "bg-accent text-accent-foreground"
                              : "text-muted-foreground hover:bg-accent/50",
                          )}
                        >
                          {SubIcon ? <SubIcon className="size-3.5 shrink-0" /> : null}
                          <span className="truncate">{sub.title}</span>
                        </Link>
                      );
                    })}
                  </div>
                ) : null}
              </div>
            );
          })}
        </nav>
        <div className="flex items-center justify-between gap-1 border-t px-2 py-2">
          <UIVersionSwitcher />
          <ThemeSwitcher />
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center gap-2 border-b px-4">
          <button
            type="button"
            onClick={() => setCollapsed((v) => !v)}
            className="rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent"
            aria-label={collapsed ? "展开侧栏" : "收起侧栏"}
          >
            {collapsed ? <PanelLeftOpen className="size-4" /> : <PanelLeftClose className="size-4" />}
          </button>
          <SearchDialog />
          <div className="ml-auto flex items-center gap-2">
            {version ? (
              <span className="text-muted-foreground text-xs tabular-nums">v{version}</span>
            ) : null}
            <UpdateBadge />
            <NotificationCenter />
          </div>
        </header>

        <div className="flex min-h-0 flex-1 overflow-hidden">
          <main data-ui-body="v2" className="min-w-0 flex-1 overflow-x-auto p-4 md:p-6">
            {children}
          </main>
          {panelOpen ? <PanelHost /> : null}
        </div>
      </div>
      <UINotifier />
      <RowPanelBinder />
    </div>
  );
}
