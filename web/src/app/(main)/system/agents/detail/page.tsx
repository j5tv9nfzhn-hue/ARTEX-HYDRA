"use client";

import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { AgentEditor } from "@/components/agent-editor";
import { Masthead } from "@/components/editorial";
import { Sheet } from "@/components/editorial-sheet";
import { Button } from "@/components/ui/button";

/**
 * Agent 详情：人设 / 工具 / 模型三段的全宽编辑器。
 *
 * 地址：/system/agents/detail?key=<agent key>
 *
 * 主流程仍然是 `/system/agents` 里的抽屉（B 类），这一页把同一个 AgentEditor
 * 铺成全宽，供外链与书签直开。书眉沿用二级页的统一形状：编号一行 + 标题 + 副标题。
 */
function AgentDetailInner() {
  const t = useTranslations("agentDetailPage");
  const searchParams = useSearchParams();
  const key = searchParams.get("key") ?? "";

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <Masthead
          title={t("title")}
          meta={
            <>
              <code className="font-mono text-[10px]">{key || "—"}</code>
              <span className="mx-1.5 opacity-40">·</span>
              <span>{t("metaEditor")}</span>
            </>
          }
          subtitle={t("subtitle")}
          titleClassName="text-xl"
        />
        <Button asChild size="sm" variant="outline">
          <Link href="/system/agents">
            <ArrowLeftIcon data-icon="inline-start" />
            {t("back")}
          </Link>
        </Button>
      </div>

      <Sheet className="min-h-0 flex-1">
        <div data-editorial="body" className="flex min-h-0 flex-1 flex-col">
          <AgentEditor agentKey={key} />
        </div>
      </Sheet>
    </div>
  );
}

// useSearchParams must sit under a Suspense boundary for static export.
export default function AgentDetailPage() {
  return (
    <React.Suspense fallback={null}>
      <AgentDetailInner />
    </React.Suspense>
  );
}
