"use client";

import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon, PlugIcon, ServerIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { KpiRow, Masthead } from "@/components/editorial";
import { RecordRow, Sheet, SheetFoot, SheetHead } from "@/components/editorial-sheet";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { api } from "@/lib/api";
import type { MCPServer, MCPTool } from "@/lib/types";

/**
 * MCP 服务详情：连接方式、工具清单与「谁能用到这些工具」。
 *
 * 地址：/system/mcp/detail?id=<服务 id>
 *
 * 工具清单来自 `/mcp/:id/tools`（由上次握手缓存）；「暴露给哪些 Agent」不是服务端字段，
 * 而是按工具名去 Agent 的可见性表里反查 —— 一次拉全量 visibility 太贵，这里按每个
 * Agent 的 `tools` 绑定看不出 MCP 维度，因此如实标注为「未采集」，不编造关联。
 */
function MCPDetailInner() {
  const t = useTranslations("mcpDetail");
  const searchParams = useSearchParams();
  const id = Number(searchParams.get("id") ?? "");

  const [server, setServer] = React.useState<MCPServer | null>(null);
  const [tools, setTools] = React.useState<MCPTool[]>([]);
  const [loaded, setLoaded] = React.useState(false);

  React.useEffect(() => {
    if (!Number.isFinite(id) || id <= 0) {
      setLoaded(true);
      return;
    }
    let alive = true;
    api
      .mcpServers()
      .then((rows) => {
        if (alive) setServer(rows.find((s) => s.id === id) ?? null);
      })
      .catch(() => {
        if (alive) setServer(null);
      })
      .finally(() => {
        if (alive) setLoaded(true);
      });
    api
      .mcpTools(id)
      .then((rows) => {
        if (alive) setTools(rows);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [id]);

  const back = (
    <Button asChild size="sm" variant="outline">
      <Link href="/system/mcp">
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

  if (!server) {
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

  const envKeys = Object.keys(server.env ?? {});

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <Masthead
          title={server.name}
          meta={
            <>
              <code className="font-mono text-[10px]">#{server.id}</code>
              <span className="mx-1.5 opacity-40">·</span>
              <span className="font-mono">{server.transport}</span>
              <span className="mx-1.5 opacity-40">·</span>
              <span>{server.enabled ? t("enabled") : t("disabled")}</span>
              {server.insecure ? (
                <>
                  <span className="mx-1.5 opacity-40">·</span>
                  <span className="text-amber-600 dark:text-amber-400">{t("insecure")}</span>
                </>
              ) : null}
            </>
          }
          subtitle={server.url || [server.command, ...(server.args ?? [])].filter(Boolean).join(" ")}
          titleClassName="text-xl"
        />
        {back}
      </div>

      <KpiRow className="grid grid-cols-2 divide-y divide-border/60 border-border/60 border-y sm:grid-cols-3 sm:divide-x sm:divide-y-0 lg:grid-cols-5">
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("kpi.tools")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">
            {tools.length || (server.tools ?? []).length}
          </span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("kpi.transport")}</span>
          <span className="font-semibold text-[15px] leading-none font-mono">{server.transport}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("kpi.env")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{envKeys.length}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("kpi.args")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{(server.args ?? []).length}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-[10px] tracking-[0.08em] text-muted-foreground">{t("kpi.state")}</span>
          <span className="font-semibold text-[15px] leading-none">
            {server.enabled ? t("enabled") : t("disabled")}
          </span>
        </div>
      </KpiRow>

      <div className="grid min-w-0 gap-4 xl:grid-cols-2">
        <Sheet className="min-w-0">
          <SheetHead icon={ServerIcon} title={t("connection")} hint={server.transport} />
          <div data-editorial="body" className="px-4 pt-1 pb-3">
            {server.url ? (
              <RecordRow lead="URL" cols="72px minmax(0,1fr)">
                <code className="block truncate font-mono text-[12px]">{server.url}</code>
              </RecordRow>
            ) : null}
            {server.command ? (
              <RecordRow lead={t("row.command")} cols="72px minmax(0,1fr)">
                <code className="block font-mono text-[12px] [overflow-wrap:anywhere]">
                  {server.command} {(server.args ?? []).join(" ")}
                </code>
              </RecordRow>
            ) : null}
            {envKeys.length > 0 ? (
              <RecordRow lead={t("row.env")} cols="72px minmax(0,1fr)">
                <span className="font-mono text-[12px]">{envKeys.join(" · ")}</span>
                <span className="mt-0.5 block text-[11px] text-muted-foreground">{t("envHint")}</span>
              </RecordRow>
            ) : null}
            <RecordRow lead={t("row.handshake")} cols="72px minmax(0,1fr)">
              <span className="text-[12.5px] text-muted-foreground">{t("handshakeNotCaptured")}</span>
            </RecordRow>
          </div>
          <SheetFoot left={t("connectionFoot")} right={server.insecure ? "insecure" : ""} />
        </Sheet>

        <Sheet className="min-w-0">
          <SheetHead
            icon={PlugIcon}
            title={t("tools")}
            count={`${tools.length || (server.tools ?? []).length}`}
            hint={t("toolsHint")}
          />
          {(tools.length === 0 && (server.tools ?? []).length === 0) ? (
            <div data-editorial="body" className="px-4 py-6 text-center text-muted-foreground text-xs">
              {t("toolsEmpty")}
            </div>
          ) : (
            <div data-editorial="body" className="px-4 pt-1 pb-2">
              {(tools.length > 0 ? tools.map((x) => ({ name: x.name, description: x.description })) : (server.tools ?? []).map((n) => ({ name: n, description: "" }))).map(
                (tool) => (
                  <RecordRow key={tool.name} lead={<Badge variant="outline" className="text-[10px]">mcp</Badge>} cols="56px minmax(0,1fr)">
                    <code className="block truncate font-mono text-[12px]">{tool.name}</code>
                    {tool.description ? (
                      <span className="mt-0.5 block truncate text-[11px] text-muted-foreground">{tool.description}</span>
                    ) : null}
                  </RecordRow>
                ),
              )}
            </div>
          )}
          <SheetFoot left={t("toolsFoot")} right={t("exposureNote")} />
        </Sheet>
      </div>
    </div>
  );
}

export default function MCPDetailPage() {
  return (
    <React.Suspense fallback={null}>
      <MCPDetailInner />
    </React.Suspense>
  );
}
