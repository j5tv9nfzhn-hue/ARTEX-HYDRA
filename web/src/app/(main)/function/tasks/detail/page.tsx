"use client";

import * as React from "react";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";

import {
  ArchiveIcon,
  ArrowLeftIcon,
  BrainIcon,
  CheckIcon,
  CircleAlertIcon,
  CrosshairIcon,
  MessagesSquareIcon,
  PauseIcon,
  PlayIcon,
  ShieldAlertIcon,
  SparklesIcon,
} from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { KpiRow } from "@/components/editorial";
import { StatusBadge } from "@/components/status-badge";
import { TaskLLMProfileChain } from "@/components/task-llm-profile-chain";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverDescription,
  PopoverHeader,
  PopoverTitle,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Separator } from "@/components/ui/separator";
import { SidebarTrigger } from "@/components/ui/sidebar";
import { Spinner } from "@/components/ui/spinner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { usePolling } from "@/hooks/use-polling";
import { api } from "@/lib/api";
import type { LLMProfile, Task } from "@/lib/types";
import { cn } from "@/lib/utils";

import { AssetsTab } from "./_tabs/assets-tab";
import { BroadcastTab } from "./_tabs/broadcast-tab";
import { CoverageGraphTab } from "./_tabs/coverage-graph-tab";
import { FindingsTab } from "./_tabs/findings-tab";
import { GraphTab } from "./_tabs/graph-tab";
import { InterceptTab } from "./_tabs/intercept-tab";
import { OverviewTab } from "./_tabs/overview-tab";
import { ReportTab } from "./_tabs/report-tab";
import { RetestsTab } from "./_tabs/retests-tab";
import { SessionsTab } from "./_tabs/sessions-tab";
import { SoftwareRETab } from "./_tabs/software-re-tab";
import { TimelineTab } from "./_tabs/timeline-tab";

// 页签顺序沿用原来的分簇顺序，只是不再画簇与簇之间的竖线：
// 纸本主题里，页签是书页上的下划线索引，不是一排按钮。
const SOFTWARE_RE_TABS = ["software", "sessions", "overview", "graph", "report", "intercept", "timeline"] as const;

const WEB_TABS = [
  "sessions",
  "overview",
  "graph",
  "findings",
  "retests",
  "report",
  "assets",
  "coverage",
  "broadcast",
  "intercept",
  "timeline",
] as const;

function taskProfileIDs(task: Task): string[] {
  if (task.llm_profile_ids && task.llm_profile_ids.length > 0) {
    return task.llm_profile_ids.map(String);
  }
  return task.llm_profile_id ? [String(task.llm_profile_id)] : [];
}

function TaskLLMControl({ task, profiles, onUpdated }: { task: Task; profiles: LLMProfile[]; onUpdated: () => void }) {
  const t = useTranslations("taskDetail.llm");
  const [open, setOpen] = React.useState(false);
  const [profileIDs, setProfileIDs] = React.useState<string[]>(() => taskProfileIDs(task));
  const [activeProfileID, setActiveProfileID] = React.useState(
    task.active_llm_profile_id ? String(task.active_llm_profile_id) : (taskProfileIDs(task)[0] ?? ""),
  );
  const [saving, setSaving] = React.useState(false);
  const popoverContentRef = React.useRef<HTMLDivElement>(null);

  const chain = taskProfileIDs(task);
  const exhausted = task.llm_failover_state === "chain_exhausted";
  // 任何状态都可以改链,终态也不例外:任务结束后主 Agent 对话仍走这条链,
  // 链上模型出问题时必须能换掉,否则已完成任务就没法继续交互。
  const terminal = ["done", "failed", "timeout"].includes(task.status);
  // A null active profile on an exhausted, non-empty chain is a persisted end
  // cursor. Keep the status display honest; choosing the first profile is only
  // the editor's reset draft and does not mean it is currently active.
  let activeID = chain[0] ?? "";
  if (exhausted) activeID = "";
  if (task.active_llm_profile_id) activeID = String(task.active_llm_profile_id);
  const activeProfile = profiles.find((profile) => profile.id === activeID);
  const currentLabel = exhausted
    ? t("exhausted")
    : (activeProfile?.name ?? (activeID ? t("profileRef", { id: activeID }) : t("followDefault")));
  const activeIndex = chain.indexOf(activeID);
  const backupCount = activeIndex >= 0 ? Math.max(0, chain.length - activeIndex - 1) : 0;
  const currentTitle = [
    currentLabel,
    activeProfile?.model,
    backupCount > 0 ? t("backupCount", { count: backupCount }) : "",
  ]
    .filter(Boolean)
    .join(" / ");
  let editorDescription = t("editorDescription");
  if (terminal) editorDescription = t("editorDescriptionTerminal");
  let saveLabel = t("save");
  if (exhausted) saveLabel = t("saveReset");
  if (saving) saveLabel = t("saving");

  const syncDraft = React.useCallback(() => {
    const next = taskProfileIDs(task);
    setProfileIDs(next);
    setActiveProfileID(task.active_llm_profile_id ? String(task.active_llm_profile_id) : (next[0] ?? ""));
  }, [task]);

  const handleOpenChange = (next: boolean) => {
    setOpen(next);
    if (next) syncDraft();
  };

  const handleProfileIDsChange = (next: string[]) => {
    setProfileIDs(next);
    setActiveProfileID((current) => (next.includes(current) ? current : (next[0] ?? "")));
  };

  const save = async () => {
    setSaving(true);
    try {
      const result = await api.updateTaskLLMProfiles(
        task.id,
        profileIDs.map(Number),
        activeProfileID ? Number(activeProfileID) : undefined,
      );
      if (result.switch_event) {
        toast.info(result.switch_event.summary, { id: `task-${task.id}-llm-${result.switch_event.seq}` });
      } else {
        toast.success(
          result.reopened_intents > 0 ? t("updatedWithReopened", { count: result.reopened_intents }) : t("updated"),
        );
      }
      setOpen(false);
      onUpdated();
    } catch (error) {
      toast.error(t("updateFailed", { message: (error as Error).message }));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Popover open={open} onOpenChange={handleOpenChange}>
      <PopoverTrigger asChild>
        <Button
          size="sm"
          variant={exhausted ? "destructive" : "outline"}
          className="h-7 gap-1.5 rounded-lg px-2.5 text-xs"
          aria-label={t("controlAria")}
          title={currentTitle}
        >
          <BrainIcon className="size-3.5 text-primary" data-icon="inline-start" />
          <span className="hidden max-w-36 truncate lg:inline">{currentLabel}</span>
          {backupCount > 0 && <span className="hidden text-muted-foreground xl:inline">+{backupCount}</span>}
        </Button>
      </PopoverTrigger>
      <PopoverContent ref={popoverContentRef} align="start" className="w-[min(28rem,calc(100vw-2rem))] gap-4 p-4">
        <PopoverHeader>
          <PopoverTitle>{t("chainTitle")}</PopoverTitle>
          <PopoverDescription>{editorDescription}</PopoverDescription>
        </PopoverHeader>

        {exhausted && (
          <Alert variant="destructive">
            <CircleAlertIcon />
            <AlertTitle>{t("exhaustedTitle")}</AlertTitle>
            <AlertDescription>{task.llm_failover_reason ?? t("exhaustedDesc")}</AlertDescription>
          </Alert>
        )}

        <TaskLLMProfileChain
          profiles={profiles}
          value={profileIDs}
          onValueChange={handleProfileIDsChange}
          activeProfileId={activeProfileID}
          onActiveProfileChange={setActiveProfileID}
          inputId="task-llm-profiles"
          disabled={saving}
          portalContainer={popoverContentRef}
        />

        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" size="sm" onClick={() => setOpen(false)}>
            {t("close")}
          </Button>
          <Button type="button" size="sm" onClick={() => void save()} disabled={saving}>
            {saving && <Spinner data-icon="inline-start" />}
            {saveLabel}
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}

function TaskDetailInner() {
  const t = useTranslations("taskDetail");
  const router = useRouter();
  const searchParams = useSearchParams();
  const id = searchParams.get("id") ?? "";
  const [task, setTask] = React.useState<Task | null>(null);
  const [paused, setPaused] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [tab, setTab] = React.useState("sessions");
  // 未收口分支数（完成闸门）：坏终态且没被续派/收口的分支。进页面拉一次 + 15s 轮询，
  // 数字掉了说明分支已回炉跑完或被人工收口。
  const [openBranches, setOpenBranches] = React.useState(0);
  React.useEffect(() => {
    const id = task?.id;
    if (!id) return;
    let alive = true;
    const load = () => {
      api
        .taskBranches(id)
        .then((r) => {
          if (alive) setOpenBranches(r.count || 0);
        })
        .catch(() => {
          /* 角标是增强信息，读不到就不显示，不影响主流程 */
        });
    };
    load();
    const timer = window.setInterval(load, 15000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, [task?.id]);
  const [interceptPendingCount, setInterceptPendingCount] = React.useState(0);
  const [profiles, setProfiles] = React.useState<LLMProfile[]>([]);
  const [archiving, setArchiving] = React.useState(false);
  const [distilling, setDistilling] = React.useState(false);

  React.useEffect(() => {
    api
      .llmProfiles()
      .then(setProfiles)
      .catch(() => setProfiles([]));
  }, []);

  // 待审批计数：5s 轮询改为 usePolling 统一管理（可见性暂停 / 去重 / 退避）。
  usePolling(
    async () => {
      const rows = await api.interceptTask(id);
      setInterceptPendingCount(rows.filter((r) => r.status === "pending").length);
    },
    { intervalMs: 5000 },
  );

  // 任务本体 + stats：在途去重原先靠 taskLoadInFlight 手工维护，现在由
  // usePolling 保证同一时刻只有一轮在跑；这里只保留「结果是否仍属于当前 id」
  // 的守卫，防止切任务后旧响应覆盖新页面。
  const loadTask = React.useCallback(async () => {
    const [taskResp, s] = await Promise.all([api.task(id), api.stats(id).catch(() => null)]);
    const base = { ...taskResp };
    const at = s?.active_task;
    if (at) {
      base.in_flight = at.in_flight;
      base.goals_total = at.goals_total;
      base.goals_met = at.goals_met;
      base.engine_mode = s?.engine_mode ?? at.engine_mode;
      base.paused = at.paused;
    }
    setTask(base);
    setPaused(at?.paused ?? base.paused ?? false);
    setLoaded(true);
  }, [id]);
  usePolling(loadTask, { intervalMs: 5000, enabled: Boolean(id) });

  async function togglePause() {
    if (task && ["done", "failed", "timeout"].includes(task.status)) return;
    const next = !paused;
    try {
      await api.controlTask(id, next ? "pause" : "resume");
      setPaused(next);
      toast.success(next ? t("control.paused") : t("control.resumed"));
    } catch (e) {
      toast.error(t("control.failed", { message: (e as Error).message }));
    }
  }

  async function archiveTask() {
    if (!task || archiving) return;
    setArchiving(true);
    try {
      await api.archiveTask(task.id);
      toast.success(t("archive.queued"));
      router.push("/function/tasks");
    } catch (error) {
      toast.error(t("archive.failed", { message: (error as Error).message }));
      setArchiving(false);
    }
  }

  async function distillTask() {
    if (!task || distilling) return;
    setDistilling(true);
    try {
      const res = await api.distillTaskSkill({ task_id: Number(task.id) });
      const prop = res.proposal;
      toast.success(
        prop.status === "applied"
          ? t("distill.applied", { skill: prop.skill })
          : t("distill.pending", { skill: prop.skill }),
        {
          action: {
            label: t("distill.openReview"),
            onClick: () => router.push(`/system/skills?proposal=${encodeURIComponent(prop.id)}`),
          },
        },
      );
    } catch (error) {
      toast.error(t("distill.failed", { message: (error as Error).message }));
    } finally {
      setDistilling(false);
    }
  }

  if (!task) {
    return (
      <div className="flex flex-1 flex-col items-center justify-center gap-3 p-10 text-center">
        <p className="text-muted-foreground">{loaded ? t("header.notFound", { id }) : t("header.loading")}</p>
        {loaded && (
          <Button asChild variant="outline">
            <Link href="/function/tasks">
              <ArrowLeftIcon /> {t("header.backToList")}
            </Link>
          </Button>
        )}
      </div>
    );
  }

  const completed = task.status === "done";
  const terminal = ["done", "failed", "timeout"].includes(task.status);
  const archiveLifecycleEligible = terminal || paused || task.status === "paused";
  const canArchive = archiveLifecycleEligible && !task.archive_blocked_by_task_id;
  let archiveDisabledReason = task.queued ? t("archive.disabledQueued") : t("archive.disabledRunning");
  if (archiveLifecycleEligible && task.archive_blocked_by_task_id) {
    archiveDisabledReason = t("archive.disabledInherited", { id: task.archive_blocked_by_task_id });
  }
  const engineMode = paused ? "paused" : (task.engine_mode ?? "idle");
  let controlVariant: "default" | "secondary" | "outline" = "outline";
  let controlIcon = <PauseIcon data-icon="inline-start" />;
  let controlLabel = t("control.pause");
  if (terminal) {
    controlVariant = "secondary";
    controlIcon = <CheckIcon data-icon="inline-start" />;
    controlLabel = completed ? t("control.completed") : t("control.ended");
  } else if (paused) {
    controlVariant = "default";
    controlIcon = <PlayIcon data-icon="inline-start" />;
    controlLabel = t("control.resume");
  }

  const totalFindings =
    (task.findings?.critical ?? 0) +
    (task.findings?.high ?? 0) +
    (task.findings?.medium ?? 0) +
    (task.findings?.low ?? 0);
  const inFlightCount = task.in_flight ?? 0;
  const goalsTotal = task.goals_total ?? 0;
  const goalsMet = task.goals_met ?? 0;

  const archiveTrigger = (
    <Button
      size="icon-sm"
      variant="ghost"
      className="size-7"
      disabled={!canArchive || archiving}
      aria-label={canArchive ? t("archive.triggerAria") : archiveDisabledReason}
    >
      {archiving ? <Spinner /> : <ArchiveIcon className="size-3.5" />}
    </Button>
  );

  return (
    <Tabs data-editorial="body" value={tab} onValueChange={setTab} className="flex flex-1 flex-col gap-0">
      {task.analysis_domain === "software_re" && (
        <Alert className="rounded-none border-x-0 border-t-0">
          <BrainIcon className="size-4" />
          <AlertTitle>{t("analysisDomain.title")}</AlertTitle>
          <AlertDescription>{t("analysisDomain.hint")}</AlertDescription>
        </Alert>
      )}
      {/* ── 封面：工具行 → 书眉 → 数字行 ────────────────────────────────────
          纸本主题下这一块就是任务册的封面：编号/分类/状态一行，
          26px 标题压住目标，下面一条五格数字行。 */}
      <header className="border-border/80 border-b">
        <div className="flex flex-wrap items-center gap-2 px-4 pt-2 lg:px-6">
          <SidebarTrigger className="-ml-1" />
          <Button asChild variant="ghost" size="icon-sm" className="size-7 rounded-lg">
            <Link href="/function/tasks">
              <ArrowLeftIcon className="size-4" />
            </Link>
          </Button>
          <div className="min-w-0 flex-1" />
          <Separator orientation="vertical" className="mx-0.5 hidden h-4 sm:block" />
          <Button asChild size="sm" variant="outline" className="h-7 gap-1.5 rounded-lg px-2.5 text-xs">
            <Link href={`/function/tasks/session?id=${encodeURIComponent(id)}`}>
              <MessagesSquareIcon className="size-3.5" data-icon="inline-start" />
              <span className="hidden sm:inline">主 Agent 对话</span>
            </Link>
          </Button>
          <TaskLLMControl task={task} profiles={profiles} onUpdated={loadTask} />

          {interceptPendingCount > 0 && (
            <Link
              href="/system/intercept/approvals"
              className="shrink-0 rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              title={t("header.pendingApprovalsTitle", { n: interceptPendingCount })}
            >
              <Badge
                variant="outline"
                className="gap-1 border-amber-500/40 bg-amber-500/10 text-amber-600 dark:text-amber-400"
              >
                <ShieldAlertIcon className="size-3.5" />
                <span className="hidden sm:inline">{t("header.pendingApprovals", { n: interceptPendingCount })}</span>
                <span className="sm:hidden">×{interceptPendingCount}</span>
              </Badge>
            </Link>
          )}
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                size="sm"
                variant="outline"
                className="h-7 gap-1.5 rounded-lg px-2.5 text-xs"
                disabled={distilling}
                onClick={() => void distillTask()}
                aria-label={t("distill.triggerAria")}
              >
                {distilling ? <Spinner data-icon="inline-start" /> : <SparklesIcon className="size-3.5 text-primary" />}
                <span className="hidden sm:inline">{t("distill.button")}</span>
              </Button>
            </TooltipTrigger>
            <TooltipContent>{t("distill.tooltip")}</TooltipContent>
          </Tooltip>
          {canArchive ? (
            <AlertDialog>
              <AlertDialogTrigger asChild>{archiveTrigger}</AlertDialogTrigger>
              <AlertDialogContent>
                <AlertDialogHeader>
                  <AlertDialogTitle>{t("archive.dialogTitle", { id: task.id })}</AlertDialogTitle>
                  <AlertDialogDescription>{t("archive.dialogDesc")}</AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                  <AlertDialogCancel>{t("archive.cancel")}</AlertDialogCancel>
                  <AlertDialogAction onClick={() => void archiveTask()}>{t("archive.confirm")}</AlertDialogAction>
                </AlertDialogFooter>
              </AlertDialogContent>
            </AlertDialog>
          ) : (
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="inline-flex">{archiveTrigger}</span>
              </TooltipTrigger>
              <TooltipContent>{archiveDisabledReason}</TooltipContent>
            </Tooltip>
          )}
          <Button
            size="sm"
            variant={controlVariant}
            className="h-7 rounded-lg px-2.5 text-xs"
            onClick={() => void togglePause()}
            disabled={terminal}
          >
            {controlIcon}
            {controlLabel}
          </Button>
        </div>

        {/* 书眉：编号 / 分类 / 状态 / 标题 / 目标 */}
        <div className="flex flex-col gap-1 px-4 pt-3 pb-2 lg:px-6">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground tabular-nums">
            <code className="rounded-[2px] border border-border/60 px-1 py-px font-mono text-xs">#{task.id}</code>
            <span>{t(task.analysis_domain === "software_re" ? "analysisDomain.software" : "analysisDomain.web")}</span>
            <span className="h-2 w-px bg-border" />
            <StatusBadge domain={terminal ? "task" : "engine"} value={terminal ? task.status : engineMode} dot />
            {openBranches > 0 && (
              <span className="text-amber-600 dark:text-amber-400" title={t("openBranchesTip")}>
                {t("openBranches", { n: openBranches })}
              </span>
            )}
            {task.created_at && <span className="h-2 w-px bg-border" />}
            {task.created_at && <span>{task.created_at}</span>}
          </div>
          <h1 className="font-display text-[26px] leading-tight font-semibold tracking-[-0.02em]" title={task.name || task.description}>
            {task.name || task.description}
          </h1>
          {task.goal && (
            <p className="flex items-start gap-1.5 text-[13px] leading-relaxed text-muted-foreground">
              <CrosshairIcon className="mt-[3px] size-3.5 shrink-0 text-primary" />
              <span className="line-clamp-2">{task.goal}</span>
            </p>
          )}
        </div>

        {/* 数字行：五格，没有框，只靠竖线分开 */}
        <KpiRow className="mx-4 mb-3 grid grid-cols-2 divide-y divide-border/60 border-border/60 border-y sm:grid-cols-3 sm:divide-x sm:divide-y-0 lg:mx-6 lg:grid-cols-5">
          <button
            type="button"
            onClick={() => setTab("overview")}
            className="flex flex-col items-start gap-0.5 px-3 py-2 text-left transition-colors hover:bg-accent/40"
          >
            <span className="text-xs tracking-[0.08em] text-muted-foreground">目标达成</span>
            <span className="font-semibold text-[22px] leading-none tabular-nums">
              {goalsMet}
              <span className="ml-0.5 font-normal text-[12px] text-muted-foreground">/ {goalsTotal}</span>
            </span>
          </button>
          <button
            type="button"
            onClick={() => setTab("graph")}
            className="flex flex-col items-start gap-0.5 px-3 py-2 text-left transition-colors hover:bg-accent/40"
          >
            <span className="text-xs tracking-[0.08em] text-muted-foreground">在途 Worker</span>
            <span className="font-semibold text-[22px] leading-none tabular-nums">{inFlightCount}</span>
          </button>
          <button
            type="button"
            onClick={() => setTab("findings")}
            className="flex flex-col items-start gap-0.5 px-3 py-2 text-left transition-colors hover:bg-accent/40"
          >
            <span className="text-xs tracking-[0.08em] text-muted-foreground">已确认发现</span>
            <span className="font-semibold text-[22px] leading-none tabular-nums">{totalFindings}</span>
          </button>
          <button
            type="button"
            onClick={() => setTab("intercept")}
            className="flex flex-col items-start gap-0.5 px-3 py-2 text-left transition-colors hover:bg-accent/40"
          >
            <span className="text-xs tracking-[0.08em] text-muted-foreground">待审批</span>
            <span className="font-semibold text-[22px] leading-none tabular-nums">{interceptPendingCount}</span>
          </button>
          <button
            type="button"
            onClick={() => setTab("timeline")}
            className="flex flex-col items-start gap-0.5 px-3 py-2 text-left transition-colors hover:bg-accent/40"
          >
            <span className="text-xs tracking-[0.08em] text-muted-foreground">未收口分支</span>
            <span className="font-semibold text-[22px] leading-none tabular-nums">{openBranches}</span>
          </button>
        </KpiRow>

        {/* 页签：下划线索引，吸顶 */}
        <div className="no-scrollbar sticky top-0 z-10 overflow-x-auto bg-background/92 px-4 pt-1 backdrop-blur-md lg:px-6">
          <TabsList variant="line" className="h-8 min-w-max gap-4 border-0 bg-transparent p-0">
            {(task.analysis_domain === "software_re" ? SOFTWARE_RE_TABS : WEB_TABS).map((value) => (
              <TabsTrigger key={value} value={value} className="h-8 px-0 text-xs">
                {t(`tabs.${value}`)}
                {value === "findings" && totalFindings > 0 && (
                  <span className="ml-1 text-xs font-semibold text-rose-600 tabular-nums dark:text-rose-400">
                    {totalFindings > 99 ? "99+" : totalFindings}
                  </span>
                )}
                {value === "intercept" && interceptPendingCount > 0 && (
                  <span className="ml-1 text-xs font-semibold text-amber-600 tabular-nums dark:text-amber-400">
                    {interceptPendingCount > 99 ? "99+" : interceptPendingCount}
                  </span>
                )}
              </TabsTrigger>
            ))}
          </TabsList>
        </div>
      </header>

      {/* Tab content */}
      <div className={cn("flex-1", tab === "sessions" ? "p-2.5 lg:p-4" : "p-4 lg:p-6")}>
        {task.analysis_domain === "software_re" && (
          <TabsContent value="software" className="mt-0">
            <SoftwareRETab key={id} taskId={id} />
          </TabsContent>
        )}
        <TabsContent value="sessions" className="mt-0">
          <SessionsTab taskId={id} />
        </TabsContent>
        <TabsContent value="overview" className="mt-0">
          <OverviewTab taskId={id} />
        </TabsContent>
        <TabsContent value="graph" className="mt-0">
          <GraphTab taskId={id} />
        </TabsContent>
        <TabsContent value="broadcast" className="mt-0">
          <BroadcastTab taskId={id} />
        </TabsContent>
        <TabsContent value="findings" className="mt-0">
          <FindingsTab taskId={id} />
        </TabsContent>
        <TabsContent value="retests" className="mt-0">
          <RetestsTab key={id} taskId={id} />
        </TabsContent>
        <TabsContent value="assets" className="mt-0">
          <AssetsTab taskId={id} />
        </TabsContent>
        <TabsContent value="coverage" className="mt-0">
          <CoverageGraphTab taskId={id} coverageEnabled={task?.coverage_enabled !== false} />
        </TabsContent>
        <TabsContent value="intercept" className="mt-0">
          <InterceptTab taskId={id} />
        </TabsContent>
        <TabsContent value="timeline" className="mt-0">
          <TimelineTab taskId={id} onReverted={loadTask} />
        </TabsContent>
        <TabsContent value="report" className="mt-0">
          <ReportTab taskId={id} analysisDomain={task.analysis_domain} />
        </TabsContent>
      </div>
    </Tabs>
  );
}

// useSearchParams must sit under a Suspense boundary for static export.
export default function TaskDetailPage() {
  return (
    <React.Suspense fallback={null}>
      <TaskDetailInner />
    </React.Suspense>
  );
}
