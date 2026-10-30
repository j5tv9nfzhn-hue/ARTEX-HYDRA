"use client";

import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon, ShieldCheckIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { ApprovalDetail } from "@/components/approval-records";
import { KpiRow, Masthead } from "@/components/editorial";
import { Sheet, SheetFoot, SheetHead } from "@/components/editorial-sheet";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { api } from "@/lib/api";
import type { InterceptApprovalRow } from "@/lib/types";

/**
 * 审批票详情：一张放行票的完整上下文。
 *
 * 地址：/system/intercept/approvals/detail?id=<审批 id>
 *
 * 正文直接复用 `components/approval-records.tsx` 的 ApprovalDetail —— 行内展开看到的
 * 内容与这里逐字相同，只是这里可以刷新、可以贴给别人、可以从通知里直链进来。
 * 待裁决的票仍然可以在这里裁决（与列表页同一套 decide）。
 */
function ApprovalDetailInner() {
  const t = useTranslations("approvalDetailPage");
  const ta = useTranslations("approval");
  const searchParams = useSearchParams();
  const id = Number(searchParams.get("id") ?? "");

  const [row, setRow] = React.useState<InterceptApprovalRow | null>(null);
  const [loaded, setLoaded] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [revision, setRevision] = React.useState(0);
  const [error, setError] = React.useState("");

  React.useEffect(() => {
    if (!Number.isFinite(id) || id <= 0) {
      setLoaded(true);
      return;
    }
    let alive = true;
    api
      .interceptHistory()
      .then((rows) => {
        if (alive) setRow(rows.find((r) => r.id === id) ?? null);
      })
      .catch(() => {
        if (alive) setRow(null);
      })
      .finally(() => {
        if (alive) setLoaded(true);
      });
    return () => {
      alive = false;
    };
  }, [id]);

  const decide = React.useCallback(async (rid: number, decision: "allowed" | "denied") => {
    setBusy(true);
    setError("");
    try {
      await api.interceptDecide(rid, decision);
      setRevision((v) => v + 1);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }, []);

  const back = (
    <Button asChild size="sm" variant="outline">
      <Link href="/system/intercept/approvals">
        <ArrowLeftIcon data-icon="inline-start" />
        {t("back")}
      </Link>
    </Button>
  );

  if (!loaded) {
    return (
      <div className="flex flex-1 items-center justify-center gap-2 p-10 text-muted-foreground text-sm">
        <Spinner />
        {t("loading")}
      </div>
    );
  }

  if (!row) {
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

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <Masthead
          title={row.tool_name || t("title")}
          meta={
            <>
              <code className="font-mono text-[10px]">#{row.id}</code>
              <span className="mx-1.5 opacity-40">·</span>
              <span className="font-mono">{row.agent_name || "—"}</span>
              <span className="mx-1.5 opacity-40">·</span>
              <Badge variant="outline">{ta(`status.${row.status}`)}</Badge>
              <span className="mx-1.5 opacity-40">·</span>
              <span className="tabular-nums">{row.created_at}</span>
            </>
          }
          subtitle={
            row.task_id
              ? t("taskLine", { id: row.task_id })
              : row.conv_title || t("noTask")
          }
          titleClassName="text-xl"
        />
        {back}
      </div>

      <KpiRow className="grid grid-cols-2 divide-y divide-border/60 border-border/60 border-y sm:grid-cols-3 sm:divide-x sm:divide-y-0 lg:grid-cols-5">
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("kpi.status")}</span>
          <span className="font-semibold text-[15px] leading-none">
            <Badge variant="outline">{ta(`status.${row.status}`)}</Badge>
          </span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("kpi.source")}</span>
          <span className="font-semibold text-[15px] leading-none">
            {row.decision_source === "model" ? ta("modelDecision") : row.rule_name || ta("ruleMissing")}
          </span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("kpi.agent")}</span>
          <span className="truncate font-mono text-[13px] leading-none">{row.agent_name || "—"}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("kpi.requested")}</span>
          <span className="font-semibold text-[13px] leading-none tabular-nums">{row.created_at}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("kpi.decided")}</span>
          <span className="font-semibold text-[13px] leading-none tabular-nums">{row.decided_at || "—"}</span>
        </div>
      </KpiRow>

      {error ? (
        <p className="border-destructive/40 border-l-2 pl-3 text-[12.5px] text-destructive">{error}</p>
      ) : null}

      <Sheet>
        <SheetHead icon={ShieldCheckIcon} title={t("body")} hint={t("bodyHint")} />
        <ApprovalDetail
          row={row}
          busy={busy}
          decide={decide}
          revision={revision}
          defaultExpanded
          onResolved={() => setRevision((v) => v + 1)}
        />
        <SheetFoot left={t("foot", { id: row.id })} right={row.decision_source === "model" ? "model" : "rule"} />
      </Sheet>
    </div>
  );
}

export default function ApprovalDetailPage() {
  return (
    <React.Suspense fallback={null}>
      <ApprovalDetailInner />
    </React.Suspense>
  );
}
