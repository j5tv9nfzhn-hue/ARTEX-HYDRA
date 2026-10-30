"use client";

import * as React from "react";

import Link from "next/link";

import { ArrowUpRightIcon, BellIcon, ShieldAlertIcon, ShieldCheckIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { api } from "@/lib/api";
import type { Finding, InterceptPending } from "@/lib/types";
import { cn } from "@/lib/utils";

/**
 * 通知中心：顶栏铃铛。
 *
 * 这里不引第三个「通知表」——真正需要人处理的只有两件事：
 *   1. 等裁决的审批票（`/intercept/pending`，点开进审批详情，可当场裁决）；
 *   2. 新落库的高危发现（critical / high，点开进发现详情）。
 * 两路都是现成接口，这样铃铛上的数字永远等于「你现在有事要做」，
 * 而不是一条读了就消失的未读消息。
 *
 * 打开面板时才拉一次数据；关闭态不做轮询，避免在每一页都挂一个后台请求。
 */
const SEVERE = new Set(["critical", "high"]);
const MAX_EACH = 10;

type Translate = ReturnType<typeof useTranslations>;

function relTime(ts: string, t: Translate) {
  const ms = Date.parse(ts);
  if (!ms) return ts;
  const mins = Math.round((Date.now() - ms) / 60000);
  if (mins < 1) return t("justNow");
  if (mins < 60) return t("minsAgo", { n: mins });
  const hours = Math.round(mins / 60);
  if (hours < 24) return t("hoursAgo", { n: hours });
  return t("daysAgo", { n: Math.round(hours / 24) });
}

export function NotificationCenter() {
  const t = useTranslations("notificationCenter");
  const [open, setOpen] = React.useState(false);
  const [pending, setPending] = React.useState<InterceptPending[]>([]);
  const [severe, setSevere] = React.useState<Finding[]>([]);
  const [loading, setLoading] = React.useState(false);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const [p, f] = await Promise.all([
        api.interceptPending().catch(() => []),
        api.findings().catch(() => [] as Finding[]),
      ]);
      setPending(p);
      setSevere(
        f
          .filter((x) => SEVERE.has(x.severity))
          .sort((a, b) => Date.parse(b.ts) - Date.parse(a.ts))
          .slice(0, MAX_EACH),
      );
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    if (open) void load();
  }, [open, load]);

  const count = pending.length;

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="relative size-8"
          aria-label={t("aria", { count })}
          title={t("aria", { count })}
        >
          <BellIcon className="size-4" />
          {count > 0 ? (
            <span className="absolute -top-0.5 -right-0.5 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-destructive px-1 font-semibold text-xs text-destructive-foreground tabular-nums">
              {count > 99 ? "99+" : count}
            </span>
          ) : null}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-[22rem] gap-0 p-0">
        <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
          <span className="font-medium text-xs">{t("title")}</span>
          <button
            type="button"
            onClick={() => void load()}
            className="text-muted-foreground text-xs hover:text-foreground"
            disabled={loading}
          >
            {loading ? t("loading") : t("refresh")}
          </button>
        </div>

        <div className="max-h-[26rem] overflow-y-auto">
          <div className="flex items-center gap-1.5 px-3 pt-2 pb-1">
            <ShieldCheckIcon className="size-3.5 text-amber-500" />
            <span className="font-medium text-xs">{t("pending")}</span>
            <span className="text-xs text-muted-foreground tabular-nums">{pending.length}</span>
            <span className="ml-auto">
              <Link href="/system/intercept/approvals" className="text-xs text-primary hover:underline">
                {t("all")}
              </Link>
            </span>
          </div>
          {pending.length === 0 ? (
            <p className="px-3 py-2 text-xs text-muted-foreground">{t("pendingEmpty")}</p>
          ) : (
            pending.slice(0, MAX_EACH).map((row) => (
              <Link
                key={row.id}
                href={`/system/intercept/approvals/detail?id=${row.id}`}
                className="flex items-start gap-2 px-3 py-1.5 hover:bg-accent/40"
                onClick={() => setOpen(false)}
              >
                <span className="mt-1 size-1.5 shrink-0 rounded-full bg-amber-500" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-mono text-xs">{row.tool_name}</span>
                  <span className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
                    <span className="truncate">{row.agent_name}</span>
                    <span className="shrink-0">{relTime(row.created_at, t)}</span>
                  </span>
                </span>
                <ArrowUpRightIcon className="mt-0.5 size-3 shrink-0 text-muted-foreground" />
              </Link>
            ))
          )}

          <div className="mt-1 flex items-center gap-1.5 border-t px-3 pt-2 pb-1">
            <ShieldAlertIcon className="size-3.5 text-rose-500" />
            <span className="font-medium text-xs">{t("severe")}</span>
            <span className="text-xs text-muted-foreground tabular-nums">{severe.length}</span>
            <span className="ml-auto">
              <Link href="/function/findings" className="text-xs text-primary hover:underline">
                {t("all")}
              </Link>
            </span>
          </div>
          {severe.length === 0 ? (
            <p className="px-3 py-2 text-xs text-muted-foreground">{t("severeEmpty")}</p>
          ) : (
            severe.map((f) => (
              <Link
                key={f.finding_id ?? f.id}
                href={`/function/findings/detail?id=${encodeURIComponent(f.finding_id ?? f.id)}`}
                className="flex items-start gap-2 px-3 py-1.5 hover:bg-accent/40"
                onClick={() => setOpen(false)}
              >
                <span
                  className={cn(
                    "mt-1 size-1.5 shrink-0 rounded-full",
                    f.severity === "critical" ? "bg-rose-600" : "bg-rose-400",
                  )}
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-xs">{f.name || f.vulnclass || f.summary}</span>
                  <span className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
                    <span className="truncate">
                      {f.task_description || (f.task_id ? `#${f.task_id}` : "—")}
                    </span>
                    <span className="shrink-0">{relTime(f.ts, t)}</span>
                  </span>
                </span>
                <ArrowUpRightIcon className="mt-0.5 size-3 shrink-0 text-muted-foreground" />
              </Link>
            ))
          )}
        </div>

        <p className="border-t px-3 py-1.5 text-[10.5px] text-muted-foreground">{t("foot")}</p>
      </PopoverContent>
    </Popover>
  );
}
