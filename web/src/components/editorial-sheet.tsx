import type * as React from "react";

import { cn } from "@/lib/utils";

// ── 版块原语（纸本页面语言）────────────────────────────────────────────────
//
// 这一组只负责「结构 → 排版」的锚点，和 editorial.tsx 的分工一样：
//
//   · 默认外观是**现在的卡片**（圆角 + 描边 + 卡底），所以老主题一个像素都不变；
//   · 纸本外观（data-skin="editorial"）由 styles/skin-editorial.css 把卡片压平成
//     「上沿一条发丝线 + 标题行 + 记录行 + 落款」，不需要页面自己写样式。
//
// 一次只做一个取值的硬约束：
//   结构锚点写成 data-editorial="sheet|sheet-head|sheet-foot|group-head|record"，
//   CSS 只认这些锚点；页面里不允许再出现「为了好看」的临时 div。

/**
 * 一个版块。纸本下等于「一条上沿细线 + 内容」，老主题下就是原来的卡片。
 *
 * 注意：这里刻意用 data-slot="card" —— 皮肤层对卡片的收口规则只认这一个锚点，
 * 换名字就得在 CSS 里重复一遍，没必要。
 */
export function Sheet({
  className,
  children,
  ...rest
}: React.ComponentProps<"section"> & { children: React.ReactNode }) {
  return (
    <section
      data-slot="card"
      data-editorial="sheet"
      className={cn("flex min-w-0 flex-col overflow-hidden rounded-lg border bg-card", className)}
      {...rest}
    >
      {children}
    </section>
  );
}

/**
 * 版块标题行：标题 + 计数 + 一条拉到右端的细线 + 右端可选动作。
 *
 * 不右对齐标题、不加刻度横杠 —— 只在标题与动作之间拉一条发丝线，
 * 让「这一卷有多少条」有个落脚点。
 */
export function SheetHead({
  title,
  count,
  hint,
  actions,
  className,
  icon: Icon,
  iconClassName,
}: {
  title: React.ReactNode;
  count?: React.ReactNode;
  hint?: React.ReactNode;
  actions?: React.ReactNode;
  className?: string;
  icon?: React.ElementType;
  iconClassName?: string;
}) {
  return (
    <div
      data-editorial="sheet-head"
      className={cn("flex shrink-0 items-center gap-2 border-b px-4 py-2", className)}
    >
      {Icon ? <Icon className={cn("size-3.5 shrink-0 text-muted-foreground", iconClassName)} /> : null}
      <h2 className="shrink-0 font-semibold text-sm">{title}</h2>
      {count !== undefined && count !== null ? (
        <span className="shrink-0 font-semibold text-muted-foreground text-xs tabular-nums">{count}</span>
      ) : null}
      <span data-editorial="rule" className="mx-1 hidden h-px min-w-4 flex-1 bg-border sm:block" />
      {hint ? <span className="hidden shrink-0 text-muted-foreground text-xs md:block">{hint}</span> : null}
      {actions ? <div className="flex shrink-0 items-center gap-1.5">{actions}</div> : null}
    </div>
  );
}

/** 分卷的头：圆点 + 卷名 + 计数 + 细线 + 右端提示。一条记录列表里可以有多卷。 */
export function GroupHead({
  tone = "muted",
  title,
  count,
  hint,
  className,
}: {
  tone?: "sky" | "amber" | "red" | "green" | "muted";
  title: React.ReactNode;
  count?: React.ReactNode;
  hint?: React.ReactNode;
  className?: string;
}) {
  const dot =
    tone === "sky"
      ? "bg-sky-500"
      : tone === "amber"
        ? "bg-amber-500"
        : tone === "red"
          ? "bg-rose-500"
          : tone === "green"
            ? "bg-emerald-500"
            : "bg-muted-foreground/60";
  return (
    <div data-editorial="group-head" data-tone={tone} className={cn("flex items-center gap-2 pt-3 pb-1", className)}>
      <span className={cn("size-1.5 shrink-0 rounded-full", dot)} />
      <span className="shrink-0 font-medium text-xs">{title}</span>
      {count !== undefined && count !== null ? (
        <span className="shrink-0 text-muted-foreground text-xs tabular-nums">{count}</span>
      ) : null}
      <span className="h-px min-w-3 flex-1 bg-border/70" />
      {hint ? <span className="hidden shrink-0 text-muted-foreground text-xs sm:block">{hint}</span> : null}
    </div>
  );
}

/**
 * 记录行：左窄标签 + 正文 + 右侧两列数。
 *
 * `--rec-cols` 一行一个宽度，和调试版前端的 .rec 是同一套约定；
 * 没给就按「标签 64px / 正文 / 两列 auto」排。
 */
export function RecordRow({
  lead,
  children,
  values,
  cols,
  className,
  onClick,
  expanded,
}: {
  lead?: React.ReactNode;
  children: React.ReactNode;
  values?: React.ReactNode[];
  cols?: string;
  className?: string;
  onClick?: () => void;
  expanded?: boolean;
}) {
  const style = cols ? ({ ["--rec-cols" as string]: cols } as React.CSSProperties) : undefined;
  const Tag = onClick ? "button" : "div";
  return (
    <Tag
      data-editorial="record"
      data-expanded={expanded ? "true" : undefined}
      type={onClick ? "button" : undefined}
      onClick={onClick}
      style={style}
      className={cn(
        "grid w-full items-baseline gap-3 border-b px-4 py-2.5 text-left last:border-b-0",
        "[grid-template-columns:var(--rec-cols,64px_minmax(0,1fr)_auto_auto)]",
        onClick && "transition-colors hover:bg-accent/40",
        className,
      )}
    >
      {lead !== undefined ? <span className="text-xs text-muted-foreground tabular-nums">{lead}</span> : <span />}
      <div className="min-w-0">{children}</div>
      {(values ?? []).map((v, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: 右侧数位固定，位置即身份
        <span key={i} className="shrink-0 text-right text-xs tabular-nums">
          {v}
        </span>
      ))}
    </Tag>
  );
}

/** 记录行里的第二行：来源 · 时间 · 证据数这类。中间用 2px 圆点隔开。 */
export function RecordSub({ items }: { items: React.ReactNode[] }) {
  const nodes = items.filter((x) => x !== null && x !== undefined && x !== "");
  return (
    <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground">
      {nodes.map((node, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: 以位置定身份，内容可能重复
        <span key={i} className="inline-flex items-center gap-2">
          {i > 0 ? <span className="size-0.5 rounded-full bg-muted-foreground/50" /> : null}
          {node}
        </span>
      ))}
    </div>
  );
}

/**
 * 页脚落款：一行小字，左端是「共多少条」，右端是一句提醒。
 *
 * 纸本里每一页都要有个落款 —— 它不是分页器，是这一卷的收尾。
 */
export function SheetFoot({ left, right }: { left: React.ReactNode; right?: React.ReactNode }) {
  return (
    <div
      data-editorial="sheet-foot"
      className="mt-1 flex flex-wrap items-center justify-between gap-2 border-t px-4 py-2 text-[10.5px] text-muted-foreground"
    >
      <span className="tabular-nums">{left}</span>
      {right ? <span>{right}</span> : null}
    </div>
  );
}
