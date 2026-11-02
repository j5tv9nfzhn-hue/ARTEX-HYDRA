"use client";

import * as React from "react";

import { getClientCookie, setClientCookie } from "@/lib/cookie.client";

/**
 * UI 版本骨架开关（双轨共存共进）。
 *
 * 与 `skin` 的区别：
 *   - `skin`（classic / editorial）只换外观，作用在**同一套骨架**上；
 *   - `ui`（v1 / v2）换的是**骨架本身**：导航结构、外壳、页面级排版容器。
 *
 * 两者正交：v1 与 v2 各自都能配 classic / editorial 皮肤，用户可以
 * 「旧骨架 + 老皮肤」「新骨架 + 纸本」任意组合，切换即时生效、不丢状态。
 *
 * 之所以用 cookie 而不是 localStorage：外壳选择是 layout-critical 的，
 * 静态导出（`output: export`）下没有服务端，但 SSR 预渲染阶段必须能读到
 * 稳定值，否则首屏会闪一次旧骨架。做法与 `sidebar_variant` 一致。
 */

export type UIVersion = "v1" | "v2";

export const UI_VERSION_KEY = "artex_ui";

/** 默认 v1：现有用户升级后第一眼仍是熟悉的骨架，新骨架要自己打开。 */
export const DEFAULT_UI_VERSION: UIVersion = "v1";

export const UI_VERSIONS: readonly UIVersion[] = ["v1", "v2"];

function parseUIVersion(raw: string | null | undefined): UIVersion {
  return raw === "v2" ? "v2" : "v1";
}

const listeners = new Set<() => void>();

let cachedRaw: string | null | undefined;
let cached: UIVersion = DEFAULT_UI_VERSION;

function getSnapshot(): UIVersion {
  if (typeof document === "undefined") return DEFAULT_UI_VERSION;
  const raw = getClientCookie(UI_VERSION_KEY);
  if (raw === cachedRaw) return cached;
  cachedRaw = raw;
  cached = parseUIVersion(raw);
  return cached;
}

function getServerSnapshot(): UIVersion {
  return DEFAULT_UI_VERSION;
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** 当前 UI 骨架版本。SSR 阶段返回 v1，水合后由 cookie 校正。 */
export function useUIVersion(): UIVersion {
  return React.useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}

/** 切换骨架。写 cookie 后通知订阅者，无需刷新。 */
export function setUIVersion(next: UIVersion): void {
  setClientCookie(UI_VERSION_KEY, next, 365);
  cachedRaw = undefined;
  for (const listener of listeners) listener();
}

/** 非 hook 场景读取（如 layout 初始化）。SSR 安全。 */
export function readUIVersion(): UIVersion {
  return getSnapshot();
}
