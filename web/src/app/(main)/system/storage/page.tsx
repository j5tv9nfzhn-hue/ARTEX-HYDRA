"use client";

import { useTranslations } from "next-intl";

import { StoragePanel } from "@/components/storage-panel";
import { Badge } from "@/components/ui/badge";
import { Masthead } from "@/ui/container";

// 系统 → 存储与清理（兼容入口）。
//
// 内容已并入工作空间页的「存储与清理」标签；这条路由保留，是为了让书签、
// 旧文档链接和习惯性输入 URL 仍然可用，不再在侧边栏单独占一项。
export default function StoragePage() {
  const t = useTranslations("storagePage");
  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <Masthead
          title={t("title")}
          subtitle={t("subtitle")}
          meta={<Badge variant="secondary">{t("badgeManual")}</Badge>}
          titleClassName="text-xl"
        />
      </div>
      <StoragePanel />
    </div>
  );
}
