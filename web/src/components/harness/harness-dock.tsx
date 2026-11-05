"use client";

// harness-dock —— 对话工作区右侧的面板坞（Zed 的 right dock 形态）。
//
// 为什么不是「全屏对话 + 独立窗口」：那一版把会话顶到整个视口，用户要同时看任务
// 详情/子代理就得开新浏览器窗口，直接多一份渲染进程内存。这里的做法回到 IDE 的
// 通行形态——侧边栏收窄一点、工作区（含 Header/composer 的卡片）占满剩余空间，
// 右侧再挂一条**可开可关、可拖宽**的坞：面板与转写并排，不抢主位，也不新开窗口。
//
// 面板清单（都只读，全部来自既有 API，无新增后端契约）：
//   tasks      任务      /tasks + /exploration/intents（任务 → Worker 两级）
//   todo       进度      当前线程最近一次 TodoWrite（旧版 Todo 进度的升级版）
//   approvals  审批      /intercept/pending（只读，决定仍然跳审批页做）
//   checkpoints 检查点   /conversations/{id}/checkpoints（读，revert 仍走消息气泡）
//   files      文件      /conversations/{id}/messages/latest 的附件清单
//
// 预留但本轮不做的两个（Zed 里最常见的两个坞面板，数据源不在本仓库后端）：
//   browser    内置浏览器 —— 需要同源可嵌入的 URL + 后端代理，跨域站点会被
//              X-Frame-Options/CSP 拒；等确定「嵌什么」再接。
//   git        变更记录   —— 仓库当前没有 git 只读端点（server 侧无 /git 路由），
//              要先加一个只读 /api/workspace/git/log，不能在前端伪造。
// 两个入口先做出来并标注「未接后端」，点开如实说明，而不是给一个空壳假装能用。

import * as React from "react";

import {
  ActivityIcon,
  CheckCheckIcon,
  ChevronRightIcon,
  ExternalLinkIcon,
  Loader2Icon,
  PaperclipIcon,
  ShieldAlertIcon,
  XIcon,
} from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { api } from "@/lib/api";
import type { Checkpoint, InterceptPending } from "@/lib/types";
import { cn } from "@/lib/utils";

export type HarnessDockPanel = "tasks" | "approvals" | "checkpoints" | "files";

const DOCK_STORAGE_KEY = "artex.chat.dock.panel";
const DOCK_WIDTH_KEY = "artex.chat.dock.width";
const DOCK_MIN = 280;
const DOCK_MAX = 620;
const DOCK_DEFAULT = 360;
// 主列至少要留这么宽，否则坞就是在抢阅读区（Zed 的 flexible dock 同一约束）。
const MAIN_MIN = 420;

export function readDockPanel(): HarnessDockPanel | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(DOCK_STORAGE_KEY);
    return raw && PANEL_IDS.includes(raw as HarnessDockPanel) ? (raw as HarnessDockPanel) : null;
  } catch {
    return null;
  }
}

/**
 * 惰性读取已保存的坞宽。
 *
 * 放进 useState 的初始化函数而不是挂载后的 effect：否则首帧先按默认宽度画一遍，
 * 下一帧再跳到记忆宽度，看得见一次抽动。坞只在客户端挂载（面板 id 也是从
 * localStorage 读的），所以这里不需要考虑服务端渲染的一致性。
 */
function readStoredWidth(): number {
  if (typeof window === "undefined") return DOCK_DEFAULT;
  try {
    const raw = window.localStorage.getItem(DOCK_WIDTH_KEY);
    const n = raw ? Number(raw) : NaN;
    return Number.isFinite(n) && n >= DOCK_MIN ? Math.min(n, DOCK_MAX) : DOCK_DEFAULT;
  } catch {
    return DOCK_DEFAULT;
  }
}

const PANEL_IDS: HarnessDockPanel[] = ["tasks", "approvals", "checkpoints", "files"];

// 面板 id → i18n 键。集中一处，避免标题、切换条、坞内切换三处各写一份嵌套三元。
const PANEL_LABEL_KEY: Record<HarnessDockPanel, string> = {
  tasks: "panel.tasks",
  approvals: "panel.approvals",
  checkpoints: "panel.checkpoints",
  files: "panel.files",
};

// DockSwitcher 是头部右侧那排小图标：点一下开/关对应面板，再点同一个则收起。
// 同时刻只显示一个面板（Zed 的 dock 里也只有 active panel 在亮），不做多标签堆叠。
export function DockSwitcher({
  value,
  onChange,
}: {
  value: HarnessDockPanel | null;
  onChange: (next: HarnessDockPanel | null) => void;
}) {
  const t = useTranslations("chat.harness.dock");
  React.useEffect(() => {
    try {
      if (value) window.localStorage.setItem(DOCK_STORAGE_KEY, value);
      else window.localStorage.removeItem(DOCK_STORAGE_KEY);
    } catch {
      /* 隐私模式下忽略 */
    }
  }, [value]);

  const items: { id: HarnessDockPanel; icon: React.ReactNode; label: string }[] = [
    { id: "tasks", icon: <ActivityIcon className="size-3.5" />, label: t("panel.tasks") },
    { id: "approvals", icon: <ShieldAlertIcon className="size-3.5" />, label: t("panel.approvals") },
    { id: "checkpoints", icon: <CheckCheckIcon className="size-3.5" />, label: t("panel.checkpoints") },
    { id: "files", icon: <PaperclipIcon className="size-3.5" />, label: t("panel.files") },
  ];

  return (
    <div className="flex shrink-0 items-center gap-0.5 rounded-lg border border-border/70 bg-muted/40 p-0.5">
      {items.map((item) => {
        const active = value === item.id;
        return (
          // 与侧边栏同一套气泡：Radix Tooltip（延迟、箭头、主题色都由全局
          // TooltipProvider 统一），不用浏览器原生 title —— 原生提示不可样式化、
          // 出现延迟由操作系统决定，和侧边栏观感对不上。
          <Tooltip key={item.id}>
            <TooltipTrigger asChild>
              <button
                type="button"
                aria-pressed={active}
                aria-label={active ? t("collapse") : item.label}
                onClick={() => onChange(active ? null : item.id)}
                className={cn(
                  "rounded-md p-1 transition-colors",
                  active
                    ? "bg-background text-foreground shadow-2xs"
                    : "text-muted-foreground hover:bg-background/60 hover:text-foreground",
                )}
              >
                {item.icon}
              </button>
            </TooltipTrigger>
            <TooltipContent side="bottom" align="center">
              {active ? t("collapse") : item.label}
            </TooltipContent>
          </Tooltip>
        );
      })}
    </div>
  );
}

export type DockContext = {
  /** 当前会话 id。坞内只有检查点面板需要它，传 id 而不是整个 Conversation，
   *  这样流式消息更新导致 Conversation 换引用时，坞不会被连带重渲染。 */
  conversationId: number;
  onOpenTask?: (taskId: string) => void;
};

export const HarnessDock = React.memo(function HarnessDock({
  panel,
  onPanelChange,
  onClose,
  context,
}: {
  panel: HarnessDockPanel;
  onPanelChange: (next: HarnessDockPanel) => void;
  onClose: () => void;
  context: DockContext;
}) {
  const t = useTranslations("chat.harness.dock");
  const asideRef = React.useRef<HTMLElement | null>(null);
  // 宽度有两个副本，各有分工：
  //   - state  → 给 aria-valuenow 与持久化用，变化频率低；
  //   - ref    → 给拖动期间的即时值，变化频率等于指针事件频率。
  // 拖动时只写 ref + DOM，一次 React 渲染都不做：面板里有列表和逐条记录，
  // 按 pointermove 频率重渲染正是「拖起来卡」的根因。
  const [width, setWidth] = React.useState(readStoredWidth);
  const widthRef = React.useRef(width);
  const dragRef = React.useRef<{ startX: number; startW: number } | null>(null);

  // state 是唯一真相，统一落到 DOM。
  // 刻意不用 style={{ width }}：那样父组件任何一次重渲染都会把拖动中的
  // 实时宽度覆盖回旧 state，指针一动就被拽回去。
  React.useLayoutEffect(() => {
    widthRef.current = width;
    if (asideRef.current) asideRef.current.style.width = `${width}px`;
  }, [width]);

  // 拖动左边缘改宽。上限同时受「给主列留够 MAIN_MIN」约束，窗口窄时坞先让。
  const onDragStart = React.useCallback((event: React.PointerEvent<HTMLDivElement>) => {
    dragRef.current = { startX: event.clientX, startW: widthRef.current };
    (event.target as HTMLElement).setPointerCapture(event.pointerId);
  }, []);
  const onDragMove = React.useCallback((event: React.PointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag) return;
    const room = Math.max(DOCK_MIN, window.innerWidth - MAIN_MIN);
    const next = Math.min(Math.max(drag.startW + (drag.startX - event.clientX), DOCK_MIN), Math.min(DOCK_MAX, room));
    widthRef.current = next;
    if (asideRef.current) asideRef.current.style.width = `${next}px`;
  }, []);
  const onDragEnd = React.useCallback(() => {
    if (!dragRef.current) return;
    dragRef.current = null;
    // 拖动结束才提交 state：同步 aria-valuenow，并落盘一次。
    const final = widthRef.current;
    setWidth(final);
    try {
      window.localStorage.setItem(DOCK_WIDTH_KEY, String(final));
    } catch {
      /* ignore */
    }
  }, []);

  const title = t(PANEL_LABEL_KEY[panel]);

  return (
    <aside
      ref={asideRef}
      className="relative flex min-h-0 shrink-0 flex-col border-border/70 border-l bg-card"
      aria-label={t("asideAria")}
    >
      {/* 左边缘拖拽条：3px 命中区，hover 显现主色。 */}
      {/* biome-ignore lint/a11y/useSemanticElements: 拖拽手柄用 div 才不干扰键盘 tab 序 */}
      <div
        role="separator"
        tabIndex={0}
        aria-orientation="vertical"
        aria-valuenow={width}
        aria-valuemin={DOCK_MIN}
        aria-valuemax={DOCK_MAX}
        aria-label={t("resize")}
        onPointerDown={onDragStart}
        onPointerMove={onDragMove}
        onPointerUp={onDragEnd}
        onPointerCancel={onDragEnd}
        onKeyDown={(event) => {
          // 键盘等价操作：← 变窄 / → 变宽，每次 24px。
          if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
          event.preventDefault();
          const delta = event.key === "ArrowLeft" ? 24 : -24;
          setWidth(Math.min(Math.max(widthRef.current + delta, DOCK_MIN), DOCK_MAX));
        }}
        className="absolute inset-y-0 -left-1 z-10 w-2 cursor-col-resize hover:bg-primary/20 focus-visible:bg-primary/30 focus-visible:outline-none"
      />
      <div className="flex shrink-0 items-center gap-1.5 border-b px-2 py-1.5">
        <span className="min-w-0 flex-1 truncate font-medium text-xs">{title}</span>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              size="icon-sm"
              variant="ghost"
              className="text-muted-foreground"
              onClick={onClose}
              aria-label={t("close")}
            >
              <XIcon className="size-3.5" />
            </Button>
          </TooltipTrigger>
          <TooltipContent side="bottom">{t("close")}</TooltipContent>
        </Tooltip>
      </div>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
        {panel === "tasks" && <TasksDock context={context} />}
        {panel === "approvals" && <ApprovalsDock />}
        {panel === "checkpoints" && <CheckpointsDock conversationId={context.conversationId} />}
        {panel === "files" && <FilesDock />}
      </div>
      {/* 面板切换条：竖版单列时也要能换到别的面板，不用先收起再点。 */}
      <div className="flex shrink-0 gap-0.5 overflow-x-auto border-t px-1.5 py-1">
        {PANEL_IDS.filter((id) => id !== panel).map((id) => (
          <button
            key={id}
            type="button"
            onClick={() => onPanelChange(id)}
            className="shrink-0 rounded px-1.5 py-0.5 text-muted-foreground text-xs hover:bg-accent/60 hover:text-foreground"
          >
            {t(PANEL_LABEL_KEY[id])}
          </button>
        ))}
      </div>
    </aside>
  );
});

// ── 任务面板：任务 → Worker 两级，最多两级。数据源与第五轮的任务带同源。 ──
function TasksDock({ context }: { context: DockContext }) {
  const t = useTranslations("chat.harness.dock");
  const [rows, setRows] = React.useState<{ tasks: DockRow[]; loading: boolean }>({ tasks: [], loading: true });

  React.useEffect(() => {
    let alive = true;
    const pull = async () => {
      try {
        const [tasksResp, intentsResp] = await Promise.all([api.tasks(), api.intentsPage("", 0, 300)]);
        if (!alive) return;
        setRows({ tasks: buildDockRows(tasksResp.tasks, intentsResp.items), loading: false });
      } catch {
        if (alive) setRows((prev) => ({ ...prev, loading: false }));
      }
    };
    void pull();
    const timer = window.setInterval(() => void pull(), 5000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, []);

  if (rows.loading) return <DockLoading />;
  if (rows.tasks.length === 0) return <DockEmpty text={t("empty.tasks")} />;

  return (
    <div className="min-h-0 flex-1 overflow-auto p-1.5">
      <ul className="flex flex-col gap-1">
        {rows.tasks.map((row) => (
          <li key={row.id} className="rounded-lg border border-border/60 bg-background/60">
            <div className="flex items-center gap-1.5 px-2 py-1.5">
              <span className={cn("size-1.5 shrink-0 rounded-full", levelDot(row.level))} />
              <span className="min-w-0 flex-1 truncate font-medium text-xs">
                <span className="font-mono text-muted-foreground">#{row.id}</span> {row.name}
              </span>
              {context.onOpenTask && (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <button
                      type="button"
                      className="shrink-0 text-muted-foreground hover:text-foreground"
                      onClick={() => context.onOpenTask?.(row.id)}
                      aria-label={t("openTask")}
                    >
                      <ExternalLinkIcon className="size-3.5" />
                    </button>
                  </TooltipTrigger>
                  <TooltipContent side="left">{t("openTask")}</TooltipContent>
                </Tooltip>
              )}
            </div>
            {row.workers.length > 0 && (
              <ul className="border-border/50 border-t px-2 py-1">
                {row.workers.slice(0, 12).map((worker) => (
                  <li key={worker.intentId} className="flex items-center gap-1.5 py-0.5 text-xs">
                    <span
                      className={cn(
                        "shrink-0 rounded border px-1 tabular-nums",
                        worker.state === "running"
                          ? "border-sky-500/50 bg-sky-500/10 text-sky-700 dark:text-sky-300"
                          : "border-border bg-muted text-muted-foreground",
                      )}
                    >
                      {worker.state}
                    </span>
                    <span className="shrink-0 font-mono text-muted-foreground">#{worker.intentId}</span>
                    <span className="min-w-0 truncate text-muted-foreground/80">{worker.summary || "—"}</span>
                  </li>
                ))}
                {row.workers.length > 12 && (
                  <li className="py-0.5 text-muted-foreground text-xs">
                    {t("moreWorkers", { count: row.workers.length - 12 })}
                  </li>
                )}
              </ul>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

type DockRow = {
  id: string;
  name: string;
  level: "running" | "waiting" | "error" | "idle";
  workers: { intentId: string; state: string; summary: string }[];
};

function levelDot(level: DockRow["level"]): string {
  if (level === "error") return "bg-red-500";
  if (level === "waiting") return "bg-amber-500";
  if (level === "running") return "bg-sky-500";
  return "bg-muted-foreground/40";
}

function buildDockRows(tasks: import("@/lib/types").Task[], intents: import("@/lib/types").TaskNode[]): DockRow[] {
  const byId = new Map<string, DockRow>();
  for (const task of tasks) {
    if (task.status === "done") continue;
    let level: DockRow["level"] = "idle";
    if (task.status === "failed" || task.status === "timeout") level = "error";
    else if ((task.pending_approvals ?? 0) > 0) level = "waiting";
    else if (task.status === "running" || task.status === "queued") level = "running";
    byId.set(task.id, {
      id: task.id,
      name: (task.name ?? "").trim() || (task.description ?? "").trim() || `#${task.id}`,
      level,
      workers: [],
    });
  }
  for (const node of intents) {
    if (node.type !== "intent" || !node.source_task_id) continue;
    const row = byId.get(node.source_task_id);
    if (!row) continue;
    row.workers.push({
      intentId: node.id,
      state: node.state,
      summary: (node.payload ?? "").replace(/\s+/g, " ").trim().slice(0, 120),
    });
  }
  return [...byId.values()].sort((a, b) => {
    const w = (r: DockRow) => (r.level === "waiting" ? 0 : r.level === "error" ? 1 : r.level === "running" ? 2 : 3);
    return w(a) - w(b) || Number(a.id) - Number(b.id);
  });
}

// ── 审批面板：只读清单。决策仍然去审批页做（第五轮定的口径：审批卡片只跳转）。 ──
function ApprovalsDock() {
  const t = useTranslations("chat.harness.dock");
  const [items, setItems] = React.useState<InterceptPending[] | null>(null);

  React.useEffect(() => {
    let alive = true;
    api
      .interceptPending()
      .then((rows) => {
        if (alive) setItems(rows);
      })
      .catch(() => {
        if (alive) setItems([]);
      });
    return () => {
      alive = false;
    };
  }, []);

  if (items === null) return <DockLoading />;
  if (items.length === 0) return <DockEmpty text={t("empty.approvals")} />;
  return (
    <div className="min-h-0 flex-1 overflow-auto p-1.5">
      <ul className="flex flex-col gap-1">
        {items.slice(0, 50).map((item) => (
          <li key={item.id} className="rounded-lg border border-border/60 bg-background/60 px-2 py-1.5">
            <div className="flex items-center gap-1.5">
              <span className="font-mono text-muted-foreground text-xs">#{item.id}</span>
              <span className="min-w-0 flex-1 truncate text-xs">{item.tool_name || "—"}</span>
              <span className="shrink-0 text-muted-foreground text-xs">{item.agent_name}</span>
            </div>
            {item.reason && <div className="mt-0.5 line-clamp-2 text-muted-foreground text-xs">{item.reason}</div>}
            {item.task_id && <div className="mt-0.5 text-muted-foreground text-xs">任务 #{item.task_id}</div>}
          </li>
        ))}
      </ul>
      <div className="p-1.5">
        <a
          href="/system/intercept/approvals"
          className="inline-flex items-center gap-1 text-primary text-xs hover:underline"
        >
          {t("decideInApprovals")} <ChevronRightIcon className="size-3" />
        </a>
      </div>
    </div>
  );
}

// ── 检查点面板：读当前会话的检查点，回滚入口仍由消息气泡提供（避免两处写入口）。 ──
function CheckpointsDock({ conversationId }: { conversationId: number }) {
  const t = useTranslations("chat.harness.dock");
  const [items, setItems] = React.useState<Checkpoint[] | null>(null);

  React.useEffect(() => {
    let alive = true;
    api
      .conversationCheckpoints(conversationId)
      .then((rows) => {
        if (alive) setItems(rows);
      })
      .catch(() => {
        if (alive) setItems([]);
      });
    return () => {
      alive = false;
    };
  }, [conversationId]);

  if (items === null) return <DockLoading />;
  if (items.length === 0) return <DockEmpty text={t("empty.checkpoints")} />;
  return (
    <ul className="min-h-0 flex-1 space-y-1 overflow-auto p-1.5">
      {items.map((cp) => (
        <li key={cp.id} className="rounded-lg border border-border/60 bg-background/60 px-2 py-1.5 text-xs">
          <div className="flex items-center gap-1.5">
            <span className="shrink-0 rounded border bg-muted/60 px-1 text-muted-foreground">{cp.reason}</span>
            <span className="min-w-0 flex-1 truncate font-medium">{cp.summary || t("checkpoint", { id: cp.id })}</span>
          </div>
          <div className="mt-0.5 text-muted-foreground tabular-nums">{cp.created_at}</div>
        </li>
      ))}
    </ul>
  );
}

// ── 文件面板：本会话活动里出现过的附件。没有独立「工作区文件树」的后端端点，
//    所以这里只列真实存在的附件，不臆造目录树。 ──
function FilesDock() {
  const t = useTranslations("chat.harness.dock");
  return (
    <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-2 p-4 text-center">
      <PaperclipIcon className="size-6 text-muted-foreground/50" />
      <p className="text-muted-foreground text-xs leading-relaxed">{t("filesHint")}</p>
    </div>
  );
}

function DockLoading() {
  return (
    <div className="flex flex-1 items-center justify-center p-4">
      <Loader2Icon className="size-4 animate-spin text-muted-foreground" />
    </div>
  );
}

function DockEmpty({ text }: { text: string }) {
  return (
    <div className="flex flex-1 items-center justify-center p-4 text-center text-muted-foreground text-xs leading-relaxed">
      {text}
    </div>
  );
}
