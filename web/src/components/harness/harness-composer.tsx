"use client";

import * as React from "react";

import { BotIcon, CornerDownLeftIcon, Loader2Icon, ShieldAlertIcon, SlashIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { MentionTextarea } from "@/components/mention-textarea";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { api } from "@/lib/api";
import { shouldSubmitOnKey, useChatSendMode } from "@/lib/chat-send-mode";
import type { Task, TaskNode } from "@/lib/types";

// ─────────────────────────────────────────────────────────────────────────────
// 收件人（recipient）：composer 只保留一个写入口，但「这句话说给谁」可以是
//   ① 主 Agent —— 走 /api/conversations/{id}/messages，回车排队；
//   ② 某个运行中的 Worker —— 走 /api/tasks/{task}/intents/{intent}/steer，
//      文本进引擎 steer 队列，下一步工具调用前注入，不暂停、不重跑。
// 两者语义由 ready 上的 hint 讲清楚，不做隐式切换。
// ─────────────────────────────────────────────────────────────────────────────
export type HarnessRecipient = { kind: "agent" } | { kind: "worker"; taskId: string; intentId: string; title: string };

export function recipientKey(r: HarnessRecipient): string {
  return r.kind === "agent" ? "agent" : `worker:${r.taskId}:${r.intentId}`;
}

// useWorkerRecipients 拉一次运行中的 Worker（意图 state=running）作为可选收件人。
// 数据来自 /exploration/intents?page=1，不需要后端改动；页面每 8s 刷新一次。
export function useWorkerRecipients(): { workers: HarnessRecipient[]; loading: boolean } {
  const [workers, setWorkers] = React.useState<HarnessRecipient[]>([]);
  const [loading, setLoading] = React.useState(true);

  React.useEffect(() => {
    let alive = true;
    const pull = async () => {
      try {
        const [tasksResp, intentsResp] = await Promise.all([api.tasks(), api.intentsPage("", 0, 300)]);
        if (!alive) return;
        const byId = new Map<string, Task>(tasksResp.tasks.map((t) => [t.id, t]));
        const rows: HarnessRecipient[] = [];
        for (const node of intentsResp.items as TaskNode[]) {
          if (node.state !== "running" || !node.source_task_id) continue;
          const task = byId.get(node.source_task_id);
          if (!task) continue;
          rows.push({
            kind: "worker",
            taskId: task.id,
            intentId: node.id,
            title: `#${task.id} ${task.name || task.description}`,
          });
        }
        setWorkers(rows.slice(0, 40));
      } catch {
        // 运行中 Worker 只是增强项：读不到就只剩主 Agent，不影响发送。
      } finally {
        if (alive) setLoading(false);
      }
    };
    void pull();
    const timer = window.setInterval(() => void pull(), 8000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, []);

  return { workers, loading };
}

// RecipientPicker 是 composer 左侧的那颗胶囊：默认「主 Agent ▾」，点开列出运行中的 Worker。
export function RecipientPicker({
  value,
  workers,
  onChange,
  disabled,
}: {
  value: HarnessRecipient;
  workers: HarnessRecipient[];
  onChange: (next: HarnessRecipient) => void;
  disabled?: boolean;
}) {
  const t = useTranslations("chat.harness");
  const current = value.kind === "agent" ? t("recipient.agent") : `#${value.intentId}`;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          size="sm"
          variant="ghost"
          disabled={disabled}
          className="h-7 gap-1.5 rounded-lg border border-border/70 bg-muted/50 px-2.5 text-xs"
        >
          <BotIcon className="size-3.5" />
          <span className="max-w-40 truncate">{current}</span>
          <span className="text-muted-foreground">▾</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-72">
        <DropdownMenuLabel>{t("recipient.title")}</DropdownMenuLabel>
        <DropdownMenuGroup>
          <DropdownMenuItem onSelect={() => onChange({ kind: "agent" })}>
            <BotIcon />
            {t("recipient.agent")}
            {value.kind === "agent" && <span className="ml-auto text-primary">✓</span>}
          </DropdownMenuItem>
        </DropdownMenuGroup>
        <DropdownMenuSeparator />
        <DropdownMenuLabel className="text-muted-foreground text-xs">
          {workers.length > 0 ? t("recipient.workers", { count: workers.length }) : t("recipient.noWorkers")}
        </DropdownMenuLabel>
        <DropdownMenuGroup>
          {workers.map((w) => {
            if (w.kind !== "worker") return null;
            const active = value.kind === "worker" && value.intentId === w.intentId && value.taskId === w.taskId;
            return (
              <DropdownMenuItem key={recipientKey(w)} onSelect={() => onChange(w)}>
                <span className="truncate">
                  #{w.intentId} · {w.title}
                </span>
                {active && <span className="ml-auto text-primary">✓</span>}
              </DropdownMenuItem>
            );
          })}
        </DropdownMenuGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Slash 命令：harness 的第二个写入口（composer 内的 /）。
// 只列「后端真的存在」的命令；compact/fork 目前没有对应接口，菜单里如实标注，
// 不做假按钮——点了不会发生任何事的命令比没有更糟。
type SlashCommand = {
  key: string;
  label: string;
  hint: string;
  available: boolean;
  run?: () => void;
};

// ─────────────────────────────────────────────────────────────────────────────
// HarnessComposer：整页唯一的写入口。
//   Enter        = 交给收件人（主 Agent 排队 / Worker 立刻 steer）
//   Ctrl/Cmd + ↵ = 立刻发送（运行时表示「插队 / 立即 steer」）
//   /            = 命令菜单（/compact 只在主 Agent 收件人下可用，见下）
// 语义差异写在底部右侧的 hint 里，不靠用户猜。
// ─────────────────────────────────────────────────────────────────────────────
export function HarnessComposer({
  running,
  disabled,
  recipient,
  workers,
  onRecipientChange,
  onSend,
  onStop,
  stopping,
  placeholder,
  initialText,
  topSlot,
  todoSlot,
  children,
}: {
  running: boolean;
  disabled: boolean;
  recipient: HarnessRecipient;
  workers: HarnessRecipient[];
  onRecipientChange: (next: HarnessRecipient) => void;
  /** 主 Agent：把文本交给会话；返回 false 表示失败（调用方负责回填）。 */
  onSend: (text: string) => Promise<boolean>;
  onStop: () => void;
  stopping: boolean;
  placeholder: string;
  /** 挂载时要回填的初稿（草稿页转交、回滚回填都用这个）。 */
  initialText?: string;
  /** 输入框上方的附件 chip 之类。 */
  topSlot?: React.ReactNode;
  /** 操作行里、收件人选择器之前的那一格（TODO 气泡就走这里）。 */
  todoSlot?: React.ReactNode;
  /** 操作行左侧的附加控件（模型选择等）。 */
  children?: React.ReactNode;
}) {
  const t = useTranslations("chat.harness");
  const sendMode = useChatSendMode();
  const [value, setValue] = React.useState(initialText ?? "");
  const [sending, setSending] = React.useState(false);
  const [steering, setSteering] = React.useState(false);

  const commands: SlashCommand[] = React.useMemo(
    () => [
      {
        key: "/stop",
        label: t("slash.stop"),
        hint: t("slash.stopHint"),
        available: running,
        run: onStop,
      },
      {
        key: "/btw",
        label: t("slash.btw"),
        hint: t("slash.btwHint"),
        available: true,
      },
      {
        key: "/compact",
        label: t("slash.compact"),
        hint: t("slash.unavailable"),
        available: false,
      },
      {
        key: "/fork",
        label: t("slash.fork"),
        hint: t("slash.unavailable"),
        available: false,
      },
    ],
    [onStop, running, t],
  );
  // 只有「整个输入就是一截没空格的 /xxx」时才弹菜单，避免和正常句子里打斜杠打架。
  const slashQuery = /^\/[a-z]*$/i.test(value) ? value.toLowerCase() : null;
  const slashMatches = slashQuery ? commands.filter((c) => c.key.startsWith(slashQuery)) : [];
  const showSlash = slashMatches.length > 0;

  function applyCommand(cmd: SlashCommand) {
    if (!cmd.available) return;
    setValue("");
    cmd.run?.();
  }

  const toWorker = recipient.kind === "worker";

  async function submit() {
    const text = value.trim();
    if (!text || sending || steering) return;
    if (toWorker) {
      setSteering(true);
      try {
        await api.steerIntent(recipient.taskId, recipient.intentId, text);
        setValue("");
      } catch (e) {
        // 失败保留原文，避免用户丢掉输入。
        void e;
      } finally {
        setSteering(false);
      }
      return;
    }
    setSending(true);
    const ok = await onSend(text);
    if (ok) setValue("");
    setSending(false);
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (showSlash && (e.key === "Tab" || (e.key === "Enter" && !e.shiftKey && !e.ctrlKey && !e.metaKey))) {
      const first = slashMatches.find((c) => c.available);
      if (first) {
        e.preventDefault();
        applyCommand(first);
        return;
      }
    }
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      void submit();
      return;
    }
    if (!shouldSubmitOnKey(e, sendMode)) return;
    e.preventDefault();
    void submit();
  }

  const busy = sending || steering;
  let hintLabel = t("hint.send");
  if (toWorker) hintLabel = t("hint.steer");
  else if (running) hintLabel = t("hint.queue");
  return (
    <div className="px-3 pt-1.5 pb-3 md:px-6">
      <div className="mx-auto flex w-full max-w-[800px] flex-col rounded-2xl border border-border/85 bg-background/75 p-2.5 shadow-xs transition-all focus-within:border-primary/50 focus-within:ring-2 focus-within:ring-primary/20">
        {toWorker && (
          <div className="mb-2 flex items-center gap-1.5 rounded-lg border border-amber-500/40 bg-amber-500/10 px-2.5 py-1.5 text-amber-700 text-xs dark:text-amber-300">
            <ShieldAlertIcon className="size-3.5 shrink-0" />
            <span className="min-w-0">
              {t("steerNotice", { id: recipient.intentId })}
              <button type="button" className="ml-2 underline" onClick={() => onRecipientChange({ kind: "agent" })}>
                {t("backToAgent")}
              </button>
            </span>
          </div>
        )}
        {showSlash && (
          <div className="mb-1.5 overflow-hidden rounded-lg border bg-popover shadow-xs">
            {slashMatches.map((cmd) => (
              <button
                key={cmd.key}
                type="button"
                disabled={!cmd.available}
                onClick={() => applyCommand(cmd)}
                className="flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs transition-colors hover:bg-accent/60 disabled:cursor-not-allowed disabled:opacity-50"
              >
                <SlashIcon className="size-3.5 shrink-0 text-muted-foreground" />
                <code className="font-mono">{cmd.key}</code>
                <span className="min-w-0 flex-1 truncate text-muted-foreground">{cmd.label}</span>
                <span className="shrink-0 text-muted-foreground text-xs">{cmd.hint}</span>
              </button>
            ))}
          </div>
        )}
        {/* 输入框上方的一行：待办胶囊（左）+ 附件 chip（topSlot，自带换行）。 */}
        {(todoSlot ?? topSlot) && (
          <div className="mb-1.5 flex min-w-0 flex-wrap items-center gap-1.5 px-0.5">
            {todoSlot}
            {topSlot}
          </div>
        )}
        <div className="flex min-w-0 flex-1">
          <MentionTextarea
            className="max-h-40 min-h-11 w-full min-w-0 flex-1 resize-none border-0 bg-transparent px-1.5 py-1 text-sm shadow-none focus-visible:ring-0 dark:bg-transparent"
            rows={1}
            placeholder={placeholder}
            value={value}
            onValueChange={setValue}
            onKeyDown={onKeyDown}
          />
        </div>
        <div className="mt-1.5 flex flex-wrap items-center justify-between gap-2 border-border/50 border-t pt-2">
          <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
            <RecipientPicker
              value={recipient}
              workers={workers}
              onChange={onRecipientChange}
              disabled={disabled || busy}
            />
            {running && !toWorker && (
              <Badge variant="outline" className="shrink-0 px-1.5 py-0 text-xs">
                {t("runtimeBadge.queued")}
              </Badge>
            )}
            {children}
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            <span className="hidden text-muted-foreground text-xs sm:inline">{hintLabel}</span>
            <Button
              size="icon-sm"
              className="rounded-xl"
              onClick={() => void submit()}
              disabled={disabled || busy || !value.trim()}
              title={toWorker ? t("steerNow") : t("send")}
              aria-label={toWorker ? t("steerNow") : t("send")}
            >
              {busy ? <Loader2Icon className="size-4 animate-spin" /> : <CornerDownLeftIcon className="size-4" />}
            </Button>
            {running && (
              <Button
                size="icon-sm"
                variant="destructive"
                className="rounded-xl"
                onClick={onStop}
                disabled={stopping}
                title={t("stop")}
                aria-label={t("stop")}
              >
                {stopping ? (
                  <Loader2Icon className="size-3.5 animate-spin" />
                ) : (
                  <span className="size-3 rounded-[2px] bg-current" />
                )}
              </Button>
            )}
          </div>
        </div>
      </div>
      <p className="mx-auto mt-1.5 w-full max-w-[800px] text-center text-muted-foreground/70 text-xs">
        {t("footerHint")}
      </p>
    </div>
  );
}
