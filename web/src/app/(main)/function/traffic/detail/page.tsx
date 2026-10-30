"use client";

import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon, ClockIcon, Link2Icon } from "lucide-react";
import { useTranslations } from "next-intl";

import { KpiRow, Masthead } from "@/components/editorial";
import { RecordRow, Sheet, SheetFoot, SheetHead } from "@/components/editorial-sheet";
import { HttpCodeBlock } from "@/components/http-code-block";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { api } from "@/lib/api";
import type { TrafficDetail, TrafficExchange } from "@/lib/types";

/**
 * 报文详情：一次交换的请求 / 响应原文与元信息。
 *
 * 地址：/function/traffic/detail?id=<报文 id>
 *
 * 原文走 `/traffic/exchange?id=`；元信息（主机、方法、状态、体量、时间）后端没有
 * 单条 GET，只能从列表里按 id 定位 —— 与工具执行详情同一套取法。定位不到时
 * 仍然把原文显示出来，只是元信息标注为「已滚出窗口」。
 */
function TrafficDetailInner() {
  const t = useTranslations("trafficDetailPage");
  const searchParams = useSearchParams();
  const id = searchParams.get("id") ?? "";

  const [meta, setMeta] = React.useState<TrafficExchange | null>(null);
  const [detail, setDetail] = React.useState<TrafficDetail | null>(null);
  const [loaded, setLoaded] = React.useState(false);

  React.useEffect(() => {
    if (!id) {
      setLoaded(true);
      return;
    }
    let alive = true;
    api
      .trafficExchange(id)
      .then((d) => {
        if (alive) setDetail(d);
      })
      .catch(() => {
        if (alive) setDetail(null);
      })
      .finally(() => {
        if (alive) setLoaded(true);
      });
    api
      .traffic(0, 300)
      .then((r) => {
        if (alive) setMeta((r.exchanges ?? []).find((x) => x.id === id) ?? null);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [id]);

  const back = (
    <Button asChild size="sm" variant="outline">
      <Link href="/function/traffic">
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

  if (!detail) {
    return (
      <div className="flex flex-1 flex-col gap-3">
        <div className="flex items-start justify-between gap-3">
          <Masthead title={t("title")} titleClassName="text-xl" />
          {back}
        </div>
        <p className="border-border/70 border-t pt-6 text-center text-muted-foreground text-sm">
          {t("notFound", { id })}
        </p>
      </div>
    );
  }

  const reqLines = detail.req ? detail.req.split("\n").length : 0;
  const respLines = detail.resp ? detail.resp.split("\n").length : 0;

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <Masthead
          title={meta?.url || t("title")}
          meta={
            <>
              {meta ? <span className="font-mono">{meta.method}</span> : null}
              {meta ? <span className="mx-1.5 opacity-40">·</span> : null}
              {meta ? <span className="tabular-nums">{meta.status}</span> : null}
              {meta ? <span className="mx-1.5 opacity-40">·</span> : null}
              {meta ? <span className="font-mono">{meta.host}</span> : null}
              <span className="mx-1.5 opacity-40">·</span>
              <code className="font-mono text-[10px]">{id}</code>
            </>
          }
          subtitle={meta ? `${meta.ts} · ${meta.content_type || t("noContentType")}` : t("metaGone")}
          titleClassName="text-xl"
        />
        {back}
      </div>

      <KpiRow className="grid grid-cols-2 divide-y divide-border/60 border-border/60 border-y sm:grid-cols-3 sm:divide-x sm:divide-y-0 lg:grid-cols-5">
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("kpi.status")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{meta?.status ?? "—"}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("kpi.method")}</span>
          <span className="font-semibold text-[22px] leading-none font-mono">{meta?.method ?? "—"}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("kpi.reqLines")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{reqLines}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("kpi.respLines")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{respLines}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("kpi.size")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">
            {meta ? `${Math.round(meta.resp_len / 102.4) / 10}K` : "—"}
          </span>
        </div>
      </KpiRow>

      <div className="grid min-w-0 gap-4 xl:grid-cols-2">
        <Sheet className="min-w-0">
          <SheetHead title={t("request")} count={`${reqLines} 行`} hint={t("requestHint")} />
          <div data-editorial="body" className="min-h-0 px-2 py-2">
            {detail.req ? (
              <div className="max-h-[62vh] overflow-auto">
                <HttpCodeBlock raw={detail.req} />
              </div>
            ) : (
              <p className="px-3 py-6 text-center text-muted-foreground text-xs">{t("emptyRequest")}</p>
            )}
          </div>
        </Sheet>

        <Sheet className="min-w-0">
          <SheetHead title={t("response")} count={`${respLines} 行`} hint={meta?.content_type || ""} />
          <div data-editorial="body" className="min-h-0 px-2 py-2">
            {detail.resp ? (
              <div className="max-h-[62vh] overflow-auto">
                <HttpCodeBlock raw={detail.resp} />
              </div>
            ) : (
              <p className="px-3 py-6 text-center text-muted-foreground text-xs">{t("emptyResponse")}</p>
            )}
          </div>
          <SheetFoot left={t("footExchange", { status: meta?.status ?? "—" })} right={meta?.content_type || ""} />
        </Sheet>
      </div>

      <div className="grid min-w-0 gap-4 lg:grid-cols-2">
        <Sheet>
          <SheetHead icon={ClockIcon} title={t("timing")} hint={t("timingHint")} />
          <div data-editorial="body" className="px-4 pt-1 pb-3">
            <RecordRow lead="DNS" cols="72px minmax(0,1fr) auto" values={[t("notCaptured")]}>
              <span className="text-[13px] text-muted-foreground">{t("timingDns")}</span>
            </RecordRow>
            <RecordRow lead="TCP" cols="72px minmax(0,1fr) auto" values={[t("notCaptured")]}>
              <span className="text-[13px] text-muted-foreground">{t("timingTcp")}</span>
            </RecordRow>
            <RecordRow lead="TLS" cols="72px minmax(0,1fr) auto" values={[t("notCaptured")]}>
              <span className="text-[13px] text-muted-foreground">{t("timingTls")}</span>
            </RecordRow>
            <RecordRow lead="TTFB" cols="72px minmax(0,1fr) auto" values={[t("notCaptured")]}>
              <span className="text-[13px] text-muted-foreground">{t("timingTtfb")}</span>
            </RecordRow>
          </div>
          <SheetFoot left={t("timingFoot")} />
        </Sheet>

        <Sheet>
          <SheetHead icon={Link2Icon} title={t("findings")} hint={t("findingsHint")} />
          <div data-editorial="body" className="px-4 py-6 text-center text-muted-foreground text-xs">
            {t("findingsEmpty")}
          </div>
          <SheetFoot left={t("findingsFoot")} />
        </Sheet>
      </div>
    </div>
  );
}

export default function TrafficDetailPage() {
  return (
    <React.Suspense fallback={null}>
      <TrafficDetailInner />
    </React.Suspense>
  );
}
