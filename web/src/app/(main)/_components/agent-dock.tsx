"use client";

import * as React from "react";

import { ChevronLeftIcon, ChevronRightIcon, NetworkIcon, RadioTowerIcon, TriangleAlertIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { agentLive } from "@/lib/agent-live-store";
import { currentTask } from "@/lib/current-task-store";
import type { AgentLiveNode } from "@/lib/types";
import { cn } from "@/lib/utils";

/**
 * 子代理停靠栏：右侧滑出，不跳页、不占主区布局。
 *
 * 三条硬约束（对应需求）：
 *  1. 收缩态只占 44px 竖条，展开是覆盖式 fixed 面板 —— 主对话区的宽度不变，
 *     不会触发 reflow，也不会把聊天滚动位置顶掉；
 *  2. 数据来自独立的 /api/agents/stream，且走外部 store 合帧 —— 子代理刷日志
 *     只重渲染这个组件，主聊天树不动；
 *  3. 状态全部显式：排队 / 运行 / 等待交卷 / 完成 / 失败 / 疑似卡死，没有一个是
 *     "无限转圈"。`stalled` 来自后端 10 分钟无活动的判定。
 */

const ACTIVE_STATES = new Set(["running", "waiting"]);
const TERMINAL_STATES = new Set(["completed", "stopped", "crashed", "failed"]);

function stateTone(state: string): string {
  if (state === "running") return "bg-emerald-500";
  if (state === "waiting") return "bg-sky-500";
  if (state === "completed") return "bg-muted-foreground/50";
  if (state === "stopped") return "bg-amber-500";
  if (state === "crashed" || state === "failed") return "bg-destructive";
  if (state === "budget_paused") return "bg-amber-500";
  return "bg-muted-foreground/40";
}

function elapsed(from: string, to?: string): string {
  const start = Date.parse(from);
  if (Number.isNaN(start)) return "—";
  const end = to ? Date.parse(to) : Date.now();
  const sec = Math.max(0, Math.round((end - start) / 1000));
  if (sec < 60) return `${sec}s`;
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m}m${sec % 60}s`;
  return `${Math.floor(m / 60)}h${m % 60}m`;
}

function statusTone(status: string): string {
  if (status === "live") return "bg-emerald-500";
  if (status === "resync") return "bg-amber-500";
  return "bg-muted-foreground/40";
}

/** 把扁平节点表还原成森林：谁派的谁、各派了几个，全由 parent_id 决定。 */
function buildForest(nodes: Record<string, AgentLiveNode>) {
  const children = new Map<string, AgentLiveNode[]>();
  const roots: AgentLiveNode[] = [];
  for (const node of Object.values(nodes)) {
    const pid = node.parent_id ?? "";
    if (!pid || !nodes[pid]) {
      roots.push(node);
      continue;
    }
    const arr = children.get(pid) ?? [];
    arr.push(node);
    children.set(pid, arr);
  }
  const byStart = (a: AgentLiveNode, b: AgentLiveNode) => Date.parse(a.started_at) - Date.parse(b.started_at);
  roots.sort(byStart);
  for (const arr of children.values()) arr.sort(byStart);
  return { roots, children };
}

export function AgentDock() {
  const t = useTranslations("agentDock");
  const taskId = React.useSyncExternalStore(currentTask.subscribe, currentTask.getSnapshot, currentTask.getSnapshot);
  const snap = React.useSyncExternalStore(agentLive.subscribe, agentLive.getSnapshot, agentLive.getSnapshot);
  const [open, setOpen] = React.useState(false);
  const [selected, setSelected] = React.useState<string | null>(null);

  // 会话切换 → 换一条按新 task_id 过滤的事件流。没有绑定任务（通用对话）时
  // 不建连接，停靠栏也不出现：这里只服务"某个任务正在并行跑子代理"的场景。
  React.useEffect(() => {
    agentLive.setTask(taskId);
  }, [taskId]);

  React.useEffect(() => {
    agentLive.start();
    return () => agentLive.stop();
  }, []);

  const nodes = snap.nodes;
  const all = React.useMemo(() => Object.values(nodes), [nodes]);
  const active = all.filter((n) => ACTIVE_STATES.has(n.state));
  const queued = React.useMemo(() => {
    const ids = new Set<string>();
    for (const s of Object.values(snap.slots)) for (const id of s.queue ?? []) ids.add(id);
    return [...ids];
  }, [snap.slots]);
  const stalled = React.useMemo(() => {
    const ids = new Set<string>();
    for (const s of Object.values(snap.slots)) for (const id of s.stalled ?? []) ids.add(id);
    return ids;
  }, [snap.slots]);
  const slot = React.useMemo(() => Object.values(snap.slots)[0], [snap.slots]);
  const { roots, children } = React.useMemo(() => buildForest(nodes), [nodes]);

  // 只服务"当前会话绑定了任务、且该任务有子代理"的场景：
  // 无绑定任务 → 不显示；有任务但没在跑的 agent → 也不占屏。
  const hasWork = all.length > 0 || queued.length > 0;
  if (!taskId || !hasWork) return null;

  const shown = selected && nodes[selected] ? selected : (active[0]?.id ?? all[0]?.id ?? null);
  const logs = shown ? (snap.logs[shown] ?? []) : [];

  return (
    <div className="pointer-events-none fixed inset-y-0 right-0 z-40 flex">
      {/* 收缩态竖条：只报告"在跑几个 / 排队几个"，点击展开 */}
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        aria-label={t("toggle")}
        className={cn(
          "pointer-events-auto flex h-full w-11 flex-col items-center gap-2 border-l bg-background/95 py-3 backdrop-blur",
          open && "hidden",
        )}
      >
        {open ? <ChevronLeftIcon className="size-4" /> : <ChevronRightIcon className="size-4" />}
        <NetworkIcon className="size-4 text-muted-foreground" />
        <Badge variant={active.length > 0 ? "secondary" : "outline"} className="px-1.5 py-0 text-[10px]">
          {active.length}
        </Badge>
        {queued.length > 0 ? (
          <Badge variant="outline" className="px-1.5 py-0 text-[10px]">
            {queued.length}
          </Badge>
        ) : null}
        {stalled.size > 0 ? <TriangleAlertIcon className="size-4 text-amber-500" /> : null}
      </button>

      {/* 展开态：覆盖式抽屉，不参与主区布局计算 */}
      <aside
        className={cn(
          "pointer-events-auto flex h-full w-[420px] flex-col border-l bg-background shadow-xl",
          !open && "hidden",
        )}
      >
        <header className="flex shrink-0 items-center gap-2 border-b px-3 py-2">
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            onClick={() => setOpen(false)}
            aria-label={t("collapse")}
          >
            <ChevronRightIcon className="size-4" />
          </Button>
          <RadioTowerIcon className="size-4 text-muted-foreground" />
          <span className="font-medium text-sm">{t("title")}</span>
          <span className="ml-auto flex items-center gap-1.5 text-[11px] text-muted-foreground">
            <span className={cn("size-1.5 rounded-full", statusTone(snap.status))} />
            {t(`status.${snap.status}`)}
          </span>
        </header>

        {/* 槽位水位：还能开几条并行 + 排队曲线 */}
        <div className="flex shrink-0 items-center gap-2 border-b px-3 py-1.5 text-[11px] text-muted-foreground">
          <span>{t("slots", { used: slot?.used ?? active.length, limit: slot?.limit ?? 0 })}</span>
          {slot?.chain ? <code className="rounded bg-muted px-1 py-0.5 tabular-nums">{slot.chain}</code> : null}
          {queued.length > 0 ? (
            <span className="text-amber-600 dark:text-amber-500">{t("queuedCount", { count: queued.length })}</span>
          ) : null}
        </div>

        {/* 上半：层级树。谁派的、派了几个，一眼可见 */}
        <div className="min-h-0 flex-1 overflow-y-auto px-2 py-1.5">
          {roots.length === 0 ? (
            <p className="px-2 py-6 text-center text-muted-foreground text-xs">{t("empty")}</p>
          ) : (
            <ul className="flex flex-col gap-0.5">
              {roots.map((node) => (
                <Subtree
                  key={node.id}
                  node={node}
                  depth={0}
                  kidsMap={children}
                  selected={shown}
                  onSelect={setSelected}
                  stalled={stalled}
                  t={t}
                />
              ))}
            </ul>
          )}
          {queued.length > 0 ? (
            <div className="mt-2 rounded-md border border-dashed px-2 py-1.5">
              <p className="mb-1 text-[11px] text-muted-foreground">{t("queueTitle")}</p>
              {queued.map((id) => (
                <div key={id} className="flex items-center gap-1.5 text-[11px] text-amber-600 dark:text-amber-500">
                  <span className="size-1.5 rounded-full bg-amber-500" />
                  {id}
                </div>
              ))}
            </div>
          ) : null}
        </div>

        {/* 下半：选中 agent 的日志。滚动只在"本来就在底部"时跟随 */}
        <section className="flex h-[42%] shrink-0 flex-col border-t">
          <div className="flex shrink-0 items-center gap-2 border-b px-3 py-1.5">
            <span className="truncate font-medium text-[11px]">
              {shown ? nodes[shown]?.role || shown : t("noSelection")}
            </span>
            <span className="ml-auto text-[10px] text-muted-foreground">{t("logCount", { count: logs.length })}</span>
          </div>
          <LogPane logs={logs} />
        </section>
      </aside>
    </div>
  );
}

type Translator = ReturnType<typeof useTranslations>;

function Subtree({
  node,
  depth,
  kidsMap,
  selected,
  onSelect,
  stalled,
  t,
}: {
  node: AgentLiveNode;
  depth: number;
  kidsMap: Map<string, AgentLiveNode[]>;
  selected: string | null;
  onSelect: (id: string) => void;
  stalled: Set<string>;
  t: Translator;
}) {
  const kids = kidsMap.get(node.id) ?? [];
  const isStalled = stalled.has(node.id);
  const isTerminal = TERMINAL_STATES.has(node.state);
  return (
    <li>
      <button
        type="button"
        onClick={() => onSelect(node.id)}
        className={cn(
          "flex w-full items-start gap-1.5 rounded px-1.5 py-1 text-left text-xs transition-colors hover:bg-accent/50",
          selected === node.id && "bg-accent/60",
        )}
        style={{ paddingLeft: 6 + depth * 14 }}
      >
        <span
          className={cn("mt-1 size-1.5 shrink-0 rounded-full", isStalled ? "bg-amber-500" : stateTone(node.state))}
        />
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-1.5">
            <span className="truncate font-medium">{node.role?.length ? node.role : node.id}</span>
            {node.root ? (
              <Badge variant="outline" className="px-1 py-0 text-[9px]">
                root
              </Badge>
            ) : null}
            {kids.length > 0 ? (
              <Badge variant="secondary" className="px-1 py-0 text-[9px]">
                {t("children", { count: kids.length })}
              </Badge>
            ) : null}
            {isStalled ? <TriangleAlertIcon className="size-3 text-amber-500" /> : null}
          </span>
          <span className="flex items-center gap-2 text-[10px] text-muted-foreground">
            <span>{isStalled ? t("state.stalled") : t(`state.${node.state}`)}</span>
            <span className="tabular-nums">{elapsed(node.started_at, isTerminal ? node.last_active : undefined)}</span>
            {node.turn > 0 ? <span className="tabular-nums">#{node.turn}</span> : null}
          </span>
        </span>
      </button>
      {kids.length > 0 ? (
        <ul className="flex flex-col gap-0.5">
          {kids.map((kid) => (
            <Subtree
              key={kid.id}
              node={kid}
              depth={depth + 1}
              kidsMap={kidsMap}
              selected={selected}
              onSelect={onSelect}
              stalled={stalled}
              t={t}
            />
          ))}
        </ul>
      ) : null}
    </li>
  );
}

function LogPane({ logs }: { logs: { seq: number; at: string; kind: string; state?: string; summary?: string }[] }) {
  const ref = React.useRef<HTMLDivElement>(null);
  const stick = React.useRef(true);

  React.useEffect(() => {
    const el = ref.current;
    if (!el || !stick.current) return;
    el.scrollTop = el.scrollHeight;
  }, []);

  return (
    <div
      ref={ref}
      onScroll={() => {
        const el = ref.current;
        if (!el) return;
        stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
      }}
      className="min-h-0 flex-1 overflow-y-auto px-3 py-1.5 font-mono text-[11px] leading-relaxed"
    >
      {logs.length === 0 ? <p className="py-6 text-center font-sans text-muted-foreground text-xs">—</p> : null}
      {logs.map((ev) => (
        <div key={ev.seq} className="flex gap-2">
          <span className="shrink-0 text-muted-foreground/60 tabular-nums">{new Date(ev.at).toLocaleTimeString()}</span>
          <span className={cn("shrink-0", ev.state ? "text-sky-600 dark:text-sky-400" : "text-muted-foreground")}>
            {ev.kind}
          </span>
          <span className="min-w-0 break-words">{ev.summary}</span>
        </div>
      ))}
    </div>
  );
}
