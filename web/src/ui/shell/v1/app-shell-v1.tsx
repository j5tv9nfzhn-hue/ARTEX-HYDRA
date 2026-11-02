"use client";

import * as React from "react";

import { MainContent } from "@/app/(main)/_components/main-content";
import { AppSidebar } from "@/app/(main)/_components/sidebar/app-sidebar";
import { UINotifier } from "@/components/ui-notifier";
import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import type { SidebarCollapsible, SidebarVariant } from "@/lib/preferences/layout";
import { cn } from "@/lib/utils";

export interface AppShellProps {
  variant: SidebarVariant;
  collapsible: SidebarCollapsible;
  defaultOpen: boolean;
  children: React.ReactNode;
}

/**
 * v1 外壳 —— 与改造前**逐像素一致**。
 *
 * 内容是从 `app/(main)/layout.tsx` 原样搬过来的，未做任何视觉改动。
 * 双轨并存的前提就是老骨架零回归：v1 用户升级后看到的东西必须和昨天一样。
 */
export function AppShellV1({ variant, collapsible, defaultOpen, children }: AppShellProps) {
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
      {/* 全局应用内通知：挂在外壳层、内容区之外，保证任何页面都收得到。 */}
      <UINotifier />
    </SidebarProvider>
  );
}
