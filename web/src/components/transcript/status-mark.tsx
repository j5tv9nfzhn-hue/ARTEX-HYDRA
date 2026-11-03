"use client";

import { Check, Minus, X } from "lucide-react";

import type { ToolStatus } from "@/lib/tool-status";
import { cn } from "@/lib/utils";

// StatusMark 是工具行行首的**终态标记**，替代原来的旋转圆环（StatusRing）。
//
// 为什么去掉圆环（第十二轮，对齐 codex 官方观感）：
//   1. 圆环在运行中会转，一格一行、十几行同时在转，整条流看起来「满屏小圈」；
//   2. 圆环是「进度指示器」的语汇，但工具调用并没有可信进度——弧长最终总停在
//      整环/半环，只是拿形状假装有进度；
//   3. codex 原版行首没有图形，状态写在行尾的文字里。这里保留一个**最小的**
//      终态记号，是因为中文里「已执行 / 已失败 / 已拒绝」三个词长度差很多，
//      只靠文字会让每行右侧参差不齐；对号/错号/横杠给了固定的视觉落点。
//
// 交互约定：
//   - 运行中 → 不画任何记号（行尾文字在进行中已经说明一切，避免再多一个转圈）；
//   - 成功 → 对号（绿）；失败 → 错号（红）；拒绝 → 横杠（琥珀）。被用户/策略
//     主动拦下的调用不是「错」，用错号会被误读成系统异常。
export function StatusMark({ status, className }: { status: ToolStatus; className?: string }) {
  if (status === "running") return null;
  // 表驱动，避免嵌套三元（biome noNestedTernary）。
  const ICONS: Record<ToolStatus, typeof Check> = {
    running: Check,
    completed: Check,
    failed: X,
    denied: Minus,
    stopped: Minus,
  };
  const Icon = ICONS[status];
  return (
    <Icon
      aria-hidden="true"
      className={cn(
        "size-3.5 shrink-0",
        status === "completed" && "text-emerald-600 dark:text-emerald-400",
        status === "failed" && "text-red-600 dark:text-red-400",
        (status === "denied" || status === "stopped") && "text-amber-600 dark:text-amber-400",
        className,
      )}
      strokeWidth={2.25}
    />
  );
}
