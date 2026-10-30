"use client";

import * as React from "react";

import Link from "next/link";

import { ArrowDownIcon, ArrowUpIcon, ArrowUpRightIcon, ChevronRightIcon } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { GroupHead, RecordRow, RecordSub, Sheet, SheetFoot, SheetHead } from "@/components/editorial-sheet";
import { EmptyState } from "@/components/empty-state";
import { StatusBadge } from "@/components/status-badge";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger } from "@/components/ui/select";
import { usePolling } from "@/hooks/use-polling";
import { api } from "@/lib/api";
import { useStoredSortPreference } from "@/lib/sort-preference";
import type { Finding, FindingStatus, Severity } from "@/lib/types";
import { cn } from "@/lib/utils";

type FindingSortField = "time";

const FINDING_SORT_FIELDS: readonly FindingSortField[] = ["time"];
const FINDING_SORT_PREFERENCE_KEY = "artex_task_findings_sort";

function findingLabel(finding: Finding, unclassified: string): string {
  if (finding.name?.trim()) return finding.name;
  if (finding.vulnclass?.trim()) return finding.vulnclass;
  return unclassified;
}

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

// 分卷顺序与发现页一致：严重 → 高危 → 中危 → 低危 → 其余。
// 「其余」收纳 info / 未定级，避免有的条目无处可去。
const SEVERITY_GROUPS = ["critical", "high", "medium", "low"] as const;
type SeverityGroup = (typeof SEVERITY_GROUPS)[number];

function groupOf(severity: Severity | undefined): SeverityGroup | "other" {
  return (SEVERITY_GROUPS as readonly string[]).includes(severity ?? "")
    ? (severity as SeverityGroup)
    : "other";
}

/**
 * 一条发现：标签是严重度（整页唯一的色点），正文是名称 + 摘要，
 * 右侧三列是证据数、复核态、时间。展开后是证据原文（纸本里是一段引文）。
 */
function Row({
  f,
  contextTaskId,
  onStatus,
}: {
  f: Finding;
  contextTaskId: string;
  onStatus: (f: Finding, next: FindingStatus) => void;
}) {
  const t = useTranslations("taskDetail.findings");
  const tStatus = useTranslations("status");
  const [open, setOpen] = React.useState(false);

  return (
    <div data-editorial="record-wrap">
      <RecordRow
        expanded={open}
        onClick={() => setOpen((o) => !o)}
        cols="56px minmax(0,1fr) 62px 74px 92px"
        lead={<StatusBadge domain="severity" value={f.severity} dot />}
        values={[
          <span key="ev" className="text-muted-foreground">
            {t("trafficEvidence", { count: f.traffic_count ?? 0 })}
          </span>,
          <span key="val" className="hidden sm:inline-flex">
            {f.finding_id ? <StatusBadge domain="validation" value={f.validation ?? "unverified"} /> : null}
          </span>,
          <span key="ts" className="hidden text-muted-foreground md:inline">
            {new Date(f.ts).toLocaleString("zh-CN")}
          </span>,
        ]}
      >
        <div className="flex min-w-0 items-center gap-2">
          <ChevronRightIcon
            className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")}
          />
          <span className="truncate font-medium text-[13px]">{findingLabel(f, t("unclassified"))}</span>
          {f.inherited && f.source_task_id && (
            <Badge variant="outline" className="shrink-0">
              {t("sourceReadonly", { id: f.source_task_id })}
            </Badge>
          )}
        </div>
        <RecordSub
          items={[
            <span key="sum" className="line-clamp-1 max-w-full">
              {f.summary}
            </span>,
            ...(f.assets ?? []).slice(0, 2).map((a) => (
              <code key={a.id} className="font-mono text-[10.5px]" title={`${a.type} / ${a.label}`}>
                {a.label}
              </code>
            )),
            f.assets && f.assets.length > 2 ? <span key="more">+{f.assets.length - 2}</span> : null,
          ]}
        />
      </RecordRow>

      {/* 行内第二排：状态与详情入口。点状态不展开，点其余区域展开证据。 */}
      <div className="-mt-1 flex items-center justify-end gap-2 px-4 pb-2">
        {f.finding_id && !f.inherited ? (
          <Select value={f.status} onValueChange={(v) => onStatus(f, v as FindingStatus)}>
            <SelectTrigger size="sm" className="h-6 w-auto shrink-0 border-none px-1 shadow-none focus-visible:ring-0">
              <StatusBadge domain="finding" value={f.status} dot />
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
        ) : (
          <StatusBadge domain="finding" value={f.status} dot />
        )}
        {f.finding_id && (
          <Link
            href={
              f.inherited
                ? `/function/findings/detail?id=${f.finding_id}&context_task=${contextTaskId}`
                : `/function/findings/detail?id=${f.finding_id}`
            }
            className="inline-flex shrink-0 items-center gap-0.5 text-muted-foreground text-xs hover:text-primary"
            title={t("viewDetail")}
          >
            {t("detail")}
            <ArrowUpRightIcon className="size-3" />
          </Link>
        )}
      </div>

      {open && (
        <div className="px-4 pb-3 pl-14">
          <div className="mb-1 text-xs text-muted-foreground tracking-[0.06em]">{t("evidencePoc")}</div>
          <pre
            data-editorial="evidence"
            className="overflow-auto whitespace-pre-wrap rounded-md border bg-muted/25 p-3 font-mono text-xs"
          >
            {f.evidence}
          </pre>
        </div>
      )}
    </div>
  );
}

export function FindingsTab({ taskId }: { taskId: string }) {
  const t = useTranslations("taskDetail.findings");
  const tStatus = useTranslations("status");
  const tSev = useTranslations("findings.severity");
  const [findings, setFindings] = React.useState<Finding[]>([]);
  const [sortPreference, setSortPreference] = useStoredSortPreference(
    FINDING_SORT_PREFERENCE_KEY,
    FINDING_SORT_FIELDS,
    "time",
    "desc",
  );

  usePolling(
    async () => {
      const fs = await api.findings(taskId);
      setFindings(fs);
    },
    { intervalMs: 3000, enabled: Boolean(taskId) },
  );

  const onStatus = React.useCallback(
    async (f: Finding, next: FindingStatus) => {
      if (f.inherited || !f.finding_id || next === f.status) return;
      const prev = f.status;
      setFindings((cur) => cur.map((x) => (x.id === f.id ? { ...x, status: next } : x)));
      try {
        await api.setFindingStatus(f.finding_id, next);
        toast.success(t("markedAs", { label: tStatus(`finding.${next}`) }));
      } catch (e) {
        setFindings((cur) => cur.map((x) => (x.id === f.id ? { ...x, status: prev } : x)));
        toast.error(t("updateFailed", { message: (e as Error).message }));
      }
    },
    [t, tStatus],
  );

  const items = React.useMemo(
    () =>
      findings
        .filter((f) => f.task_id === taskId || f.inherited)
        .sort((a, b) => {
          const compared = Date.parse(a.ts) - Date.parse(b.ts);
          if (compared !== 0) return sortPreference.direction === "asc" ? compared : -compared;
          return b.id.localeCompare(a.id, undefined, { numeric: true });
        }),
    [findings, taskId, sortPreference.direction],
  );

  // 分卷：同一批发现按严重度切开，每卷自带计数，空卷不出现。
  const groups = React.useMemo(() => {
    const map = new Map<SeverityGroup | "other", Finding[]>();
    for (const f of items) {
      const key = groupOf(f.severity);
      const bucket = map.get(key);
      if (bucket) bucket.push(f);
      else map.set(key, [f]);
    }
    return [...map.entries()];
  }, [items]);

  const toneOf = (g: SeverityGroup | "other") =>
    g === "critical" || g === "high" ? "red" : g === "medium" ? "amber" : g === "low" ? "green" : "muted";

  return (
    <Sheet>
      <SheetHead
        title={t("columnFinding")}
        count={items.length}
        actions={
          <button
            type="button"
            className="inline-flex items-center gap-1 rounded-sm text-muted-foreground text-xs outline-none hover:text-foreground focus-visible:underline"
            aria-label={t("sortAria", {
              order: sortPreference.direction === "asc" ? t("orderAsc") : t("orderDesc"),
            })}
            onClick={() =>
              setSortPreference((current) => ({
                field: "time",
                direction: current.direction === "asc" ? "desc" : "asc",
              }))
            }
          >
            <span>{t("sortTime")}</span>
            {sortPreference.direction === "asc" ? (
              <ArrowUpIcon className="size-3.5" />
            ) : (
              <ArrowDownIcon className="size-3.5" />
            )}
          </button>
        }
      />

      <div className="px-4">
        {groups.map(([g, list]) => (
          <React.Fragment key={g}>
            <GroupHead
              tone={toneOf(g) as "red" | "amber" | "green" | "muted"}
              title={g === "other" ? t("unclassified") : tSev(g)}
              count={list.length}
            />
            {list.map((f) => (
              <Row key={f.id} f={f} contextTaskId={taskId} onStatus={onStatus} />
            ))}
          </React.Fragment>
        ))}
        {items.length === 0 && <EmptyState illustration="radar" title={t("empty")} />}
      </div>

      {items.length > 0 && (
        <SheetFoot left={t("foot", { total: items.length, groups: groups.length })} right={t("footHint")} />
      )}
    </Sheet>
  );
}
