"use client";

import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon, ArrowUpRightIcon, BoxesIcon, NetworkIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { KpiRow, Masthead } from "@/components/editorial";
import { GroupHead, RecordRow, Sheet, SheetFoot, SheetHead } from "@/components/editorial-sheet";
import { StatusBadge } from "@/components/status-badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { api } from "@/lib/api";
import type { Asset, Finding, TrafficExchange } from "@/lib/types";

/**
 * 资产详情：一个资产的画像、关联发现、出现过的任务与最近流量。
 *
 * 地址：/function/assets/detail?id=<资产 id>
 *
 * 后端没有单条资产的 REST 端点，这里沿用工具执行详情的做法：拉一页资产再按 id 定位。
 * 资产表按 type 分六个页签，单页 500 条足够覆盖一个实例的常见规模；找不到时给
 * 「可能已被删除或在别的企业范围里」的落款，而不是假装 404。
 */
function AddressOf(a: Asset): string {
  if (a.url) return a.url;
  if (a.domain) return a.domain;
  if (a.ip) return a.port ? `${a.ip}:${a.port}` : a.ip;
  if (a.c_segment) return a.c_segment;
  if (a.app_name) return a.app_name;
  if (a.service_name) return a.service_name;
  if (typeof a.record_value === "string") return a.record_value;
  if (Array.isArray(a.record_value) && a.record_value.length > 0) return a.record_value.join(" · ");
  return "—";
}

function LabelOf(a: Asset): string {
  return a.domain || a.ip || a.app_name || a.service_name || a.c_segment || `#${a.id}`;
}

function HostOf(a: Asset): string {
  if (a.domain) return a.domain;
  if (a.ip) return a.ip;
  if (a.url) {
    try {
      return new URL(a.url).host;
    } catch {
      return "";
    }
  }
  return "";
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline gap-3 border-border/60 border-b py-1.5 last:border-b-0">
      <span className="w-28 shrink-0 text-xs text-muted-foreground">{label}</span>
      <div className="min-w-0 flex-1 text-[13px] [overflow-wrap:anywhere]">{children}</div>
    </div>
  );
}

function AssetDetailInner() {
  const t = useTranslations("assetDetail");
  const tk = useTranslations("assetDetail.kind");
  const searchParams = useSearchParams();
  const id = Number(searchParams.get("id") ?? "");

  const [asset, setAsset] = React.useState<Asset | null>(null);
  const [findings, setFindings] = React.useState<Finding[]>([]);
  const [traffic, setTraffic] = React.useState<TrafficExchange[]>([]);
  const [trafficNote, setTrafficNote] = React.useState("");
  const [loaded, setLoaded] = React.useState(false);

  React.useEffect(() => {
    if (!Number.isFinite(id) || id <= 0) {
      setLoaded(true);
      return;
    }
    let alive = true;
    api
      .assets("", 500, 0)
      .then((r) => {
        if (!alive) return;
        setAsset(r.assets.find((a) => a.id === id) ?? null);
      })
      .catch(() => {
        if (alive) setAsset(null);
      })
      .finally(() => {
        if (alive) setLoaded(true);
      });
    api
      .findings()
      .then((rows) => {
        if (!alive) return;
        setFindings(rows.filter((f) => (f.assets ?? []).some((a) => a.id === String(id))));
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [id]);

  // 流量按「这台主机最近 20 条」取。没有可用主机名（云盘目录、C 段之类）就不查，
  // 而不是拿空字符串去换一整个库里最新的一页。
  React.useEffect(() => {
    if (!asset) return;
    const host = HostOf(asset);
    if (!host) {
      setTrafficNote(t("trafficNoHost"));
      return;
    }
    let alive = true;
    api
      .traffic(0, 20, host)
      .then((r) => {
        if (alive) setTraffic(r.exchanges ?? []);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [asset, t]);

  if (!loaded) {
    return (
      <div className="flex flex-1 items-center justify-center gap-2 p-10 text-muted-foreground text-sm">
        <Spinner />
        {t("loading")}
      </div>
    );
  }

  const back = (
    <Button asChild size="sm" variant="outline">
      <Link href="/function/assets">
        <ArrowLeftIcon data-icon="inline-start" />
        {t("back")}
      </Link>
    </Button>
  );

  if (!asset) {
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

  const ports = asset.open_ports ?? [];
  const tech = asset.technologies ?? [];
  const taskIds = asset.task_ids ?? [];
  const label = LabelOf(asset);

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <Masthead
          title={label}
          meta={
            <>
              <code className="font-mono text-xs">#{asset.id}</code>
              <span className="mx-1.5 opacity-40">·</span>
              <span>{tk(asset.type)}</span>
              <span className="mx-1.5 opacity-40">·</span>
              <span className="tabular-nums">{asset.last_seen}</span>
            </>
          }
          subtitle={asset.task_source_summary || undefined}
          titleClassName="text-xl"
        />
        {back}
      </div>

      <KpiRow className="grid grid-cols-2 divide-y divide-border/60 border-border/60 border-y sm:grid-cols-3 sm:divide-x sm:divide-y-0 lg:grid-cols-5">
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.ports")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{ports.length}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.tech")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{tech.length}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.tasks")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{taskIds.length}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.findings")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{findings.length}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.status")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{asset.status_code ?? "—"}</span>
        </div>
      </KpiRow>

      <div className="grid min-w-0 gap-4 lg:grid-cols-3">
        <div className="flex min-w-0 flex-col gap-4 lg:col-span-2">
          <Sheet>
            <SheetHead icon={BoxesIcon} title={t("profile")} count={`#${asset.id}`} hint={t("profileHint")} />
            <div data-editorial="body" className="px-4 pt-1 pb-3">
              <Field label={t("field.type")}>{tk(asset.type)}</Field>
              <Field label={t("field.addr")}>
                <code className="font-mono text-[12px]">{AddressOf(asset)}</code>
              </Field>
              {asset.root_domain ? <Field label={t("field.root")}>{asset.root_domain}</Field> : null}
              {asset.bound_domains && asset.bound_domains.length > 0 ? (
                <Field label={t("field.domains")}>
                  <span className="font-mono text-[12px]">{asset.bound_domains.join(" · ")}</span>
                </Field>
              ) : null}
              {asset.port ? <Field label={t("field.port")}>{asset.port}</Field> : null}
              {asset.icp ? <Field label={t("field.icp")}>{asset.icp}</Field> : null}
              {asset.page_title ? <Field label={t("field.title")}>{asset.page_title}</Field> : null}
              {asset.content_length ? (
                <Field label={t("field.len")}>
                  <span className="tabular-nums">{asset.content_length} B</span>
                </Field>
              ) : null}
              {asset.favicon_mmh3 ? (
                <Field label={t("field.favicon")}>
                  <code className="font-mono text-xs text-muted-foreground">{asset.favicon_mmh3}</code>
                </Field>
              ) : null}
              {asset.company_id ? <Field label={t("field.company")}>#{asset.company_id}</Field> : null}
              <Field label={t("field.seen")}>
                <span className="tabular-nums">{asset.last_seen}</span>
              </Field>
              <Field label={t("field.source")}>
                {asset.task_source ? `${asset.task_source}` : "—"}
                {asset.task_source_summary ? (
                  <span className="ml-2 text-muted-foreground">{asset.task_source_summary}</span>
                ) : null}
              </Field>
            </div>
            <SheetFoot left={t("foot.profile", { n: 1 })} right={asset.method || ""} />
          </Sheet>

          {ports.length > 0 ? (
            <Sheet>
              <SheetHead icon={NetworkIcon} title={t("ports")} count={`${ports.length}`} />
              <div data-editorial="body" className="px-4 pt-1 pb-2">
                {ports.map((p) => (
                  <RecordRow key={p.port} lead={String(p.port)} cols="64px minmax(0,1fr)">
                    <span className="text-[13px]">{p.service || t("portUnknown")}</span>
                  </RecordRow>
                ))}
              </div>
            </Sheet>
          ) : null}

          <Sheet>
            <SheetHead title={t("findings")} count={`${findings.length}`} hint={t("findingsHint")} />
            {findings.length === 0 ? (
              <div data-editorial="body" className="px-4 py-6 text-center text-muted-foreground text-xs">
                {t("findingsEmpty")}
              </div>
            ) : (
              <div data-editorial="body" className="px-4 pt-1 pb-2">
                {findings.map((f) => (
                  <RecordRow
                    key={f.finding_id ?? f.id}
                    lead={<StatusBadge domain="severity" value={f.severity} dot />}
                    values={[
                      <StatusBadge key="s" domain="finding" value={f.status} dot />,
                      <Link
                        key="l"
                        href={`/function/findings/detail?id=${encodeURIComponent(f.finding_id ?? f.id)}`}
                        className="inline-flex items-center gap-0.5 text-primary hover:underline"
                        aria-label={t("openFinding")}
                      >
                        <ArrowUpRightIcon className="size-3" />
                      </Link>,
                    ]}
                  >
                    <span className="block truncate text-[13px]">{f.name || f.vulnclass || f.summary}</span>
                    <span className="mt-0.5 block truncate text-xs text-muted-foreground">
                      {f.task_description || (f.task_id ? `#${f.task_id}` : "—")}
                    </span>
                  </RecordRow>
                ))}
              </div>
            )}
            <SheetFoot left={t("foot.findings", { n: findings.length })} />
          </Sheet>
        </div>

        <div className="flex min-w-0 flex-col gap-4">
          <Sheet>
            <SheetHead title={t("tasks")} count={`${taskIds.length}`} />
            {taskIds.length === 0 ? (
              <div data-editorial="body" className="px-4 py-6 text-center text-muted-foreground text-xs">
                {t("tasksEmpty")}
              </div>
            ) : (
              <div data-editorial="body" className="px-4 pt-1 pb-2">
                {taskIds.map((tid) => (
                  <RecordRow key={tid} lead="#" cols="44px minmax(0,1fr) auto">
                    <Link
                      href={`/function/tasks/detail?id=${encodeURIComponent(String(tid))}`}
                      className="block truncate text-[13px] hover:underline"
                    >
                      {t("openTask", { id: String(tid) })}
                    </Link>
                  </RecordRow>
                ))}
              </div>
            )}
          </Sheet>

          <Sheet>
            <SheetHead title={t("traffic")} count={`${traffic.length}`} hint={HostOf(asset)} />
            {traffic.length === 0 ? (
              <div data-editorial="body" className="px-4 py-6 text-center text-muted-foreground text-xs">
                {trafficNote || t("trafficEmpty")}
              </div>
            ) : (
              <div data-editorial="body" className="px-4 pt-1 pb-2">
                <GroupHead title={t("trafficRecent")} count={`${traffic.length}`} />
                {traffic.map((x) => (
                  <RecordRow
                    key={x.id}
                    lead={<span className="font-mono text-xs">{x.method}</span>}
                    cols="52px minmax(0,1fr) 40px 44px"
                    values={[
                      <span key="s" className="tabular-nums">
                        {x.status}
                      </span>,
                      <Link
                        key="l"
                        href={`/function/traffic/detail?id=${encodeURIComponent(x.id)}`}
                        className="inline-flex items-center text-primary hover:underline"
                        aria-label={t("openTraffic")}
                      >
                        <ArrowUpRightIcon className="size-3" />
                      </Link>,
                    ]}
                  >
                    <span className="block truncate font-mono text-xs">{x.url}</span>
                  </RecordRow>
                ))}
              </div>
            )}
            <SheetFoot left={t("foot.traffic", { n: traffic.length })} />
          </Sheet>
        </div>
      </div>
    </div>
  );
}

export default function AssetDetailPage() {
  return (
    <React.Suspense fallback={null}>
      <AssetDetailInner />
    </React.Suspense>
  );
}
