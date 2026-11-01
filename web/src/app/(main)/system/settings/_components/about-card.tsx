"use client";

import * as React from "react";

import { useTranslations } from "next-intl";
import Link from "next/link";

import { BotIcon, LayersIcon, ShieldCheckIcon, SparklesIcon, TargetIcon } from "lucide-react";

import { Sheet, SheetHead } from "@/components/editorial-sheet";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { APP_CONFIG } from "@/config/app-config";
import { api } from "@/lib/api";

/**
 * 项目简介与架构概览。
 *
 * 纯静态内容，不参与更新检测——以前这张卡片叫 update-card，实际却只显示版本号，
 * 更新功能一上线就容易被误读成"这里应该有更新按钮"，所以拆开：版本与更新见
 * version 卡片（同目录 update-card.tsx），这里是产品说明。
 */
export function AboutCard() {
  const t = useTranslations("settingsPage");
  const [version, setVersion] = React.useState(APP_CONFIG.version);

  React.useEffect(() => {
    let alive = true;
    api
      .health()
      .then((h) => {
        const v = (h.version ?? "").replace(/^v(?=\d)/, "").trim();
        if (alive && v) setVersion(v);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, []);

  return (
    // 设置页是多列瀑布流布局，卡片自己负责行间距并禁止跨列断开（见 page.tsx 的注释）。
    <Sheet api={["health"]} className="mb-4 break-inside-avoid md:mb-6">
      <SheetHead icon={LayersIcon} title={t("about.title")} />
      <div className="flex flex-col gap-4 px-4 py-3">
        <p className="text-muted-foreground text-sm">
          {t.rich("about.intro", { b: (chunks) => <b>{chunks}</b> })}
        </p>
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <Badge variant="secondary" className="font-mono">
            {APP_CONFIG.name}
          </Badge>
          <Badge variant="outline" className="font-mono">
            v{version}
          </Badge>
          <Badge variant="outline">Planner / Worker / MainAgent</Badge>
          <Badge variant="outline" className="font-mono">
            AGPL-3.0
          </Badge>
        </div>

        <div className="grid grid-cols-1 gap-2 text-muted-foreground text-xs sm:grid-cols-2">
          <div className="rounded-md border bg-muted/30 p-2.5">
            <div className="mb-0.5 font-medium text-foreground">{t("about.cap1Title")}</div>
            <div>{t("about.cap1Desc")}</div>
          </div>
          <div className="rounded-md border bg-muted/30 p-2.5">
            <div className="mb-0.5 font-medium text-foreground">{t("about.cap2Title")}</div>
            <div>{t("about.cap2Desc")}</div>
          </div>
          <div className="rounded-md border bg-muted/30 p-2.5">
            <div className="mb-0.5 font-medium text-foreground">{t("about.cap3Title")}</div>
            <div>{t("about.cap3Desc")}</div>
          </div>
          <div className="rounded-md border bg-muted/30 p-2.5">
            <div className="mb-0.5 font-medium text-foreground">{t("about.cap4Title")}</div>
            <div>{t("about.cap4Desc")}</div>
          </div>
        </div>

        <div className="flex flex-wrap gap-2">
          <Button asChild variant="outline" size="sm">
            <Link href="/function/tasks">
              <TargetIcon className="size-4" />
              {t("about.linkTasks")}
            </Link>
          </Button>
          <Button asChild variant="outline" size="sm">
            <Link href="/system/agents">
              <BotIcon className="size-4" />
              {t("about.linkAgents")}
            </Link>
          </Button>
          <Button asChild variant="outline" size="sm">
            <Link href="/system/skills">
              <SparklesIcon className="size-4" />
              {t("about.linkSkills")}
            </Link>
          </Button>
          <Button asChild variant="ghost" size="sm">
            <Link href="/system/intercept">
              <ShieldCheckIcon className="size-4" />
              {t("about.linkIntercept")}
            </Link>
          </Button>
        </div>
      </div>
    </Sheet>
  );
}
