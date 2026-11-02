"use client";

import * as React from "react";

import { api } from "@/lib/api";

/**
 * 单一数据出口（对齐 STRIX 的 `data/serverSource.ts`）。
 *
 * 后端零改动：这里只是把现有 `api` 与 SSE 收成一个出口，上层不再直接
 * import `api`，将来换传输实现时只改本文件。
 */
export const serverSource = api;

export interface ResourceOptions<T> {
  /** 轮询间隔（毫秒）。0 = 只取一次。对齐 STRIX 的 POLL_MS 模型。 */
  intervalMs?: number;
  /** false 时不发请求（等待依赖就绪）。 */
  enabled?: boolean;
  initialData?: T;
}

export interface ResourceResult<T> {
  data: T | undefined;
  error: Error | null;
  loading: boolean;
  refresh: () => void;
}

/**
 * 订阅式取数。`key` 变化时重新拉取；`intervalMs > 0` 时按间隔轮询，
 * 页面卸载或 key 变化时自动取消，不会泄漏定时器。
 */
export function useResource<T>(
  fetcher: () => Promise<T>,
  key: string,
  options: ResourceOptions<T> = {},
): ResourceResult<T> {
  const { intervalMs = 0, enabled = true, initialData } = options;

  const [data, setData] = React.useState<T | undefined>(initialData);
  const [error, setError] = React.useState<Error | null>(null);
  const [loading, setLoading] = React.useState(enabled);
  const [nonce, setNonce] = React.useState(0);

  const fetcherRef = React.useRef(fetcher);
  fetcherRef.current = fetcher;

  React.useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const run = async () => {
      try {
        const next = await fetcherRef.current();
        if (cancelled) return;
        setData(next);
        setError(null);
      } catch (e) {
        if (cancelled) return;
        setError(e instanceof Error ? e : new Error(String(e)));
      } finally {
        if (cancelled) return;
        setLoading(false);
        if (intervalMs > 0) timer = setTimeout(run, intervalMs);
      }
    };

    void run();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [key, enabled, intervalMs, nonce]);

  const refresh = React.useCallback(() => setNonce((n) => n + 1), []);

  return { data, error, loading, refresh };
}
