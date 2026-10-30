"use client";

// 任务详情 / 时间线 tab（批次 6 / #32、#34）：检查点列表 + 回滚入口。
// 回滚要求任务已暂停；工具副作用与已落库的图产物不会回滚（对话框已明示）。

import { HistoryIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { CheckpointTimeline } from "@/components/checkpoint-timeline";
import { Sheet, SheetFoot, SheetHead } from "@/components/editorial-sheet";

export function TimelineTab({ taskId, onReverted }: { taskId: string; onReverted?: () => void }) {
  const t = useTranslations("taskDetail.timeline");
  return (
    <Sheet>
      <SheetHead icon={HistoryIcon} title={t("title")} hint={t("description")} />
      <div className="px-4 pt-1">
        <CheckpointTimeline scope="task" id={taskId} onReverted={onReverted} />
      </div>
      <SheetFoot left={t("foot")} right={t("footHint")} />
    </Sheet>
  );
}
