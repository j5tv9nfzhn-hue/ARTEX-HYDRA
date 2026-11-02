"use client";

import * as React from "react";

import { useUIVersion } from "@/ui/version";

import { AppShellV1, type AppShellProps } from "./v1/app-shell-v1";
import { AppShellV2 } from "./v2/app-shell-v2";

/**
 * 骨架分发：按 `ui` 偏好在 v1 / v2 两套外壳之间切换。
 *
 * 两套外壳消费**同一份路由与页面内容**（children），因此双轨是"共进"的：
 * 修一个业务页面，两版同时生效；只有导航结构、外壳与页面级容器不同。
 * 默认 v1，老用户无感。
 */
export function UIShell(props: AppShellProps) {
  const version = useUIVersion();
  if (version === "v2") return <AppShellV2 {...props} />;
  return <AppShellV1 {...props} />;
}

export { AppShellV1, AppShellV2 };
export type { AppShellProps };
