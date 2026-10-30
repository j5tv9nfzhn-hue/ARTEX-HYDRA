"use client";

import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon, WrenchIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { KpiRow, Masthead } from "@/components/editorial";
import { RecordRow, Sheet, SheetFoot, SheetHead } from "@/components/editorial-sheet";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { api } from "@/lib/api";
import type { CommandRecord, Tool } from "@/lib/types";

/**
 * 工具详情：参数 schema、绑定 Agent、调用统计与最近调用。
 *
 * 地址：/system/tools/detail?key=<工具 key>
 *
 * 工具表没有单条 GET，按 key 从 `/tools` 里定位；最近调用走 `/commands?q=<key>`
 * （后端按命令原文模糊匹配，正好能捞出这个工具名出现在哪几条执行里）。
 */
function ToolDetailInner() {
  const t = useTranslations("toolDetail");
  const searchParams = useSearchParams();
  const key = searchParams.get("key") ?? "";

  const [tool, setTool] = React.useState<Tool | null>(null);
  const [calls, setCalls] = React.useState<CommandRecord[]>([]);
  const [loaded, setLoaded] = React.useState(false);

  React.useEffect(() => {
    if (!key) {
      setLoaded(true);
      return;
    }
    let alive = true;
    api
      .tools()
      .then((rows) => {
        if (alive) setTool(rows.find((x) => x.key === key) ?? null);
      })
      .catch(() => {
        if (alive) setTool(null);
      })
      .finally(() => {
        if (alive) setLoaded(true);
      });
    api
      .commands({ q: key, size: 5 })
      .then((r) => {
        if (alive) setCalls(r.commands ?? []);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [key]);

  const back = (
    <Button asChild size="sm" variant="outline">
      <Link href="/system/tools">
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

  if (!tool) {
    return (
      <div className="flex flex-1 flex-col gap-3">
        <div className="flex items-start justify-between gap-3">
          <Masthead title={t("title")} titleClassName="text-xl" />
          {back}
        </div>
        <p className="border-border/70 border-t pt-6 text-center text-muted-foreground text-sm">
          {t("notFound", { key })}
        </p>
      </div>
    );
  }

  const props = (tool.schema?.properties ?? {}) as Record<string, unknown>;
  const required = (tool.schema?.required ?? []) as string[];
  const propKeys = Object.keys(props);
  const schemaText = JSON.stringify(tool.schema ?? {}, null, 2);

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <Masthead
          title={tool.key}
          meta={
            <>
              <span>{tool.system ? t("system") : t("custom")}</span>
              <span>{tool.kind || "builtin"}</span>
              <span>{tool.enabled ? t("enabled") : t("disabled")}</span>
              {tool.deferred ? (
                <>
                  <span>{t("deferred")}</span>
                </>
              ) : null}
            </>
          }
          subtitle={tool.description}
          titleClassName="text-xl"
        />
        {back}
      </div>

      <KpiRow className="grid grid-cols-2 divide-y divide-border/60 border-border/60 border-y sm:grid-cols-3 sm:divide-x sm:divide-y-0 lg:grid-cols-5">
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.properties")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{propKeys.length}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.required")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{required.length}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.agents")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{tool.agents.length}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.calls")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{tool.calls ?? 0}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.kind")}</span>
          <span className="font-semibold text-[15px] leading-none">{tool.kind || "builtin"}</span>
        </div>
      </KpiRow>

      <div className="grid min-w-0 gap-4 xl:grid-cols-2">
        <Sheet className="min-w-0">
          <SheetHead icon={WrenchIcon} title={t("schema")} count={`${propKeys.length} ${t("props")}`} />
          <div data-editorial="body" className="px-4 pt-1 pb-3">
            {propKeys.length === 0 ? (
              <p className="py-4 text-center text-muted-foreground text-xs">{t("schemaEmpty")}</p>
            ) : (
              propKeys.map((p) => {
                const spec = props[p] as { type?: string; description?: string; enum?: unknown[] };
                return (
                  <RecordRow
                    key={p}
                    lead={<code className="font-mono text-xs">{spec?.type || "any"}</code>}
                    cols="72px minmax(0,1fr) auto"
                    values={[
                      required.includes(p) ? (
                        <Badge key="r" variant="outline">
                          {t("requiredTag")}
                        </Badge>
                      ) : (
                        <span key="r" className="text-xs text-muted-foreground">
                          {t("optionalTag")}
                        </span>
                      ),
                    ]}
                  >
                    <code className="block truncate font-mono text-[12px]">{p}</code>
                    {spec?.description ? (
                      <span className="mt-0.5 block truncate text-xs text-muted-foreground">
                        {spec.description}
                      </span>
                    ) : null}
                  </RecordRow>
                );
              })
            )}
          </div>
          <SheetFoot left={t("schemaFoot")} right={schemaText.length ? `${schemaText.length} B` : ""} />
        </Sheet>

        <div className="flex min-w-0 flex-col gap-4">
          <Sheet>
            <SheetHead title={t("agents")} count={`${tool.agents.length}`} hint={t("agentsHint")} />
            {tool.agents.length === 0 ? (
              <div data-editorial="body" className="px-4 py-5 text-center text-muted-foreground text-xs">
                {t("agentsEmpty")}
              </div>
            ) : (
              <div data-editorial="body" className="px-4 pt-1 pb-2">
                {tool.agents.map((a) => (
                  <RecordRow key={a} lead="@" cols="40px minmax(0,1fr)">
                    <Link
                      href={`/system/agents/detail?key=${encodeURIComponent(a)}`}
                      className="block truncate font-mono text-[12.5px] hover:underline"
                    >
                      {a}
                    </Link>
                  </RecordRow>
                ))}
              </div>
            )}
          </Sheet>

          <Sheet>
            <SheetHead title={t("recent")} count={`${calls.length}`} hint={t("recentHint")} />
            {calls.length === 0 ? (
              <div data-editorial="body" className="px-4 py-5 text-center text-muted-foreground text-xs">
                {t("recentEmpty")}
              </div>
            ) : (
              <div data-editorial="body" className="px-4 pt-1 pb-2">
                {calls.map((c) => (
                  <RecordRow
                    key={c.id}
                    lead={<span className="tabular-nums text-[10.5px]">{c.created_at.slice(5, 16)}</span>}
                    cols="88px minmax(0,1fr) auto"
                    values={[
                      <Link
                        key="l"
                        href={`/function/commands/detail?id=${c.id}${c.exploration_id ? `&task=${c.exploration_id}` : ""}`}
                        className="text-xs text-primary hover:underline"
                      >
                        {t("open")}
                      </Link>,
                    ]}
                  >
                    <span className="block truncate font-mono text-xs">{c.command}</span>
                    <span className="mt-0.5 flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
                      <span className="shrink-0">{c.is_error ? t("callFailed") : t("callOk")}</span>
                      <span className="truncate">{c.worker || "—"}</span>
                    </span>
                  </RecordRow>
                ))}
              </div>
            )}
            <SheetFoot left={t("footRecent")} />
          </Sheet>
        </div>
      </div>
    </div>
  );
}

export default function ToolDetailPage() {
  return (
    <React.Suspense fallback={null}>
      <ToolDetailInner />
    </React.Suspense>
  );
}
