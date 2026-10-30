"use client";

import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon, CoinsIcon, CpuIcon, RouteIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { KpiRow, Masthead } from "@/components/editorial";
import { RecordRow, Sheet, SheetFoot, SheetHead } from "@/components/editorial-sheet";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { api } from "@/lib/api";
import type { LLMProfile, LLMProvider, ProfileUsage } from "@/lib/types";

function fmtCost(n: number) {
  if (!Number.isFinite(n) || n <= 0) return "—";
  if (n < 0.01) return `$${n.toFixed(4)}`;
  if (n < 1) return `$${n.toFixed(3)}`;
  return `$${n.toFixed(2)}`;
}

function fmtTokens(n: number) {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(n);
}

/**
 * 模型配置详情：端点、参数、近 30 天用量与回退链位置。
 *
 * 地址：/system/llm/detail?id=<配置 id>
 *
 * 「价格」不在配置里 —— 价格表是全局 `settings.price_table`，后端按模型名最长匹配
 * 读时计价，所以这里只显示用量账本里已经算好的费用，不假装配置上有单价字段。
 */
function LLMDetailInner() {
  const t = useTranslations("llmProfileDetail");
  const searchParams = useSearchParams();
  const id = searchParams.get("id") ?? "";

  const [profile, setProfile] = React.useState<LLMProfile | null>(null);
  const [providers, setProviders] = React.useState<LLMProvider[]>([]);
  const [chain, setChain] = React.useState<LLMProfile[]>([]);
  const [usage, setUsage] = React.useState<ProfileUsage | null>(null);
  const [loaded, setLoaded] = React.useState(false);

  React.useEffect(() => {
    if (!id) {
      setLoaded(true);
      return;
    }
    let alive = true;
    api
      .llmProfiles()
      .then((rows) => {
        if (!alive) return;
        setProfile(rows.find((p) => p.id === id) ?? null);
        setChain([...rows].sort((a, b) => (b.priority ?? 0) - (a.priority ?? 0)));
      })
      .catch(() => {
        if (alive) setProfile(null);
      })
      .finally(() => {
        if (alive) setLoaded(true);
      });
    api
      .llmProviders()
      .then((rows) => {
        if (alive) setProviders(rows);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [id]);

  // 账本按「配置名」聚合，所以只能等 profile 拿到后再补一次匹配。
  React.useEffect(() => {
    if (!profile) return;
    let alive = true;
    api
      .usageStats(30)
      .then((s) => {
        if (!alive) return;
        setUsage(s.by_profile.find((p) => p.profile_name === profile.name) ?? null);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [profile]);

  const back = (
    <Button asChild size="sm" variant="outline">
      <Link href="/system/llm">
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

  if (!profile) {
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

  const provider = providers.find((p) => p.id === profile.provider_id);
  const modalities = (profile.effective_input_modalities || profile.input_modalities || "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <Masthead
          title={profile.name}
          meta={
            <>
              <code className="font-mono text-xs">#{profile.id}</code>
              <span className="font-mono">{profile.model}</span>
              <span className="font-mono">{profile.format}</span>
              {profile.is_default ? (
                <>
                  <span>{t("defaultTag")}</span>
                </>
              ) : null}
            </>
          }
          subtitle={provider?.name || profile.provider_name || t("noProvider")}
          titleClassName="text-xl"
        />
        {back}
      </div>

      <KpiRow className="grid grid-cols-2 divide-y divide-border/60 border-border/60 border-y sm:grid-cols-3 sm:divide-x sm:divide-y-0 lg:grid-cols-5">
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.context")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">
            {profile.context_window_k ? `${profile.context_window_k}K` : "—"}
          </span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.rate")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">
            {profile.rate_per_minute || "—"}
          </span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.maxTokens")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">
            {profile.max_tokens ? fmtTokens(profile.max_tokens) : "—"}
          </span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.calls")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{usage?.calls ?? 0}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.cost")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{fmtCost(usage?.cost_usd ?? 0)}</span>
        </div>
      </KpiRow>

      <div className="grid min-w-0 gap-4 xl:grid-cols-2">
        <Sheet className="min-w-0">
          <SheetHead icon={CpuIcon} title={t("params")} hint={t("paramsHint")} />
          <div data-editorial="body" className="px-4 pt-1 pb-3">
            <RecordRow lead={t("row.model")} cols="104px minmax(0,1fr) auto" values={[profile.model]}>
              <span className="text-[13px] text-muted-foreground">{profile.format}</span>
            </RecordRow>
            <RecordRow lead={t("row.rate")} cols="104px minmax(0,1fr) auto" values={[`${profile.rate_per_second}/s`]}>
              <span className="text-[13px] text-muted-foreground tabular-nums">{profile.rate_per_minute}/min</span>
            </RecordRow>
            <RecordRow
              lead={t("row.thinking")}
              cols="104px minmax(0,1fr) auto"
              values={[profile.reasoning_effort || profile.thinking_type || "—"]}
            >
              <span className="text-[13px] text-muted-foreground">{t("thinkingNote")}</span>
            </RecordRow>
            <RecordRow
              lead={t("row.streaming")}
              cols="104px minmax(0,1fr) auto"
              values={[profile.streaming === false ? t("off") : t("on")]}
            >
              <span className="text-[13px] text-muted-foreground">{t("streamingNote")}</span>
            </RecordRow>
            <RecordRow
              lead={t("row.modalities")}
              cols="104px minmax(0,1fr) auto"
              values={[modalities.length ? modalities.join(" / ") : "text"]}
            >
              <span className="text-[13px] text-muted-foreground">{t("modalitiesNote")}</span>
            </RecordRow>
            {profile.pool_exclude ? (
              <RecordRow lead={t("row.pool")} cols="104px minmax(0,1fr) auto" values={[t("excluded")]}>
                <span className="text-[13px] text-muted-foreground">{t("poolNote")}</span>
              </RecordRow>
            ) : null}
          </div>
          <SheetFoot left={t("paramsFoot")} right={profile.max_tokens_field || ""} />
        </Sheet>

        <Sheet className="min-w-0">
          <SheetHead icon={RouteIcon} title={t("endpoint")} hint={provider?.base_url || profile.base_url || ""} />
          <div data-editorial="body" className="px-4 pt-1 pb-3">
            <RecordRow lead={t("row.provider")} cols="104px minmax(0,1fr) auto" values={[provider?.name || "—"]}>
              <span className="font-mono text-xs text-muted-foreground">{profile.provider_id || "0"}</span>
            </RecordRow>
            <RecordRow lead={t("row.baseUrl")} cols="104px minmax(0,1fr) auto">
              <code className="block truncate font-mono text-xs">
                {provider?.base_url || profile.base_url || "—"}
              </code>
            </RecordRow>
            <RecordRow lead={t("row.proxy")} cols="104px minmax(0,1fr) auto">
              <code className="block truncate font-mono text-xs">
                {provider?.proxy || profile.proxy || "—"}
              </code>
            </RecordRow>
            <RecordRow lead={t("row.key")} cols="104px minmax(0,1fr) auto">
              <code className="font-mono text-xs text-muted-foreground">
                {provider?.api_key_hint || profile.api_key_hint || "—"}
              </code>
              <span className="mt-0.5 block text-xs text-muted-foreground">{t("keyNote")}</span>
            </RecordRow>
            {profile.session_header_key ? (
              <RecordRow lead={t("row.sessionHeader")} cols="104px minmax(0,1fr) auto">
                <code className="font-mono text-xs">{profile.session_header_key}</code>
              </RecordRow>
            ) : null}
          </div>
          <SheetFoot left={t("endpointFoot")} />
        </Sheet>
      </div>

      <div className="grid min-w-0 gap-4 lg:grid-cols-2">
        <Sheet>
          <SheetHead icon={CoinsIcon} title={t("usage")} hint={t("usageHint")} count={usage ? `${usage.calls}` : "0"} />
          <div data-editorial="body" className="px-4 pt-1 pb-3">
            <RecordRow lead={t("row.input")} cols="104px minmax(0,1fr) auto" values={[fmtTokens(usage?.input_tokens ?? 0)]}>
              <span className="text-[13px] text-muted-foreground">{t("row.tokensIn")}</span>
            </RecordRow>
            <RecordRow
              lead={t("row.output")}
              cols="104px minmax(0,1fr) auto"
              values={[fmtTokens(usage?.output_tokens ?? 0)]}
            >
              <span className="text-[13px] text-muted-foreground">{t("row.tokensOut")}</span>
            </RecordRow>
            <RecordRow lead={t("row.cache")} cols="104px minmax(0,1fr) auto" values={[fmtTokens(usage?.cache_read_tokens ?? 0)]}>
              <span className="text-[13px] text-muted-foreground">{t("cacheWrite", { n: fmtTokens(usage?.cache_write_tokens ?? 0) })}</span>
            </RecordRow>
            <RecordRow lead={t("row.tasks")} cols="104px minmax(0,1fr) auto" values={[String(usage?.tasks ?? 0)]}>
              <span className="text-[13px] text-muted-foreground">{t("row.tasksNote")}</span>
            </RecordRow>
          </div>
          <SheetFoot left={t("usageFoot")} />
        </Sheet>

        <Sheet>
          <SheetHead title={t("chain")} count={`${chain.length}`} hint={t("chainHint")} />
          <div data-editorial="body" className="px-4 pt-1 pb-2">
            {chain.map((p, i) => (
              <RecordRow
                key={p.id}
                lead={<span className="tabular-nums">{String(i + 1).padStart(2, "0")}</span>}
                cols="36px minmax(0,1fr) auto"
                values={[
                  p.id === profile.id ? (
                    <Badge key="b" variant="outline">
                      {t("here")}
                    </Badge>
                  ) : (
                    <Link
                      key="b"
                      href={`/system/llm/detail?id=${encodeURIComponent(p.id)}`}
                      className="text-xs text-primary hover:underline"
                    >
                      {t("open")}
                    </Link>
                  ),
                ]}
              >
                <span className="block truncate text-[13px]">{p.name}</span>
                <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
                  <span className="truncate">{p.model}</span>
                  <span className="shrink-0">{p.pool_exclude ? t("excluded") : t("inPool")}</span>
                </span>
              </RecordRow>
            ))}
          </div>
          <SheetFoot left={t("chainFoot")} right={t("chainOrder")} />
        </Sheet>
      </div>
    </div>
  );
}

export default function LLMProfileDetailPage() {
  return (
    <React.Suspense fallback={null}>
      <LLMDetailInner />
    </React.Suspense>
  );
}
