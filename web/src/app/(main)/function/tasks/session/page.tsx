"use client";

import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon, MessagesSquareIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { Masthead } from "@/components/editorial";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { api } from "@/lib/api";
import type { Task } from "@/lib/types";

import { SessionsTab } from "../detail/_tabs/sessions-tab";

/**
 * 子代理运行页：一个任务的会话全貌。
 *
 * 版式与任务册里的「会话」页签是同一个组件 —— 左边是会话清单
 * （主控 / 规划器 / 系统 / 各 Worker），中间是转录，右边是上下文与待办。
 * 这个路由只是把它从页签里搬出来，给一个能深链、能刷新、能回退的地址：
 *
 *   /function/tasks/session?id=<任务号>[&session=<main|plan|intent:xxx>]
 *
 * 会话本身由 SessionsTab 内部选择，刷新后回到主控会话 —— 会话状态不落 URL，
 * 因为它每 5 秒轮询一次、主要是「看」，不是「分享某一屏」。
 */
function TaskSessionInner() {
  const t = useTranslations("taskSessionsPage");
  const td = useTranslations("taskDetail");
  const searchParams = useSearchParams();
  const id = searchParams.get("id") ?? "";
  const [task, setTask] = React.useState<Task | null>(null);
  const [loaded, setLoaded] = React.useState(false);

  React.useEffect(() => {
    if (!id) {
      setLoaded(true);
      return;
    }
    let alive = true;
    api
      .task(id)
      .then((resp) => {
        if (alive) setTask(resp);
      })
      .catch(() => {
        if (alive) setTask(null);
      })
      .finally(() => {
        if (alive) setLoaded(true);
      });
    return () => {
      alive = false;
    };
  }, [id]);

  if (!id) {
    return (
      <div className="flex flex-1 items-center justify-center p-10 text-muted-foreground text-sm">
        {td("header.notFound", { id: "" })}
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <Masthead
          title={t("title")}
          subtitle={t("subtitle")}
          meta={
            <>
              <Link href={`/function/tasks/detail?id=${encodeURIComponent(id)}`} className="hover:underline">
                {task?.name || task?.description || `#${id}`}
              </Link>
              <code className="font-mono text-xs">#{id}</code>
            </>
          }
          titleClassName="text-xl"
        />
        <Button asChild size="sm" variant="outline">
          <Link href={`/function/tasks/detail?id=${encodeURIComponent(id)}`}>
            <ArrowLeftIcon data-icon="inline-start" />
            {td("header.backToList")}
          </Link>
        </Button>
      </div>

      <div className="flex min-h-0 flex-1 flex-col border-border/70 border-t pt-3">
        {!loaded ? (
          <div className="flex flex-1 items-center justify-center gap-2 p-10 text-muted-foreground text-sm">
            <Spinner />
            {td("header.loading")}
          </div>
        ) : task ? (
          <SessionsTab taskId={id} />
        ) : (
          <div className="flex flex-1 flex-col items-center justify-center gap-3 p-10 text-center">
            <MessagesSquareIcon className="size-6 text-muted-foreground/50" />
            <p className="text-muted-foreground text-sm">{td("header.notFound", { id })}</p>
          </div>
        )}
      </div>
    </div>
  );
}

// useSearchParams 必须待在 Suspense 里，静态导出才不会被判成动态页面。
export default function TaskSessionPage() {
  return (
    <React.Suspense fallback={null}>
      <TaskSessionInner />
    </React.Suspense>
  );
}
