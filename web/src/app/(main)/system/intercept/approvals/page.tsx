"use client";

import * as React from "react";

import { useSearchParams } from "next/navigation";

import { ApprovalRecords } from "@/components/approval-records";

// 审批页现在同时是「任务详情 → 待审批」的落点：带 ?task=<id> 就只看该任务的审批。
// 单独进来（侧边栏「审批」）时不带参数，展示全部待办。
function ApprovalsInner() {
  const searchParams = useSearchParams();
  const taskId = searchParams.get("task") ?? undefined;
  return (
    <div className="flex min-w-0 flex-1 flex-col p-4 sm:p-6">
      <ApprovalRecords taskId={taskId} />
    </div>
  );
}

export default function ApprovalsPage() {
  // useSearchParams 必须待在 Suspense 边界内，否则静态导出会报错。
  return (
    <React.Suspense fallback={null}>
      <ApprovalsInner />
    </React.Suspense>
  );
}
