"use client";

import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon, CoinsIcon, CpuIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { KpiRow, Masthead } from "@/components/editorial";
import { RecordRow, Sheet, SheetFoot, SheetHead } from "@/components/editorial-sheet";
import { CopyButton } from "@/components/copy-button";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { api } from "@/lib/api";
import type { LLMRecordDetail } from "@/lib/types";

/**
 * 录制详情：一次模型调用的输入片段、输出全文与 token 账。
 *
 * 地址：/function/llm-records/detail?id=<录制 id>
 *
 * 与列表页的内嵌面板同一份数据源（`/llm/records/:id`），区别是这里可以刷新、
 * 可以贴给别人看；归一化视图与 provider 原文分成两块，原文缺失的老记录明确标注。
 */
function fmtLatency(ms: number) {
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

function fmtTokens(n: number) {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(n);
}

function fmtCost(n: number) {
  if (n <= 0) return "—";
  if (n < 0.01) return `$${n.toFixed(4)}`;
  if (n < 1) return `$${n.toFixed(3)}`;
  return `$${n.toFixed(2)}`;
}

function RecordDetailInner() {
  const t = useTranslations("recordDetail");
  const searchParams = useSearchParams();
  const id = Number(searchParams.get("id") ?? "");

  const [rec, setRec] = React.useState<LLMRecordDetail | null>(null);
  const [loaded, setLoaded] = React.useState(false);
  const [rawView, setRawView] = React.useState(false);

  React.useEffect(() => {
    if (!Number.isFinite(id) || id <= 0) {
      setLoaded(true);
      return;
    }
    let alive = true;
    api
      .llmRecordDetail(id)
      .then((d) => {
        if (alive) setRec(d);
      })
      .catch(() => {
        if (alive) setRec(null);
      })
      .finally(() => {
        if (alive) setLoaded(true);
      });
    return () => {
      alive = false;
    };
  }, [id]);

  const back = (
    <Button asChild size="sm" variant="outline">
      <Link href="/function/llm-records">
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

  if (!rec) {
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

  const hasRaw = Boolean(rec.raw_request || rec.raw_response);
  const reqText = (rawView ? rec.raw_request : rec.request_body) || "";
  const respText = (rawView ? rec.raw_response : rec.response_body) || "";
  const billable = rec.input_tokens + rec.output_tokens;

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <Masthead
          title={rec.model || t("title")}
          meta={
            <>
              <code className="font-mono text-xs">#{rec.id}</code>
              <span className="font-mono">{rec.profile_name || "—"}</span>
              <span>{rec.status === "ok" ? t("statusOk") : t("statusError")}</span>
              <span className="tabular-nums">{rec.ts}</span>
            </>
          }
          subtitle={[rec.worker || null, rec.task_id ? `#${rec.task_id}` : null, rec.session_id || null]
            .filter(Boolean)
            .join(" / ")}
          titleClassName="text-xl"
        />
        {back}
      </div>

      <KpiRow className="grid grid-cols-2 divide-y divide-border/60 border-border/60 border-y sm:grid-cols-3 sm:divide-x sm:divide-y-0 lg:grid-cols-5">
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.input")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{fmtTokens(rec.input_tokens)}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.output")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{fmtTokens(rec.output_tokens)}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.cache")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">
            {fmtTokens(rec.cache_read)}
          </span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.latency")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{fmtLatency(rec.latency_ms)}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.cost")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{fmtCost(rec.cost ?? 0)}</span>
        </div>
      </KpiRow>

      <div className="grid min-w-0 gap-4 xl:grid-cols-2">
        <Sheet className="min-w-0">
          <SheetHead
            title={`${t("input")}${rawView ? ` / ${t("raw")}` : ""}`}
            count={`${reqText.length.toLocaleString("zh-CN")} 字符`}
            actions={<CopyButton text={reqText} />}
          />
          <div data-editorial="body" className="min-h-0 px-3 py-2">
            <pre className="max-h-[56vh] overflow-auto whitespace-pre-wrap break-all font-mono text-xs leading-relaxed [overflow-wrap:anywhere]">
              {reqText || t("emptyContent")}
            </pre>
          </div>
        </Sheet>

        <Sheet className="min-w-0">
          <SheetHead
            title={`${t("output")}${rawView && hasRaw ? ` / ${t("raw")}(SSE)` : ""}`}
            count={`${respText.length.toLocaleString("zh-CN")} 字符`}
            actions={<CopyButton text={respText} />}
          />
          <div data-editorial="body" className="min-h-0 px-3 py-2">
            <pre
              className={
                rec.status === "ok"
                  ? "max-h-[56vh] overflow-auto whitespace-pre-wrap break-all font-mono text-xs leading-relaxed [overflow-wrap:anywhere]"
                  : "max-h-[56vh] overflow-auto whitespace-pre-wrap break-all border-l-2 border-l-destructive pl-3 font-mono text-xs leading-relaxed text-destructive [overflow-wrap:anywhere]"
              }
            >
              {respText || t("emptyContent")}
            </pre>
          </div>
          <SheetFoot left={t("footChars", { n: respText.length })} right={hasRaw ? t("rawAvailable") : t("rawMissing")} />
        </Sheet>
      </div>

      <div className="grid min-w-0 gap-4 lg:grid-cols-2">
        <Sheet>
          <SheetHead icon={CpuIcon} title={t("account")} hint={t("accountHint")} />
          <div data-editorial="body" className="px-4 pt-1 pb-3">
            <RecordRow lead={t("row.model")} cols="96px minmax(0,1fr) auto" values={[rec.model || "—"]}>
              <span className="text-[13px] text-muted-foreground">{rec.profile_name || "—"}</span>
            </RecordRow>
            <RecordRow
              lead={t("row.tokens")}
              cols="96px minmax(0,1fr) auto"
              values={[`${fmtTokens(billable)} ${t("kpi.total")}`]}
            >
              <span className="text-[13px] tabular-nums">
                {fmtTokens(rec.input_tokens)} / {fmtTokens(rec.output_tokens)}
              </span>
            </RecordRow>
            <RecordRow
              lead={t("row.cache")}
              cols="96px minmax(0,1fr) auto"
              values={[fmtTokens(rec.cache_write)]}
            >
              <span className="text-[13px] tabular-nums">{t("cacheRead", { n: fmtTokens(rec.cache_read) })}</span>
            </RecordRow>
            {rec.error ? (
              <RecordRow lead={t("row.error")} cols="96px minmax(0,1fr) auto">
                <span className="text-[12.5px] text-destructive [overflow-wrap:anywhere]">{rec.error}</span>
              </RecordRow>
            ) : null}
          </div>
          <SheetFoot left={t("footAccount")} />
        </Sheet>

        <Sheet>
          <SheetHead icon={CoinsIcon} title={t("cost")} hint={t("costHint")} />
          <div data-editorial="body" className="px-4 pt-1 pb-3">
            <RecordRow lead={t("row.cost")} cols="96px minmax(0,1fr) auto" values={[fmtCost(rec.cost ?? 0)]}>
              <span className="text-[13px] text-muted-foreground">{t("costNote")}</span>
            </RecordRow>
            {rec.task_id ? (
              <RecordRow lead={t("row.task")} cols="96px minmax(0,1fr) auto">
                <Link
                  href={`/function/tasks/detail?id=${encodeURIComponent(rec.task_id)}`}
                  className="text-[13px] text-primary hover:underline tabular-nums"
                >
                  {t("openTask", { id: rec.task_id })}
                </Link>
              </RecordRow>
            ) : null}
            <RecordRow lead={t("row.session")} cols="96px minmax(0,1fr) auto">
              <code className="font-mono text-xs text-muted-foreground">{rec.session_id || "—"}</code>
            </RecordRow>
          </div>
          <SheetFoot left={t("footCost")} right={t("replayOff")} />
        </Sheet>
      </div>
    </div>
  );
}

export default function RecordDetailPage() {
  return (
    <React.Suspense fallback={null}>
      <RecordDetailInner />
    </React.Suspense>
  );
}
