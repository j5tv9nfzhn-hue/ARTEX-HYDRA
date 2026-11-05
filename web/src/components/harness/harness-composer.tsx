"use client";

import * as React from "react";

import { ArrowUpIcon, Loader2Icon, SlashIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { MentionTextarea } from "@/components/mention-textarea";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { shouldSubmitOnKey, useChatSendMode } from "@/lib/chat-send-mode";

// ─────────────────────────────────────────────────────────────────────────────
// 单 Agent HARNESS。
//
// 旧版 composer 有一个「收件人」概念：主 Agent / 某个运行中的 Worker，后者走
// /api/tasks/{task}/intents/{intent}/steer 直接给子代理插话。新架构里子代理是
// 根 Agent 在任务里用工具临时派发的实例，不是用户可以直接对话的对象——所有输入
// 统一交给根 Agent，由它决定自己处理还是转给某个子代理。
//
// 因此这里不再有 recipient / worker 列表 / 收件人选择器，也不再有 steer 分支。
// ─────────────────────────────────────────────────────────────────────────────

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
//   Enter        = 交给主 Agent（运行中则排队）
//   Ctrl/Cmd + ↵ = 立刻发送（运行时表示插队）
//   /            = 命令菜单
// 语义差异写在底部右侧的 hint 里，不靠用户猜。
//
// 只有一个收件人：根 Agent。旧版的 Worker steer 入口已移除——子代理由根在
// 任务里派发，不是一个可以直接对话的对象。
// ─────────────────────────────────────────────────────────────────────────────
export function HarnessComposer({
  running,
  disabled,
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
  /** 主 Agent：把文本交给会话；返回 false 表示失败（调用方负责回填）。 */
  onSend: (text: string) => Promise<boolean>;
  onStop: () => void;
  stopping: boolean;
  placeholder: string;
  /** 挂载时要回填的初稿（草稿页转交、回滚回填都用这个）。 */
  initialText?: string;
  /** 输入框上方的附件 chip 之类。 */
  topSlot?: React.ReactNode;
  /** 操作行左侧的第一格（TODO 气泡就走这里）。 */
  todoSlot?: React.ReactNode;
  /** 操作行左侧的附加控件（模型选择等）。 */
  children?: React.ReactNode;
}) {
  const t = useTranslations("chat.harness");
  const sendMode = useChatSendMode();
  const [value, setValue] = React.useState(initialText ?? "");
  const [sending, setSending] = React.useState(false);

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

  async function submit() {
    const text = value.trim();
    // running 时后端会直接 409（一次只跑一轮），此前这里没有拦、按钮也还是可点的，
    // 于是「按回车没反应」被误读成按键失效。现在统一在入口挡住，并用 hint 说明原因。
    if (!text || sending || running) return;
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

  const busy = sending;
  const hintLabel = running ? t("hint.queue") : t("hint.send");
  return (
    <div className="px-3 pt-1.5 pb-3 md:px-6">
      <div className="mx-auto flex w-full max-w-[800px] flex-col rounded-2xl border border-border/85 bg-background/75 p-2.5 shadow-xs transition-all focus-within:border-primary/50 focus-within:ring-2 focus-within:ring-primary/20">
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
            {running && (
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
              disabled={disabled || busy || running || !value.trim()}
              title={t("send")}
              aria-label={t("send")}
            >
              {/* 图标用 ↑ 而不是 ⏎：后者看起来就是「回车键」，和键盘上的 Enter 重复，
                  容易被当成第二个发送入口。 */}
              {busy ? <Loader2Icon className="size-4 animate-spin" /> : <ArrowUpIcon className="size-4" />}
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
