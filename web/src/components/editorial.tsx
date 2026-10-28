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
