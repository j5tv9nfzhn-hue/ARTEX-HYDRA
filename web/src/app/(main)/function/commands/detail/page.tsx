"use client";

import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { Masthead } from "@/components/editorial";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { api } from "@/lib/api";
import type { CommandRecord } from "@/lib/types";
import { cn } from "@/lib/utils";

/**
 * 工具执行详情：一次调用的原文与输出。
 *
 * 地址：/function/commands/detail?id=<execution id>
 *
 * 数据从 `api.commands()` 里按 id 取 —— 后端没有单条执行的 REST 端点，
 * 而这张表的量级（一页 100 条）足够在客户端定位到具体一条；找不到时给「已滚出窗口」的落款，
 * 而不是假装页面不存在。
 */
function CommandDetailInner() {
  const t = useTranslations("commandsDetail");
  const tc = useTranslations("commands");
  const searchParams = useSearchParams();
  const id = Number(searchParams.get("id") ?? "");
  const taskId = searchParams.get("task") ?? "";

  const [record, setRecord] = React.useState<CommandRecord | null>(null);
  const [loaded, setLoaded] = React.useState(false);

  React.useEffect(() => {
    if (!Number.isFinite(id) || id <= 0) {
      setLoaded(true);
      return;
    }
    let alive = true;
    api
      .commands({ task: taskId || undefined, size: 200 })
      .then((r) => {
        if (!alive) return;
        setRecord(r.commands.find((c) => c.id === id) ?? null);
      })
      .catch(() => {
        if (alive) setRecord(null);
      })
      .finally(() => {
        if (alive) setLoaded(true);
      });
    return () => {
      alive = false;
    };
  }, [id, taskId]);

  const back = (
    <Button asChild size="sm" variant="outline">
      <Link href={taskId ? `/function/tasks/detail?id=${encodeURIComponent(taskId)}` : "/function/commands"}>
        <ArrowLeftIcon data-icon="inline-start" />
        {t("back")}
      </Link>
    </Button>
  );

  if (!loaded) {
    return (
      <div className="flex flex-1 items-center justify-center gap-2 p-10 text-muted-foreground text-sm">
        <Spinner />
        {tc("detailTitle")}
      </div>
    );
  }

  if (!record) {
    return (
      <div className="flex flex-1 flex-col gap-3">
        <div className="flex items-start justify-between gap-3">
          <Masthead title={t("title")} titleClassName="text-xl" />
          {back}
        </div>
        <p className="border-border/70 border-t pt-6 text-center text-muted-foreground text-sm">
          {t("notFound", { id: String(searchParams.get("id") ?? "") })}
        </p>
      </div>
    );
  }

  const fields: { k: string; v: React.ReactNode }[] = [
    { k: t("fieldTool"), v: <code className="font-mono text-[11px]">{record.tool}</code> },
    { k: t("fieldWorker"), v: <span className="font-mono text-[11px]">{record.worker || "—"}</span> },
    {
      k: t("fieldTask"),
      v: record.exploration_id ? (
        <Link
          href={`/function/tasks/detail?id=${record.exploration_id}`}
          className="text-primary hover:underline tabular-nums"
        >
          #{record.exploration_id}
        </Link>
      ) : (
        "—"
      ),
    },
    { k: t("fieldTime"), v: <span className="tabular-nums">{record.created_at}</span> },
    {
      k: t("fieldStatus"),
      v: (
        <span className={cn("tabular-nums", record.is_error ? "text-destructive" : "text-emerald-600")}>
          {record.is_error ? "失败" : "成功"}
        </span>
      ),
    },
  ];

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      {/* 书眉：编号 / 工具 / 状态一行，标题是「工具 · 一句话」，下面是字段条 */}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <Masthead
          title={t("title")}
          meta={
            <>
              <code className="font-mono text-[10px]">#{record.id}</code>
              <span className="mx-1.5 opacity-40">·</span>
              <span>{record.tool}</span>
              <span className="mx-1.5 opacity-40">·</span>
              <span className={record.is_error ? "text-destructive" : "text-emerald-600"}>
                {record.is_error ? "失败" : "成功"}
              </span>
            </>
          }
          titleClassName="text-xl"
        />
        {back}
      </div>

      {/* 字段条：五格，靠竖线分开，没有卡片 */}
      <dl className="grid grid-cols-2 divide-border/60 border-border/60 border-y sm:grid-cols-3 lg:grid-cols-5 lg:divide-x">
        {fields.map((f) => (
          <div key={f.k} className="flex flex-col gap-0.5 px-3 py-2">
            <dt className="text-[10px] tracking-[0.08em] text-muted-foreground">{f.k}</dt>
            <dd className="truncate text-[13px]">{f.v}</dd>
          </div>
        ))}
      </dl>

      {/* 输入：原文，等宽，可横向滚动 */}
      <section className="flex min-h-0 flex-col gap-1.5">
        <h2 className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("input")}</h2>
        <pre className="overflow-x-auto rounded-[2px] border border-border/70 bg-muted/25 p-3 font-mono text-[11.5px] leading-relaxed">
          {record.command}
        </pre>
      </section>

      {/* 输出：成功/失败同一块，失败时整块转危险色的左边线 */}
      <section className="flex min-h-0 flex-col gap-1.5">
        <h2 className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("output")}</h2>
        {record.output ? (
          <pre
            className={cn(
              "max-h-[52vh] overflow-auto rounded-[2px] border border-border/70 bg-muted/25 p-3 font-mono text-[11.5px] leading-relaxed",
              record.is_error && "border-l-2 border-l-destructive",
            )}
          >
            {record.output}
          </pre>
        ) : (
          <p className="rounded-[2px] border border-border/60 border-dashed p-6 text-center text-muted-foreground text-xs">
            {t("emptyOutput")}
          </p>
        )}
      </section>
    </div>
  );
}

export default function CommandDetailPage() {
  return (
    <React.Suspense fallback={null}>
      <CommandDetailInner />
    </React.Suspense>
  );
}
