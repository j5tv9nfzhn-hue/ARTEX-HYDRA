"use client";

import type * as React from "react";

import { cn } from "@/lib/utils";

/**
 * 新版骨架（v2）的页面级容器 —— 与 v1 的 editorial 原语**同名同签名**，
 * 但结构不同：v1 那套是「卡片 + 描边 + 卡底」，皮肤层再把它压平成纸本；
 * v2 直接就长成纸本，不再依赖 skin-editorial.css 的换肤翻译。
 *
 * 与 v1 的结构性差异（不是换样式，是换 DOM 组织）：
 *   1. 容器没有边框与圆角，改由**上沿一条发丝线**分隔（ newspaper rule ）；
 *   2. 章节号 01/02 是布局的一部分（等宽、固定槽位），不再由 CSS 决定是否显形；
 *   3. 记录行用显式栅格（--rec-cols），不靠 flex 挤压；
 *   4. 接口锚点条固定落在容器底部，等宽字，作为"可追溯"的一等公民；
 *   5. 全部锚点走 data-ui-* ，与 v1 的 data-editorial 互不干扰。
 */

export type V2PanelAnnotationKind = "add" | "keep" | "trim" | "diff" | "note";

/** 报头：v1 是「h1 + 副标题」，v2 加入编号槽与元信息行。 */
export function V2Masthead({
  title,
  subtitle,
  meta,
  titleClassName = "text-xl",
  subtitleClassName = "text-sm",
}: {
  title: React.ReactNode;
  subtitle?: React.ReactNode;
  meta?: React.ReactNode;
  titleClassName?: string;
  subtitleClassName?: string;
}) {
  return (
    <header data-ui-masthead className="flex flex-col gap-1 border-b pb-3">
      {meta ? <div data-ui-masthead-meta className="text-muted-foreground text-xs">{meta}</div> : null}
      <h1 className={cn("font-semibold tracking-tight", titleClassName)}>{title}</h1>
      {subtitle ? (
        <p className={cn("text-muted-foreground", subtitleClassName)}>{subtitle}</p>
      ) : null}
    </header>
  );
}

/** 章节标题。v2 下编号常显，不依赖皮肤层。 */
export function V2SectionHead({
  index,
  icon: Icon,
  children,
  sub,
}: {
  index?: string;
  icon?: React.ElementType;
  children: React.ReactNode;
  sub?: React.ReactNode;
}) {
  return (
    <div data-ui-sechead className="mb-3 flex items-center justify-between">
      <div className="flex items-center gap-2 text-xs font-semibold">
        {index ? (
          <span data-ui-secnum className="font-mono text-[10px] text-muted-foreground">
            {index}
          </span>
        ) : null}
        {Icon ? <Icon className="size-3.5 text-muted-foreground" /> : null}
        {children}
      </div>
      {sub ? <span className="text-muted-foreground text-xs">{sub}</span> : null}
    </div>
  );
}

export function V2KpiBand({ className, children }: { className?: string; children: React.ReactNode }) {
  return (
    <div data-ui-kpi-band className={cn("flex flex-col gap-3", className)}>
      {children}
    </div>
  );
}

export function V2KpiRow({
  className,
  children,
  delay = 0,
}: {
  className?: string;
  children: React.ReactNode;
  delay?: number;
}) {
  return (
    <div
      data-ui-kpi-row
      className={cn("animate-fade-up", className)}
      style={delay > 0 ? { animationDelay: `${delay}ms` } : undefined}
    >
      {children}
    </div>
  );
}

/** 接口锚点条：v2 把它做成常驻落款，等宽字，不再是可有可无的装饰。 */
export function V2ApiBar({
  methods,
  label = "后端接口",
  className,
}: {
  methods: string[];
  label?: React.ReactNode;
  className?: string;
}) {
  if (methods.length === 0) return null;
  return (
    <div data-ui-apibar className={cn("flex flex-wrap items-center gap-1.5 border-t px-3 py-1.5", className)}>
      <span className="text-[10px] text-muted-foreground">{label}</span>
      {methods.map((m) => (
        <code key={m} className="rounded bg-muted px-1 py-0.5 font-mono text-[10px]">
          {m}
        </code>
      ))}
    </div>
  );
}

/** 面板（对应 v1 的 Panel）。无边框，上沿发丝线。 */
export function V2Panel({
  index,
  title,
  icon: Icon,
  annotation,
  api,
  apiLabel,
  actions,
  tight,
  className,
  bodyClassName,
  children,
}: {
  index?: string;
  title?: React.ReactNode;
  icon?: React.ElementType;
  annotation?: { text: React.ReactNode; kind?: V2PanelAnnotationKind };
  api?: string[];
  apiLabel?: React.ReactNode;
  actions?: React.ReactNode;
  tight?: boolean;
  className?: string;
  bodyClassName?: string;
  children?: React.ReactNode;
}) {
  const hasHead = Boolean(index || title || annotation || actions);
  return (
    <section data-ui-panel className={cn("min-w-0", className)}>
      {hasHead ? (
        <div data-ui-panel-head className="flex items-center gap-2 border-b pb-2">
          {index ? <span data-ui-secnum className="font-mono text-[10px] text-muted-foreground">{index}</span> : null}
          {Icon ? <Icon className="size-3.5 text-muted-foreground" /> : null}
          {title ? <h2 className="font-semibold text-xs">{title}</h2> : null}
          {annotation ? <span data-ui-annotation data-kind={annotation.kind ?? "note"}>{annotation.text}</span> : null}
          {actions ? <div className="ml-auto flex items-center gap-1.5">{actions}</div> : null}
        </div>
      ) : null}
      <div data-ui-panel-body className={cn(tight ? "pt-2" : "py-3", bodyClassName)}>
        {children}
      </div>
      {api && api.length > 0 ? <V2ApiBar methods={api} label={apiLabel} /> : null}
    </section>
  );
}

/** 版块（对应 v1 的 Sheet）。 */
export function V2Sheet({
  className,
  children,
  api,
  apiLabel,
  ...rest
}: React.ComponentProps<"section"> & {
  children: React.ReactNode;
  api?: string[];
  apiLabel?: React.ReactNode;
}) {
  return (
    <section data-ui-sheet className={cn("flex min-w-0 flex-col", className)} {...rest}>
      {children}
      {api && api.length > 0 ? <V2ApiBar methods={api} label={apiLabel} /> : null}
    </section>
  );
}

export function V2SheetHead({
  index,
  title,
  count,
  hint,
  actions,
  className,
  icon: Icon,
  iconClassName,
}: {
  index?: string;
  title: React.ReactNode;
  count?: React.ReactNode;
  hint?: React.ReactNode;
  actions?: React.ReactNode;
  className?: string;
  icon?: React.ElementType;
  iconClassName?: string;
}) {
  return (
    <div data-ui-sheet-head className={cn("flex shrink-0 items-center gap-2 border-b pb-2", className)}>
      {index ? <span data-ui-secnum className="font-mono text-[10px] text-muted-foreground">{index}</span> : null}
      {Icon ? <Icon className={cn("size-3.5 shrink-0 text-muted-foreground", iconClassName)} /> : null}
      <h2 className="shrink-0 font-semibold text-sm">{title}</h2>
      {count !== undefined && count !== null ? (
        <span className="shrink-0 font-semibold text-muted-foreground text-xs tabular-nums">{count}</span>
      ) : null}
      <span className="mx-1 hidden h-px min-w-4 flex-1 bg-border sm:block" />
      {hint ? <span className="hidden shrink-0 text-muted-foreground text-xs md:block">{hint}</span> : null}
      {actions ? <div className="flex shrink-0 items-center gap-1.5">{actions}</div> : null}
    </div>
  );
}

export function V2GroupHead({
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
    <div data-ui-group-head data-tone={tone} className={cn("flex items-center gap-2 pt-3 pb-1", className)}>
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

/** 记录行：显式栅格（--rec-cols），v1 靠 flex 挤压。 */
export function V2RecordRow({
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
  const Comp = onClick ? "button" : "div";
  return (
    <Comp
      data-ui-record
      data-expanded={expanded ? "" : undefined}
      onClick={onClick}
      className={cn(
        "grid w-full items-center gap-2 border-b py-1.5 text-left text-xs last:border-0",
        className,
      )}
      style={cols ? ({ "--rec-cols": cols } as React.CSSProperties) : undefined}
    >
      {lead ? <span className="text-muted-foreground">{lead}</span> : null}
      <span className="min-w-0 truncate">{children}</span>
      {values?.map((value, i) => (
        <span key={i} className="text-right tabular-nums">
          {value}
        </span>
      ))}
    </Comp>
  );
}

export function V2SheetFoot({ left, right }: { left: React.ReactNode; right?: React.ReactNode }) {
  return (
    <div data-ui-sheet-foot className="mt-1 flex flex-wrap items-center justify-between gap-2 border-t pt-2 text-[10.5px] text-muted-foreground">
      <span className="tabular-nums">{left}</span>
      {right ? <span>{right}</span> : null}
    </div>
  );
}
