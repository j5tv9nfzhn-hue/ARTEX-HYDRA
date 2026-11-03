"use client";

import * as React from "react";

import { ListTodo } from "lucide-react";
import { useTranslations } from "next-intl";

import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";

// TodoPopover 是会话此刻的 TodoWrite 状态——**气泡形态**，不是右侧面板。
//
// 为什么回到气泡（第十一轮）：右侧坞里再放一个 TODO 面板，等于把「输入框旁边
// 随手看一眼」的轻量信息搬到需要先开坞、再选面板的两步操作里；坞还会挤占正文宽度。
// 旧版这个 Popover 就挂在 composer 的操作行上，点开即看、点走即走，宽度不侵占正文。
//
// 数据来源没变：完整 todo 列表不在活动列表里（detail 是懒加载的），所以打开时按
// seq 拉最近一次 TodoWrite 的 detail，再解析它的 {todos:[…]} JSON。纯客户端。
//
// 打开期间按 5s 轮询一次：一轮 run 里 TodoWrite 会被反复调用，旧版只在打开时拉一次，
// 用户盯着气泡看进度时列表是不动的。
export function TodoPopover({
  seq,
  fetchDetail,
}: {
  seq: number | null;
  fetchDetail: (seq: number) => Promise<string>;
}) {
  const tr = useTranslations("todoPopover");
  const [open, setOpen] = React.useState(false);
  const [todos, setTodos] = React.useState<{ content: string; status: string }[] | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [err, setErr] = React.useState("");

  const load = React.useCallback(async () => {
    if (seq == null) return;
    setLoading(true);
    setErr("");
    try {
      const detail = await fetchDetail(seq);
      const start = detail.indexOf("{"); // detail 可能带 "TodoWrite " 前缀
      const parsed = JSON.parse(start >= 0 ? detail.slice(start) : detail);
      setTodos(Array.isArray(parsed?.todos) ? parsed.todos : []);
    } catch {
      setErr(tr("parseError"));
      setTodos(null);
    } finally {
      setLoading(false);
    }
  }, [seq, fetchDetail, tr]);

  // 每次打开拉一次；停留期间每 5s 跟进一次。
  React.useEffect(() => {
    if (!open) return;
    void load();
    const id = window.setInterval(() => void load(), 5000);
    return () => window.clearInterval(id);
  }, [open, load]);

  const disabled = seq == null;
  const done = (todos ?? []).filter((t) => t.status === "completed").length;
  const total = todos?.length ?? 0;

  // 进度只在有列表时出现：0/0 比不显示更吵。
  const progress = total > 0 ? `${done}/${total}` : null;
  return (
    <Popover open={open} onOpenChange={disabled ? undefined : setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          disabled={disabled}
          title={disabled ? tr("none") : tr("view")}
          aria-label={disabled ? tr("none") : tr("view")}
          className={cn(
            "inline-flex h-7 shrink-0 items-center gap-1.5 rounded-lg border border-border/70 bg-muted/50 px-2 text-muted-foreground",
            "text-xs transition-colors hover:bg-accent hover:text-foreground",
            "disabled:pointer-events-none disabled:opacity-40",
            open && "bg-accent text-foreground",
          )}
        >
          <ListTodo className="size-3.5" />
          <span className="max-sm:hidden">{tr("trigger")}</span>
          {progress ? <span className="font-mono text-ui-2xs tabular-nums opacity-70">{progress}</span> : null}
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" side="top" sideOffset={8} className="w-80 max-w-[calc(100vw-2rem)] p-0">
        <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
          <span className="font-medium text-xs">{tr("recent")}</span>
          <span className="font-mono text-muted-foreground text-ui-2xs tabular-nums">
            {loading && !todos ? tr("loadingSuffix") : (progress ?? "")}
          </span>
        </div>
        <div className="max-h-72 overflow-auto p-1.5">
          {err ? <p className="px-1.5 py-1 text-destructive text-xs">{err}</p> : null}
          {todos && todos.length === 0 && !loading ? (
            <p className="px-1.5 py-1 text-muted-foreground text-xs">{tr("empty")}</p>
          ) : null}
          <ul className="space-y-0.5">
            {(todos ?? []).map((t) => (
              <li
                key={`${t.status}:${t.content}`}
                className={cn(
                  "flex items-start gap-2 rounded-md px-1.5 py-1 text-xs",
                  t.status === "completed" && "text-muted-foreground",
                )}
              >
                {/* 状态用形状 + 颜色双编码：只靠颜色在深色主题下几乎看不出区别。 */}
                <span
                  aria-hidden
                  className={cn(
                    "mt-[3px] size-1.5 shrink-0 rounded-full",
                    t.status === "completed" && "bg-muted-foreground/40",
                    t.status === "in_progress" && "bg-primary",
                    t.status !== "completed" && t.status !== "in_progress" && "border border-muted-foreground/50",
                  )}
                />
                <span className={cn("min-w-0 break-words", t.status === "completed" && "line-through")}>
                  {t.content}
                </span>
              </li>
            ))}
          </ul>
        </div>
      </PopoverContent>
    </Popover>
  );
}
