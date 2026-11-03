"use client";

import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

// 正文排版口径对齐 codex 的 `src/styles/markdown-typography.css`：
//
//   · 正文 14px / line-height 1.6（--md-leading: 1.6），段间距 14px，
//     最后一段不留尾巴（`p:last-child { margin-bottom: 0 }`）；
//   · 列表不靠 margin 堆，改成 flex column + gap 6px，左缩进 22px，
//     标记色收到 45% 透明度——列表项多的时候不会读成一堵墙；
//   · 行内 code 是「chip」：字号 0.92em、行高 1.4、上下 padding 1px、左右 5px，
//     这套数字是算过的——chip 的行盒（18px）小于正文行盒（22.4px），
//     所以它**不会把行撑开**，段落不会因为一个 token 而错位；
//   · 表格只留 10% 的淡边框 + 淡表头，不做整块色带；
//   · 围栏代码块单独一套（10/12 padding、圆角、可横向滚动），永远不当 chip 排。
//
// 之前这里是 `space-y-1.5`（段间距 6px）+ 全局 `my-1`，去掉外层卡片之后
// 那套紧凑间距就暴露出来了：7 条编号列表挤成一片，看着像「文本没排过版」。
const components: Components = {
  h1: ({ node, ...p }) => <div className="mt-4 mb-2 font-semibold text-base" {...p} />,
  h2: ({ node, ...p }) => <div className="mt-3.5 mb-1.5 font-semibold text-sm" {...p} />,
  h3: ({ node, ...p }) => <div className="mt-3 mb-1.5 font-medium text-foreground/90 text-sm" {...p} />,
  h4: ({ node, ...p }) => <div className="mt-2.5 mb-1 font-medium text-foreground/90 text-sm" {...p} />,
  p: ({ node, ...p }) => <p className="mb-3.5 leading-[1.6] last:mb-0" {...p} />,
  ul: ({ node, ...p }) => <ul className="mb-3.5 flex list-disc flex-col gap-1.5 pl-[22px] last:mb-0" {...p} />,
  ol: ({ node, ...p }) => <ol className="mb-3.5 flex list-decimal flex-col gap-1.5 pl-[22px] last:mb-0" {...p} />,
  li: ({ node, ...p }) => <li className="pl-0.5 leading-[1.6] [&::marker]:text-foreground/45" {...p} />,
  strong: ({ node, ...p }) => <strong className="font-semibold text-foreground" {...p} />,
  a: ({ node, ...p }) => (
    <a className="text-primary underline underline-offset-2" target="_blank" rel="noreferrer" {...p} />
  ),
  hr: () => <hr className="my-4 border-border" />,
  blockquote: ({ node, ...p }) => (
    <blockquote className="mb-3.5 border-border border-l-2 pl-3 text-muted-foreground" {...p} />
  ),
  pre: ({ node, ...p }) => (
    <pre className="mb-3.5 overflow-x-auto rounded-lg bg-muted/60 p-3 text-[13px] leading-relaxed last:mb-0" {...p} />
  ),
  code: ({ node, className, children, ...rest }) => {
    const block = /language-/.test(className || "");
    // 围栏块内的 code 交给 <pre> 排版，不再叠自己的底色/内边距。
    return block ? (
      <code className={`font-mono ${className || ""}`} {...rest}>
        {children}
      </code>
    ) : (
      <code
        className="inline-flex max-w-full items-center break-words rounded-[4px] bg-foreground/[0.07] px-[5px] py-px align-baseline font-mono text-[0.92em] leading-[1.4]"
        {...rest}
      >
        {children}
      </code>
    );
  },
  table: ({ node, ...p }) => (
    <div className="mb-3.5 overflow-x-auto last:mb-0">
      <table className="w-full border-collapse text-[13px]" {...p} />
    </div>
  ),
  th: ({ node, ...p }) => (
    <th className="border border-border/60 bg-foreground/[0.04] px-2.5 py-1.5 text-left font-medium" {...p} />
  ),
  td: ({ node, ...p }) => <td className="border border-border/60 px-2.5 py-1.5 align-baseline" {...p} />,
};

export function Markdown({ text }: { text: string }) {
  // 外层不再用 space-y：间距由每个元素自己的 margin 决定（codex 同一做法），
  // 否则 `space-y-*` 与 `mb-*` 会叠加出一套没人算过的间距。
  return (
    <div className="min-w-0 text-sm leading-[1.6] [&>*:first-child]:mt-0">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>
        {text}
      </ReactMarkdown>
    </div>
  );
}
