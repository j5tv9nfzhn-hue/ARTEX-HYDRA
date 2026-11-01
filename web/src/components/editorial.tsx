import type * as React from "react";

import { cn } from "@/lib/utils";

// ── 编辑排版原语（方案 A · 纸张 · 编年）──────────────────────────────────────
//
// 硬约束：这些组件的**默认外观必须与改造前逐像素一致**。
//
// 报纸形态（报头双线、衬线大标题、章节号 01/02、数据栏竖线、发丝线）
// 全部由 web/src/styles/editorial.css 里 `[data-theme-preset="paper"]` 的规则开启，
// 组件本身只负责输出稳定的结构锚点（data-editorial）。因此切到其余任何预设时，
// 页面回落到改造前的观感 —— 现有主题零影响。
//
// 刻意不引入 useTheme / 不读 dataset：渲染结果与主题无关，避免水合不一致，
// 也让这套原语在 server component 里可用。

/**
 * 报头。非纸本主题下等价于「h1 + 副标题」的普通页头。
 *
 * titleClassName / subtitleClassName 用来承接各页原本的字号，避免为了统一页头
 * 而把其他主题的标题悄悄改小 —— 纸本主题下这两者都会被 clamp 后的衬线字号覆盖。
 */
export function Masthead({
  title,
  subtitle,
  meta,
  titleClassName = "text-lg",
  subtitleClassName = "text-xs",
}: {
  title: React.ReactNode;
  subtitle?: React.ReactNode;
  meta?: React.ReactNode;
  titleClassName?: string;
  subtitleClassName?: string;
}) {
  return (
    <header data-editorial="masthead">
      {meta ? <div data-editorial="masthead-meta">{meta}</div> : null}
      <h1 className={cn("font-semibold tracking-tight", titleClassName)}>{title}</h1>
      {subtitle ? <p className={cn("text-muted-foreground", subtitleClassName)}>{subtitle}</p> : null}
    </header>
  );
}

/**
 * 章节标题。`index` 是纸本主题下的章节号（01/02…），其余主题下不渲染可见内容。
 */
export function SectionHead({
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
    <div data-editorial="sechead" className="mb-3 flex items-center justify-between">
      <div className="flex items-center gap-1.5 text-xs font-semibold">
        {index ? <span data-editorial="secnum">{index}</span> : null}
        {Icon ? <Icon className="size-3.5 text-muted-foreground" /> : null}
        {children}
      </div>
      {sub ? <span className="text-xs text-muted-foreground">{sub}</span> : null}
    </div>
  );
}

/** 指标带外层。纸本主题下把「主 KPI + 次级矩阵」压平成报纸的数据栏。 */
export function KpiBand({ className, children }: { className?: string; children: React.ReactNode }) {
  return (
    <div data-editorial="kpi-band" className={className}>
      {children}
    </div>
  );
}

/**
 * 次级指标行。等价于 FadeIn（同样的 .animate-fade-up + 内联 animation-delay），
 * 只是多挂一个结构锚点，供纸本主题改成四栏竖线分栏。
 */
export function KpiRow({
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
      data-editorial="kpi-row"
      className={cn("animate-fade-up", className)}
      style={delay > 0 ? { animationDelay: `${delay}ms` } : undefined}
    >
      {children}
    </div>
  );
}

// ── 面板原语（03 设计）──────────────────────────────────────────────────────
//
// Panel 是 03 设计的统一容器：编号头（01 / 02…）+ 正文 + 后端接口锚点条。
//
// 与既有原语同一硬约束：默认外观【必须与改造前逐像素一致】。这里只输出结构
// 锚点（data-editorial）与中性 Tailwind；报纸形态——发丝线、衬线标题、章节号、
// 等宽接口条——全部交给 styles/skin-editorial.css 里的
// :root[data-skin="editorial"] 规则。data-skin="classic" 时本组件退化成一个
// 普通圆角卡片，老主题零影响。
//
// api 参数是「不违背后端、杜绝空匹配」的落点：它把该面板实际依赖的
// web/src/lib/api.ts 方法名显式渲染出来，让面板上每个控件都能追溯到真实接口。
// 传空数组或不传即不渲染锚点条（纯静态说明块可以这么做）。

export type PanelAnnotationKind = "add" | "keep" | "trim" | "diff" | "note";

export function ApiBar({
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
    <div data-editorial="apibar" className={className}>
      <span data-editorial="apibar-label">{label}</span>
      <span data-editorial="apibar-methods">
        {methods.map((m) => (
          <code key={m} data-editorial="apibar-method">
            {m}
          </code>
        ))}
      </span>
    </div>
  );
}

export function Panel({
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
  /** 纸本主题下渲染为章节号（01 / 02…），其余主题下不显形。 */
  index?: string;
  title?: React.ReactNode;
  icon?: React.ElementType;
  annotation?: { text: React.ReactNode; kind?: PanelAnnotationKind };
  /** 该面板依赖的 api.ts 方法名。留空则不渲染接口锚点条。 */
  api?: string[];
  apiLabel?: React.ReactNode;
  actions?: React.ReactNode;
  /** 正文去内边距，用于贴边表格。 */
  tight?: boolean;
  className?: string;
  bodyClassName?: string;
  children?: React.ReactNode;
}) {
  const hasHead = Boolean(index || title || annotation || actions);
  return (
    <section data-editorial="panel" className={cn("rounded-xl border bg-card", className)}>
      {hasHead ? (
        <div data-editorial="panel-head" className="flex items-center gap-2 border-b px-4 py-2.5">
          {index ? <span data-editorial="secnum">{index}</span> : null}
          {Icon ? <Icon className="size-3.5 text-muted-foreground" /> : null}
          {title ? (
            <h2 data-editorial="panel-title" className="text-xs font-semibold">
              {title}
            </h2>
          ) : null}
          {annotation ? (
            <span data-editorial="annotation" data-kind={annotation.kind ?? "note"}>
              {annotation.text}
            </span>
          ) : null}
          {actions ? <div className="ml-auto flex items-center gap-1.5">{actions}</div> : null}
        </div>
      ) : null}
      <div data-editorial="panel-body" className={cn(tight ? "p-0" : "p-4", bodyClassName)}>
        {children}
      </div>
      {api && api.length > 0 ? <ApiBar methods={api} label={apiLabel} /> : null}
    </section>
  );
}

/** 面板栅格。默认单列，纸本主题下由 CSS 改成报纸分栏。 */
export function PanelGrid({
  cols,
  className,
  children,
}: {
  cols?: 2 | 3;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <div data-editorial="panel-grid" data-cols={cols ?? 1} className={cn("grid gap-4", className)}>
      {children}
    </div>
  );
}

// ── 严重度分布条（对应 strix 的 IssueSeveritySummary）──────────────────────

export interface SeverityBarItem {
  key: string;
  label: string;
  count: number;
}

export function SeverityBar({
  items,
  total,
  active,
  onSelect,
  className,
}: {
  items: SeverityBarItem[];
  /** 不传则按各项 count 求和。 */
  total?: number;
  active?: string | null;
  onSelect?: (key: string) => void;
  className?: string;
}) {
  const sum = total ?? items.reduce((n, i) => n + i.count, 0);
  return (
    <div data-editorial="severity-bar" className={className}>
      {items.map((it) => {
        const pct = sum > 0 ? Math.round((it.count / sum) * 100) : 0;
        const inner = (
          <>
            <span data-editorial="severity-fill" style={{ width: `${pct}%` }} />
            <span data-editorial="severity-label">{it.label}</span>
            <span data-editorial="severity-count">{it.count}</span>
          </>
        );
        const shared = {
          "data-editorial": "severity-seg",
          "data-severity": it.key,
          "data-active": active === it.key ? "" : undefined,
          title: `${it.label} · ${it.count}`,
        };
        return onSelect ? (
          <button key={it.key} type="button" onClick={() => onSelect(it.key)} {...shared}>
            {inner}
          </button>
        ) : (
          <div key={it.key} {...shared}>
            {inner}
          </div>
        );
      })}
    </div>
  );
}

// ── 常驻指挥框（对应 strix 的 ScanPromptComposer）───────────────────────────
//
// strix 把指挥输入框放在 TAB 集合之外，让它随页面常驻：用户随时可以追加指令，
// 而不用先切到某个标签页。03 的任务页沿用这一点。

export function ComposeBar({
  value,
  onChange,
  onSubmit,
  placeholder,
  submitLabel = "发送",
  hint,
  busy,
  disabled,
  className,
}: {
  value: string;
  onChange: (v: string) => void;
  onSubmit: () => void;
  placeholder?: string;
  submitLabel?: React.ReactNode;
  hint?: React.ReactNode;
  busy?: boolean;
  disabled?: boolean;
  className?: string;
}) {
  const blocked = Boolean(busy || disabled);
  return (
    <div data-editorial="compose" className={className} data-busy={busy ? "" : undefined}>
      {hint ? <div data-editorial="compose-hint">{hint}</div> : null}
      <div data-editorial="compose-row">
        <textarea
          data-editorial="compose-input"
          value={value}
          rows={2}
          disabled={blocked}
          placeholder={placeholder}
          onChange={(e) => onChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              if (!blocked && value.trim() !== "") onSubmit();
            }
          }}
        />
        <button
          type="button"
          data-editorial="compose-submit"
          disabled={blocked || value.trim() === ""}
          onClick={onSubmit}
        >
          {busy ? "…" : submitLabel}
        </button>
      </div>
    </div>
  );
}
