"use client";

import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon, ArrowUpRightIcon, ShieldAlertIcon } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { KpiRow } from "@/components/editorial";
import { Sheet, SheetHead } from "@/components/editorial-sheet";
import { CopyButton } from "@/components/copy-button";
import { FindingRetestPanel } from "@/components/finding-retest-panel";
import { FindingTrafficPanel } from "@/components/finding-traffic-panel";
import { FindingVerifyPanel } from "@/components/finding-verify-panel";
import { Markdown } from "@/components/markdown";
import { StatusBadge } from "@/components/status-badge";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger } from "@/components/ui/select";
import { SidebarTrigger } from "@/components/ui/sidebar";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import { SEVERITIES } from "@/lib/severity";
import type { Finding, FindingStatus, Severity } from "@/lib/types";

import { FindingLineageView } from "./lineage";

const FINDING_STATUSES: FindingStatus[] = [
  "pending",
  "in_progress",
  "confirmed",
  "resolved",
  "fixed",
  "false_positive",
  "ignored",
  "duplicate",
  "risk_accepted",
];

function fmtTime(ts: string) {
  return new Date(ts).toLocaleString("zh-CN");
}

// FieldRow is one label/value line in the right-hand status panel.
function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-3 py-2.5">
      <span className="shrink-0 pt-0.5 text-xs text-muted-foreground">{label}</span>
      <div className="flex min-w-0 flex-col items-end gap-1 text-right text-sm">{children}</div>
    </div>
  );
}

function FindingDetailInner() {
  const searchParams = useSearchParams();
  const id = searchParams.get("id") ?? "";
  const contextTaskId = searchParams.get("context_task") ?? "";
  const [finding, setFinding] = React.useState<Finding | null>(null);
  const [loaded, setLoaded] = React.useState(false);
  const [tab, setTab] = React.useState("overview");
  const t = useTranslations("findings");
  const tStatus = useTranslations("status");

  const load = React.useCallback(() => {
    if (!id) {
      setLoaded(true);
      return;
    }
    api
      .getFinding(id, contextTaskId || undefined)
      .then((f) => setFinding(f))
      .catch(() => setFinding(null))
      .finally(() => setLoaded(true));
  }, [contextTaskId, id]);
  React.useEffect(() => {
    load();
  }, [load]);

  const changeSeverity = React.useCallback(
    async (next: Severity) => {
      if (!finding || finding.inherited || next === finding.severity) return;
      const prev = finding.severity;
      setFinding({ ...finding, severity: next });
      try {
        const updated = await api.setFindingSeverity(id, next);
        setFinding(updated);
        toast.success(t("detail.severityChanged", { label: tStatus(`severity.${next}`) }));
      } catch (e) {
        setFinding((cur) => (cur ? { ...cur, severity: prev } : cur));
        toast.error(t("toast.updateFailed", { error: (e as Error).message }));
      }
    },
    [finding, id, t, tStatus],
  );

  const changeStatus = React.useCallback(
    async (next: FindingStatus) => {
      if (!finding || finding.inherited || next === finding.status) return;
      const prev = finding.status;
      setFinding({ ...finding, status: next });
      try {
        const updated = await api.setFindingStatus(id, next);
        setFinding(updated);
        toast.success(t("detail.statusChanged", { label: tStatus(`finding.${next}`) }));
      } catch (e) {
        setFinding((cur) => (cur ? { ...cur, status: prev } : cur));
        toast.error(t("toast.updateFailed", { error: (e as Error).message }));
      }
    },
    [finding, id, t, tStatus],
  );

  if (!finding) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 p-10 text-center">
        <p className="text-muted-foreground">{loaded ? t("detail.notFound", { id }) : t("common.loading")}</p>
        {loaded && (
          <Button asChild variant="outline">
            <Link href="/function/findings">
              <ArrowLeftIcon /> {t("detail.back")}
            </Link>
          </Button>
        )}
      </div>
    );
  }

  const title = finding.name || finding.vulnclass || t("table.unclassified");

  return (
    <Tabs value={tab} onValueChange={setTab} className="flex flex-1 flex-col gap-0">
      {/* 工具行：只剩「从哪儿来、能做什么」，其余交给书眉 */}
      <div className="flex flex-wrap items-center gap-2 px-4 pt-2.5 lg:px-6">
        <SidebarTrigger className="-ml-1" />
        <Button asChild variant="ghost" size="icon" className="size-7">
          <Link href="/function/findings">
            <ArrowLeftIcon />
          </Link>
        </Button>
        <span className="text-muted-foreground text-xs">{t("detail.back")}</span>
        {finding.inherited && finding.source_task_id && (
          <Badge variant="outline" className="ml-auto">
            {t("detail.sourceTask", { id: finding.source_task_id })}
          </Badge>
        )}
      </div>

      {/* 书眉：编号 / 严重度 / 状态 / 复核 / 时间 / 标题 / 所属任务 */}
      <div className="flex flex-col gap-1 px-4 pt-2 pb-2 lg:px-6">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground tabular-nums">
          <code className="rounded-[2px] border border-border/60 px-1 py-px font-mono text-xs">#{finding.id}</code>
          <span>{finding.vulnclass || t("table.unclassified")}</span>
          <span className="h-2 w-px bg-border" />
          <StatusBadge domain="severity" value={finding.severity} dot />
          <span className="h-2 w-px bg-border" />
          <StatusBadge domain="finding" value={finding.status} dot />
          <span className="h-2 w-px bg-border" />
          <span>{fmtTime(finding.ts)}</span>
        </div>
        <h1 className="font-display text-[26px] font-semibold leading-tight tracking-[-0.02em]" title={title}>
          {title}
        </h1>
        {finding.task_description && (
          <p className="flex items-start gap-1.5 text-[13px] leading-relaxed text-muted-foreground">
            <ShieldAlertIcon className="mt-[3px] size-3.5 shrink-0 text-primary" />
            <Link
              href={`/function/tasks/detail?id=${finding.task_id}`}
              className="line-clamp-2 hover:text-foreground hover:underline"
            >
              {finding.task_description}
            </Link>
          </p>
        )}
      </div>

      {/* 数字行：五格，无框，只靠竖线分开；每格点一下跳到对应位置 */}
      <KpiRow className="mx-4 mb-3 grid grid-cols-2 divide-y divide-border/60 border-border/60 border-y sm:grid-cols-3 sm:divide-x sm:divide-y-0 lg:mx-6 lg:grid-cols-5">
        <button
          type="button"
          onClick={() => setTab("overview")}
          className="flex flex-col items-start gap-0.5 px-3 py-2 text-left transition-colors hover:bg-accent/40"
        >
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("detail.fieldSeverity")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">
            {tStatus(`severity.${finding.severity}`)}
          </span>
        </button>
        <button
          type="button"
          onClick={() => setTab("overview")}
          className="flex flex-col items-start gap-0.5 px-3 py-2 text-left transition-colors hover:bg-accent/40"
        >
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("detail.fieldStatus")}</span>
          <span className="font-semibold text-[22px] leading-none">
            {tStatus(`finding.${finding.status}`)}
          </span>
        </button>
        <button
          type="button"
          onClick={() => setTab("overview")}
          className="flex flex-col items-start gap-0.5 px-3 py-2 text-left transition-colors hover:bg-accent/40"
        >
          <span className="text-xs tracking-[0.08em] text-muted-foreground">复核态</span>
          <span className="font-semibold text-[22px] leading-none">
            <StatusBadge domain="validation" value={finding.validation ?? "unverified"} dot />
          </span>
        </button>
        <button
          type="button"
          onClick={() => setTab("overview")}
          className="flex flex-col items-start gap-0.5 px-3 py-2 text-left transition-colors hover:bg-accent/40"
        >
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("detail.fieldAssets")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{finding.assets?.length ?? 0}</span>
        </button>
        <button
          type="button"
          onClick={() => setTab("overview")}
          className="flex flex-col items-start gap-0.5 px-3 py-2 text-left transition-colors hover:bg-accent/40"
        >
          <span className="text-xs tracking-[0.08em] text-muted-foreground">证据</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">
            {finding.evidence ? `${Math.max(1, Math.round(finding.evidence.length / 1024))}K` : "—"}
          </span>
        </button>
      </KpiRow>

      {/* 页签：下划线索引，吸顶 */}
      <div className="no-scrollbar sticky top-0 z-10 overflow-x-auto bg-background/92 px-4 pt-1 backdrop-blur-md lg:px-6">
        <TabsList variant="line" className="h-8 min-w-max gap-4 border-0 bg-transparent p-0">
          <TabsTrigger value="overview" className="h-8 px-0 text-xs">
            {t("detail.tab.overview")}
          </TabsTrigger>
          <TabsTrigger value="lineage" className="h-8 px-0 text-xs">
            {t("detail.tab.lineage")}
          </TabsTrigger>
        </TabsList>
      </div>

      {/* Tab content */}
      <div className="flex-1 p-4 lg:p-6">
        {/* 概览：左（摘要 + 证据）/ 右（状态区） */}
        <TabsContent value="overview" className="mt-0">
          <div className="grid gap-4 lg:grid-cols-3">
            {/* 左栏 */}
            <div className="flex flex-col gap-4 lg:col-span-2">
              <Sheet>
                <SheetHead title={t("detail.summary")} icon={ShieldAlertIcon} />
                <div data-editorial="body" className="px-4 pt-2 pb-4">
                  <p className="text-sm leading-relaxed whitespace-pre-wrap [overflow-wrap:anywhere]">
                    {finding.summary || t("detail.summaryEmpty")}
                  </p>
                </div>
              </Sheet>
              <FindingVerifyPanel
                key={id}
                findingId={id}
                findingName={finding.name || finding.vulnclass || finding.summary}
                readOnly={finding.inherited}
                onCompleted={load}
              />
              <FindingRetestPanel key={id} findingId={id} readOnly={finding.inherited} onCompleted={load} />
              <Sheet>
                <SheetHead
                  title={t("detail.evidence")}
                  count={finding.evidence ? `${finding.evidence.length.toLocaleString("zh-CN")} 字符` : undefined}
                />
                <div data-editorial="body" className="px-4 pt-2 pb-4">
                  {finding.evidence ? (
                    <pre
                      data-editorial="evidence"
                      className="max-h-[46vh] overflow-auto rounded-md bg-muted px-3 py-2 font-mono text-xs whitespace-pre-wrap [overflow-wrap:anywhere]"
                    >
                      {finding.evidence}
                    </pre>
                  ) : (
                    <p className="text-sm text-muted-foreground">{t("detail.evidenceEmpty")}</p>
                  )}
                </div>
              </Sheet>
              <FindingTrafficPanel
                key={id}
                findingId={id}
                contextTask={contextTaskId || undefined}
                readOnly={finding.inherited}
                onChanged={load}
              />
              {/* 证据下方：详细报告(Markdown 渲染) */}
              <Sheet>
                <SheetHead
                  title={t("table.detailReport")}
                  hint={finding.report ? `${finding.report.length.toLocaleString("zh-CN")} 字` : undefined}
                  actions={
                    finding.report ? (
                      <CopyButton text={finding.report} successMessage={t("table.reportCopied")} />
                    ) : undefined
                  }
                />
                <div data-editorial="body" className="px-4 pt-2 pb-4">
                  {finding.report_stale ? (
                    <Alert>
                      <AlertDescription>{t("detail.reportStale")}</AlertDescription>
                    </Alert>
                  ) : null}
                  {finding.report ? (
                    <Markdown text={finding.report} />
                  ) : (
                    <p className="text-sm text-muted-foreground">{t("table.reportEmpty")}</p>
                  )}
                </div>
              </Sheet>
            </div>

            {/* 右栏：状态区 */}
            <Sheet className="h-fit lg:sticky lg:top-24">
              <SheetHead title={t("detail.statusTitle")} />
              <div data-editorial="body" className="divide-y px-4">
                {/* 漏洞 ID */}
                <FieldRow label={t("detail.fieldId")}>
                  <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-muted-foreground">
                    #{finding.id}
                  </code>
                </FieldRow>

                {/* 严重等级 */}
                <FieldRow label={t("detail.fieldSeverity")}>
                  {finding.inherited ? (
                    <StatusBadge domain="severity" value={finding.severity} dot />
                  ) : (
                    <Select value={finding.severity} onValueChange={(v) => changeSeverity(v as Severity)}>
                      <SelectTrigger size="sm" className="h-7 w-auto border-none px-1 shadow-none focus-visible:ring-0">
                        <StatusBadge domain="severity" value={finding.severity} dot />
                      </SelectTrigger>
                      <SelectContent position="popper" align="end">
                        <SelectGroup>
                          {SEVERITIES.map((sv) => (
                            <SelectItem key={sv} value={sv}>
                              {tStatus(`severity.${sv}`)}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  )}
                </FieldRow>

                {/* 处理状态 */}
                <FieldRow label={t("detail.fieldStatus")}>
                  {finding.inherited ? (
                    <StatusBadge domain="finding" value={finding.status} dot />
                  ) : (
                    <Select value={finding.status} onValueChange={(v) => changeStatus(v as FindingStatus)}>
                      <SelectTrigger size="sm" className="h-7 w-auto border-none px-1 shadow-none focus-visible:ring-0">
                        <StatusBadge domain="finding" value={finding.status} dot />
                      </SelectTrigger>
                      <SelectContent position="popper" align="end">
                        <SelectGroup>
                          {FINDING_STATUSES.map((st) => (
                            <SelectItem key={st} value={st}>
                              {tStatus(`finding.${st}`)}
                            </SelectItem>
                          ))}
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                  )}
                </FieldRow>

                {/* 漏洞类型 */}
                <FieldRow label={t("detail.fieldType")}>
                  {finding.vulnclass ? (
                    <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">{finding.vulnclass}</code>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </FieldRow>

                {/* 涉及资产 */}
                <FieldRow label={t("detail.fieldAssets")}>
                  {finding.assets && finding.assets.length > 0 ? (
                    <div className="flex flex-wrap justify-end gap-1">
                      {finding.assets.map((a) => (
                        <code
                          key={a.id}
                          className="max-w-[16rem] truncate rounded bg-muted px-1.5 py-0.5 font-mono text-xs"
                          title={`${a.type} / ${a.label}`}
                        >
                          {a.label}
                        </code>
                      ))}
                    </div>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </FieldRow>

                {/* 所属任务 */}
                <FieldRow label={t("detail.fieldTask")}>
                  {finding.task_id ? (
                    <Link
                      href={`/function/tasks/detail?id=${finding.task_id}`}
                      className="inline-flex max-w-[16rem] items-center gap-1 text-primary hover:underline"
                      title={finding.task_description}
                    >
                      <span className="truncate">{finding.task_description || `#${finding.task_id}`}</span>
                      <ArrowUpRightIcon className="size-3 shrink-0" />
                    </Link>
                  ) : (
                    <span className="text-muted-foreground">{t("detail.taskDeleted")}</span>
                  )}
                </FieldRow>

                {/* 发现时间 */}
                <FieldRow label={t("detail.fieldTime")}>
                  <span className="tabular-nums">{fmtTime(finding.ts)}</span>
                </FieldRow>
              </div>
            </Sheet>
          </div>
        </TabsContent>

        {/* 链路图：从任务初始节点回溯到本漏洞节点的攻击链路 */}
        <TabsContent value="lineage" className="mt-0">
          <FindingLineageView findingId={finding.id} />
        </TabsContent>
      </div>
    </Tabs>
  );
}

// useSearchParams must sit under a Suspense boundary for static export.
export default function FindingDetailPage() {
  return (
    <React.Suspense fallback={null}>
      <FindingDetailInner />
    </React.Suspense>
  );
}
