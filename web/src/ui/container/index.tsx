"use client";

import type * as React from "react";

import {
  ApiBar as ApiBarV1,
  ComposeBar,
  KpiBand as KpiBandV1,
  KpiRow as KpiRowV1,
  Masthead as MastheadV1,
  Panel as PanelV1,
  PanelGrid,
  SectionHead as SectionHeadV1,
  SeverityBar,
} from "@/components/editorial";
import {
  GroupHead as GroupHeadV1,
  RecordRow as RecordRowV1,
  RecordSub,
  Sheet as SheetV1,
  SheetFoot as SheetFootV1,
  SheetHead as SheetHeadV1,
} from "@/components/editorial-sheet";
import { useUIVersion } from "@/ui/version";

import {
  V2ApiBar,
  V2GroupHead,
  V2KpiBand,
  V2KpiRow,
  V2Masthead,
  V2Panel,
  V2RecordRow,
  V2SectionHead,
  V2Sheet,
  V2SheetFoot,
  V2SheetHead,
} from "@/ui/v2/container";

/**
 * 容器分发层 —— 页面统一从这里取容器，同一个名字在 v1/v2 下是不同的实现。
 *
 * 之所以不直接改 `components/editorial.tsx`：那套原语刻意保持无 hook、可在
 * server component 里使用（见其顶部注释）。分发需要读 ui 版本（客户端偏好），
 * 因此单独建一层客户端组件来做分发，原语本身保持纯净。
 *
 * 签名与 editorial / editorial-sheet **逐一对齐**，所以页面只需换 import 路径，
 * JSX 一行都不用改 —— 64 个文件的切换因此是机械且安全的。
 */

export type { PanelAnnotationKind, SeverityBarItem } from "@/components/editorial";

export function Masthead(props: React.ComponentProps<typeof MastheadV1>) {
  return useUIVersion() === "v2" ? <V2Masthead {...props} /> : <MastheadV1 {...props} />;
}

export function SectionHead(props: React.ComponentProps<typeof SectionHeadV1>) {
  return useUIVersion() === "v2" ? <V2SectionHead {...props} /> : <SectionHeadV1 {...props} />;
}

export function KpiBand(props: React.ComponentProps<typeof KpiBandV1>) {
  return useUIVersion() === "v2" ? <V2KpiBand {...props} /> : <KpiBandV1 {...props} />;
}

export function KpiRow(props: React.ComponentProps<typeof KpiRowV1>) {
  return useUIVersion() === "v2" ? <V2KpiRow {...props} /> : <KpiRowV1 {...props} />;
}

export function ApiBar(props: React.ComponentProps<typeof ApiBarV1>) {
  return useUIVersion() === "v2" ? <V2ApiBar {...props} /> : <ApiBarV1 {...props} />;
}

export function Panel(props: React.ComponentProps<typeof PanelV1>) {
  return useUIVersion() === "v2" ? <V2Panel {...props} /> : <PanelV1 {...props} />;
}

export function Sheet(props: React.ComponentProps<typeof SheetV1>) {
  return useUIVersion() === "v2" ? <V2Sheet {...props} /> : <SheetV1 {...props} />;
}

export function SheetHead(props: React.ComponentProps<typeof SheetHeadV1>) {
  return useUIVersion() === "v2" ? <V2SheetHead {...props} /> : <SheetHeadV1 {...props} />;
}

export function SheetFoot(props: React.ComponentProps<typeof SheetFootV1>) {
  return useUIVersion() === "v2" ? <V2SheetFoot {...props} /> : <SheetFootV1 {...props} />;
}

export function GroupHead(props: React.ComponentProps<typeof GroupHeadV1>) {
  return useUIVersion() === "v2" ? <V2GroupHead {...props} /> : <GroupHeadV1 {...props} />;
}

export function RecordRow(props: React.ComponentProps<typeof RecordRowV1>) {
  return useUIVersion() === "v2" ? <V2RecordRow {...props} /> : <RecordRowV1 {...props} />;
}

// 非容器类原语两版共用，直接透传。
export { PanelGrid, SeverityBar, ComposeBar, RecordSub };
