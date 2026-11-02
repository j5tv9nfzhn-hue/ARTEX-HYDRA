"use client";

import * as React from "react";

import { ExternalLink, X } from "lucide-react";

import { serverSource, useResource } from "@/data/serverSource";
import { useViewStore } from "@/stores/views/view-store";

/**
 * 详情面板：把原先的"占位"换成会拉真实数据的通用详情视图。
 *
 * 按 kind 分发取数；未注册的类型不报错，降级为「该类型尚未接入面板」+
 * 「整页打开」，保证任何列表页点进来都有可用反馈，而不是空白。
 */

type Fetcher = (id: string) => Promise<unknown>;

const FETCHERS: Record<string, Fetcher> = {
  // 单条直取
  tasks: (id) => serverSource.task(id),
  approvals: (id) => serverSource.interceptDetail(Number(id)),
  // 列表过滤（后端没有单条 GET 端点，保持后端零改动）
  findings: async (id) => {
    const all = await serverSource.findings();
    return (all ?? []).find((item) => String(item.id) === id) ?? null;
  },
  assets: async (id) => {
    const res = await serverSource.assets("", 200, 0);
    return res?.assets?.find((item) => String(item.id) === id) ?? null;
  },
  commands: async (id) => {
    const res = await serverSource.commands({ size: 200 });
    return res?.commands?.find((item) => String(item.id) === id) ?? null;
  },
  llm: async (id) => {
    const all = await serverSource.llmProfiles();
    return all.find((item) => String(item.id) === id) ?? null;
  },
  mcp: async (id) => {
    const all = await serverSource.mcpServers();
    return all.find((item) => String(item.id) === id) ?? null;
  },
  // Tool 的主键是 key（见 lib/types.ts 的 Tool 定义），不是 id。
  tools: async (id) => {
    const all = await serverSource.tools();
    return all.find((item) => item.key === id) ?? null;
  },
};

const KIND_LABEL: Record<string, string> = {
  tasks: "任务",
  findings: "发现",
  approvals: "审批记录",
  assets: "资产",
  commands: "工具执行",
  llm: "LLM 档案",
  mcp: "MCP 服务器",
  tools: "工具",
  agents: "Agent",
  skills: "Skill",
  traffic: "流量",
  "llm-records": "LLM 录制",
};

/** 详情路由末段 → 整页地址，供「整页打开」使用。 */
const KIND_ROUTE: Record<string, string> = {
  tasks: "/function/tasks/detail",
  findings: "/function/findings/detail",
  approvals: "/system/intercept/approvals/detail",
  assets: "/function/assets/detail",
  commands: "/function/commands/detail",
  llm: "/system/llm/detail",
  mcp: "/system/mcp/detail",
  tools: "/system/tools/detail",
  agents: "/system/agents/detail",
  skills: "/system/skills/detail",
  traffic: "/function/traffic/detail",
  "llm-records": "/function/llm-records/detail",
};

function isRenderable(value: unknown): boolean {
  return (
    value !== null &&
    value !== undefined &&
    typeof value !== "function" &&
    typeof value !== "object"
  );
}

export function PanelHost() {
  const selected = useViewStore((s) => s.selected);
  const closePanel = useViewStore((s) => s.closePanel);

  const kind = selected?.kind ?? "";
  const id = selected?.id ?? "";
  const fetcher = FETCHERS[kind];

  const { data, error, loading } = useResource<unknown>(
    () => (fetcher ? fetcher(id) : Promise.resolve(null)),
    `${kind}:${id}`,
    { enabled: Boolean(fetcher) && Boolean(id) },
  );

  const fullPageUrl = KIND_ROUTE[kind] ? `${KIND_ROUTE[kind]}?id=${encodeURIComponent(id)}` : "";

  return (
    <aside className="flex w-96 shrink-0 flex-col overflow-hidden border-l">
      <header className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
        <span className="truncate text-sm font-medium">
          {KIND_LABEL[kind] ?? kind}
          {id ? <span className="ml-1 text-muted-foreground text-xs">#{id}</span> : null}
        </span>
        <div className="ml-auto flex items-center gap-1">
          {fullPageUrl ? (
            <a
              href={fullPageUrl}
              className="rounded p-1.5 text-muted-foreground transition-colors hover:bg-accent"
              title="整页打开"
              aria-label="整页打开"
            >
              <ExternalLink className="size-3.5" />
            </a>
          ) : null}
          <button
            type="button"
            onClick={closePanel}
            className="rounded p-1.5 text-muted-foreground transition-colors hover:bg-accent"
            title="关闭"
            aria-label="关闭"
          >
            <X className="size-3.5" />
          </button>
        </div>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto p-3 text-xs">
        {!fetcher ? (
          <p className="text-muted-foreground">
            该类型尚未接入详情面板，可点击右上角在整页中打开。
          </p>
        ) : loading ? (
          <p className="text-muted-foreground">加载中…</p>
        ) : error ? (
          <p className="text-destructive">加载失败：{error.message}</p>
        ) : !data ? (
          <p className="text-muted-foreground">未找到该记录。</p>
        ) : (
          <dl className="space-y-1">
            {Object.entries(data as Record<string, unknown>)
              .filter(([, value]) => isRenderable(value))
              .slice(0, 80)
              .map(([key, value]) => (
                <div key={key} className="flex gap-2 border-b py-1 last:border-0">
                  <dt className="w-2/5 shrink-0 truncate text-muted-foreground">{key}</dt>
                  <dd className="min-w-0 flex-1 break-words">{String(value)}</dd>
                </div>
              ))}
          </dl>
        )}
      </div>
    </aside>
  );
}
