"use client";

import * as React from "react";

import Link from "next/link";

import {
  ArrowDownIcon,
  ArrowUpDownIcon,
  ArrowUpIcon,
  EyeIcon,
  GitBranchIcon,
  Loader2Icon,
  LockIcon,
  MessagesSquareIcon,
  PauseIcon,
  PinIcon,
  PlayIcon,
  PlugIcon,
  SearchIcon,
  ShieldAlertIcon,
  StarIcon,
  Trash2Icon,
  Undo2Icon,
  XIcon,
} from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { EmptyState } from "@/components/empty-state";
import { useStaggerOnce } from "@/components/motion";
import { StatusBadge } from "@/components/status-badge";
import { TablePagination } from "@/components/table-pagination";
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
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { usePolling } from "@/hooks/use-polling";
import { ApiError, api } from "@/lib/api";
import { getLocalStorageValue, setLocalStorageValue } from "@/lib/local-storage.client";
import { severityTextClass } from "@/lib/severity";
import { type SortDirection, useStoredSortPreference } from "@/lib/sort-preference";
import type {
  Company,
  DeleteTaskOptions,
  DeleteTaskResult,
  Task,
  TaskArchive,
  TaskArchiveState,
  TaskCategory,
  TaskStatus,
} from "@/lib/types";
import { cn } from "@/lib/utils";
import { Sheet as PaperSheet } from "@/ui/container";

// fmtBytes renders a human file size for the upload manifest.
function _fmtBytes(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${(n / (1 << 10)).toFixed(1)} KB`;
  return `${n} B`;
}

// appendUploads folds newly-uploaded files' ABSOLUTE paths into the description as a
// Read/Bash-friendly manifest — the worker opens them by path. Keeps one marked block
// (tasksPage.upload.marker): first upload adds the header, later uploads append bullets
// under it. The marker/hint come from the active locale so the description seed matches
// the UI language; the includes() check reuses the same translated marker.

// POLL_MS is the task-list refresh interval. Task state moves on the server (planner /
// worker), so the list has to be pulled; 10s is plenty for status / 进度 / token 变化.
const POLL_MS = 10_000;

// fmtTokens renders a compact token count (1234 → 1.2k, 2_000_000 → 2M).
function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n >= 10_000_000 ? 0 : 1)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(n >= 10000 ? 0 : 1)}k`;
  return String(n);
}

// fmtDuration renders a run duration in seconds as a compact human string.
function fmtDuration(sec: number): string {
  if (sec <= 0) return "—";
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const s = sec % 60;
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

// fmtFirstFindingMS renders 「首发现」耗时（毫秒差）为紧凑字符串（4m / 1h20m）。
// 0 = 尚未产出 finding，调用方在渲染前已判零。
function fmtFirstFindingMS(ms: number): string {
  const sec = Math.floor(ms / 1000);
  if (sec < 60) return `${sec}s`;
  const m = Math.floor(sec / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  return `${h}h${String(m % 60).padStart(2, "0")}m`;
}

// fmtPerHour renders the backend's one-decimal findings-per-hour float without a
// trailing ".0"（12.0 → 12，0.5 → 0.5）。
function fmtPerHour(v: number): string {
  return Number.isInteger(v) ? String(v) : v.toFixed(1);
}

// taskDuration is the task's run duration in seconds: created → now while running,
// created → completion for a finished task, else created → last activity. 0 when it
// never ran (no activity yet).
function taskDuration(task: Task, nowSec: number): number {
  const start = task.created_unix ?? 0;
  if (!start) return 0;
  const end =
    task.status === "running"
      ? nowSec
      : task.completed_unix && task.completed_unix > 0
        ? task.completed_unix
        : (task.last_activity_unix ?? 0);
  return end > start ? end - start : 0;
}

// deleteDetails takes only the countable part of the result so callers can pass an
// aggregate accumulated over a bulk delete.
type DeleteCounts = Omit<DeleteTaskResult, "deleted" | "cleanup_warning">;

// 模块级函数不能调用 Hook，因此通过参数接收翻译函数生成用户消息，
// 与 approval-records 的模式一致。
type Translator = ReturnType<typeof useTranslations>;

function _deleteDetails(t: Translator, result: DeleteCounts): string[] {
  const details: string[] = [];
  if (result.assets_deleted > 0) details.push(t("delete.detailAssets", { n: result.assets_deleted }));
  if (result.assets_detached > 0) details.push(t("delete.detailAssetsDetached", { n: result.assets_detached }));
  if (result.traffic_deleted > 0) details.push(t("delete.detailTraffic", { n: result.traffic_deleted }));
  if (result.files_deleted) details.push(t("delete.detailFiles"));
  if (result.findings_deleted > 0) details.push(t("delete.detailFindings", { n: result.findings_deleted }));
  if (result.llm_records_deleted > 0) details.push(t("delete.detailLlm", { n: result.llm_records_deleted }));
  return details;
}

// fmtDateTime renders a unix-seconds timestamp as a compact local date-time
// (MM-DD HH:mm), or "—" when unset.
function fmtDateTime(unix?: number): string {
  if (!unix || unix <= 0) return "—";
  const d = new Date(unix * 1000);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

// 状态过滤选项只保存值与顺序，标签在渲染时从 status.task 翻译。
const STATUS_OPTIONS: readonly TaskStatus[] = ["created", "queued", "running", "paused", "done", "failed", "timeout"];

// Select 不接受空字符串 value,所以「无分类」在筛选器、新建表单和批量移动里
// 统一用这个哨兵值,提交时再翻译成后端的 null。
const UNCATEGORIZED_VALUE = "uncategorized";

// 可暂停 = 非终态且未暂停,与后端 applyTaskControlWithCause 的门控一致
// (done/failed/timeout 为终态);paused 才可继续。行内按钮与批量控制共用此判断,
// 两边不会出现一个可点、另一个不可点的分歧。
const PAUSABLE_STATUSES = new Set<TaskStatus>(["created", "queued", "running"]);
const _ARCHIVABLE_STATUSES = new Set<TaskStatus>(["paused", "done", "failed", "timeout"]);

function _taskControlAction(status: TaskStatus): "pause" | "resume" | null {
  if (status === "paused") return "resume";
  if (PAUSABLE_STATUSES.has(status)) return "pause";
  return null;
}

type TaskSortField = "id" | "created" | "duration" | "status";

const TASK_SORT_FIELDS: readonly TaskSortField[] = ["id", "created", "duration", "status"];
const TASK_SORT_PREFERENCE_KEY = "artex_task_list_sort";
const TASK_FILTER_PREFERENCE_KEY = "artex_task_list_filters";

const TASK_STATUS_RANK = new Map(STATUS_OPTIONS.map((status, index) => [status, index]));

function taskIsPinned(task: Task): boolean {
  return task.pinned ?? Boolean(task.pinned_at);
}

function compareTaskIDs(left: string, right: string): number {
  return left.localeCompare(right, undefined, { numeric: true, sensitivity: "base" });
}

function taskCreatedUnix(task: Task): number {
  if (task.created_unix) return task.created_unix;
  const parsed = Date.parse(task.created_at);
  return Number.isNaN(parsed) ? 0 : Math.floor(parsed / 1000);
}

function compareTasks(left: Task, right: Task, field: TaskSortField, direction: SortDirection, nowSec: number): number {
  const leftPinned = taskIsPinned(left);
  const rightPinned = taskIsPinned(right);
  if (leftPinned !== rightPinned) return leftPinned ? -1 : 1;
  if (leftPinned && left.pinned_at !== right.pinned_at) {
    return (right.pinned_at ?? "").localeCompare(left.pinned_at ?? "");
  }

  let compared = 0;
  switch (field) {
    case "created":
      compared = taskCreatedUnix(left) - taskCreatedUnix(right);
      break;
    case "duration":
      compared = taskDuration(left, nowSec) - taskDuration(right, nowSec);
      break;
    case "status":
      compared = (TASK_STATUS_RANK.get(left.status) ?? 0) - (TASK_STATUS_RANK.get(right.status) ?? 0);
      break;
    default:
      compared = compareTaskIDs(left.id, right.id);
  }
  if (compared !== 0) return direction === "asc" ? compared : -compared;
  return -compareTaskIDs(left.id, right.id);
}

export default function TasksPage() {
  const t = useTranslations("tasksPage");
  const ts = useTranslations("status");
  const [activeTab, setActiveTab] = React.useState("current");
  const [tasks, setTasks] = React.useState<Task[]>([]);
  const [categories, setCategories] = React.useState<TaskCategory[]>([]);
  const [categoriesLoaded, setCategoriesLoaded] = React.useState(false);
  const [query, setQuery] = React.useState("");
  const [statusFilter, setStatusFilter] = React.useState<TaskStatus | "all">("all");
  const [categoryFilter, setCategoryFilter] = React.useState("all");
  const [filtersHydrated, setFiltersHydrated] = React.useState(false);
  const [sortPreference, setSortPreference] = useStoredSortPreference(
    TASK_SORT_PREFERENCE_KEY,
    TASK_SORT_FIELDS,
    "id",
    "desc",
  );
  const { field: sortField, direction: sortDirection } = sortPreference;
  const [page, setPage] = React.useState(1);
  const [pageSize, setPageSize] = React.useState(20);
  const [nowSec, setNowSec] = React.useState(() => Math.floor(Date.now() / 1000));

  React.useEffect(() => {
    const raw = getLocalStorageValue(TASK_FILTER_PREFERENCE_KEY);
    if (raw) {
      try {
        const parsed = JSON.parse(raw) as { status?: unknown; category?: unknown };
        if (parsed.status === "all" || STATUS_OPTIONS.some((status) => status === parsed.status)) {
          setStatusFilter(parsed.status as TaskStatus | "all");
        }
        if (
          typeof parsed.category === "string" &&
          (parsed.category === "all" || parsed.category === UNCATEGORIZED_VALUE || /^\d+$/.test(parsed.category))
        ) {
          setCategoryFilter(parsed.category);
        }
      } catch {
        // Ignore malformed or legacy preferences and retain the defaults.
      }
    }
    setFiltersHydrated(true);
  }, []);

  React.useEffect(() => {
    if (!filtersHydrated) return;
    setLocalStorageValue(
      TASK_FILTER_PREFERENCE_KEY,
      JSON.stringify({ status: statusFilter, category: categoryFilter }),
    );
  }, [categoryFilter, filtersHydrated, statusFilter]);

  const filtered = React.useMemo(() => {
    const q = query.trim().toLowerCase();
    return tasks.filter((t) => {
      if (statusFilter !== "all" && t.status !== statusFilter) return false;
      if (categoryFilter === UNCATEGORIZED_VALUE && t.category_id != null) return false;
      if (
        categoryFilter !== "all" &&
        categoryFilter !== UNCATEGORIZED_VALUE &&
        String(t.category_id) !== categoryFilter
      )
        return false;
      if (!q) return true;
      return (
        (t.name ?? "").toLowerCase().includes(q) ||
        (t.category_name ?? "").toLowerCase().includes(q) ||
        t.description.toLowerCase().includes(q) ||
        t.goal.toLowerCase().includes(q) ||
        t.id.toLowerCase().includes(q)
      );
    });
  }, [tasks, query, statusFilter, categoryFilter]);

  const sortNowSec = sortField === "duration" ? nowSec : 0;
  const ordered = React.useMemo(() => {
    return [...filtered].sort((left, right) => compareTasks(left, right, sortField, sortDirection, sortNowSec));
  }, [filtered, sortDirection, sortField, sortNowSec]);

  const sortTasksBy = React.useCallback(
    (field: TaskSortField) => {
      setSortPreference((current) => {
        if (current.field !== field) return { field, direction: "desc" };
        return { field, direction: current.direction === "asc" ? "desc" : "asc" };
      });
    },
    [setSortPreference],
  );

  // reset to page 1 whenever filters or ordering change
  // biome-ignore lint/correctness/useExhaustiveDependencies: both filters intentionally reset pagination.
  React.useEffect(() => {
    setPage(1);
  }, [query, statusFilter, categoryFilter, sortField, sortDirection]);

  const paginated = React.useMemo(
    () => ordered.slice((page - 1) * pageSize, page * pageSize),
    [ordered, page, pageSize],
  );

  // 表格行进场:任务列表没有独立的 loaded 标记,首批非空数据即"就绪";
  // useStaggerOnce 只在首个 1.2s 窗口内返回 true,翻页/筛选(重排)不再重播。
  const listIntro = useStaggerOnce(tasks.length > 0);

  // lastRef holds the previous poll's serialized payload: the list is re-fetched every
  // POLL_MS but usually comes back unchanged, and setTasks on an identical payload would
  // re-render the whole page for nothing. Bail out when it matches.
  const lastRef = React.useRef<string>("");

  // 返回 Promise：usePolling 靠它判定「本轮是否结束」与「是否失败」。
  // 内部仍吞掉异常——轮询失败不该在页面上弹错，退避由 hook 负责。
  const load = React.useCallback(async () => {
    try {
      const r = await api.tasks();
      const next = r.tasks.map((t) => (t.id === r.active ? { ...t, active: true } : t));
      const sig = JSON.stringify(next);
      if (sig === lastRef.current) return;
      lastRef.current = sig;
      setTasks(next);
    } catch {
      // 下一轮由 usePolling 按退避重试。
    }
  }, []);

  const loadCategories = React.useCallback(() => {
    api
      .taskCategories()
      .then((next) => {
        setCategories(next);
        setCategoriesLoaded(true);
      })
      .catch(() => {
        // Category management remains retryable without blocking the task list.
      });
  }, []);

  // 分类是低频数据（只在用户增删分类时变化），单独拉一次，不进轮询。
  React.useEffect(() => {
    loadCategories();
  }, [loadCategories]);

  usePolling(() => load(), { intervalMs: POLL_MS });

  React.useEffect(() => {
    if (!categoriesLoaded) return;
    if (categoryFilter === "all" || categoryFilter === UNCATEGORIZED_VALUE) return;
    if (!categories.some((category) => String(category.id) === categoryFilter)) setCategoryFilter("all");
  }, [categories, categoriesLoaded, categoryFilter]);

  // 只在确有 running 任务时才每秒 tick——其余情况「运行时长」是静态值,空转的 tick 会白白
  // 重渲染整张表。
  const hasRunning = React.useMemo(() => tasks.some((t) => t.status === "running"), [tasks]);

  // tick every second so running tasks' 运行时长 counts up live.
  React.useEffect(() => {
    if (!hasRunning) return;
    setNowSec(Math.floor(Date.now() / 1000));
    const i = setInterval(() => setNowSec(Math.floor(Date.now() / 1000)), 1000);
    return () => clearInterval(i);
  }, [hasRunning]);

  return (
    <Tabs data-editorial="body" value={activeTab} onValueChange={setActiveTab} className="gap-4">
      <TabsList className="mx-4 lg:mx-6">
        <TabsTrigger value="current">{t("tab.current")}</TabsTrigger>
        <TabsTrigger value="archived">{t("tab.archived")}</TabsTrigger>
      </TabsList>
      <TabsContent value="current">
        <PaperSheet
          api={[
            "tasks",
            "taskCategories",
            "createTask",
            "deleteTask",
            "renameTask",
            "pinTask",
            "archiveTask",
            "archiveTasks",
            "controlTask",
            "controlTasksBatch",
            "updateTasksCategory",
            "createTaskCategory",
            "renameTaskCategory",
            "deleteTaskCategory",
            "updateTaskCategory",
            "chatUpload",
            "companies",
            "llmProfiles",
            "settings",
            "setSettings",
          ]}
        >
          <div className="flex flex-col gap-4 px-0 pt-4">
            <div className="flex flex-wrap items-center gap-2 px-4 lg:px-6">
              <div className="relative w-full sm:max-w-xs">
                <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  placeholder={t("filter.searchPlaceholder")}
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  className="pl-8"
                />
                {query && (
                  <button
                    type="button"
                    onClick={() => setQuery("")}
                    aria-label={t("filter.clearSearch")}
                    className="absolute top-1/2 right-2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                  >
                    <XIcon className="size-4" />
                  </button>
                )}
              </div>
              <Select value={statusFilter} onValueChange={(v) => setStatusFilter(v as TaskStatus | "all")}>
                <SelectTrigger className="w-36">
                  <SelectValue placeholder={t("filter.statusPlaceholder")} />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="all">{t("filter.allStatus")}</SelectItem>
                    {STATUS_OPTIONS.map((status) => (
                      <SelectItem key={status} value={status}>
                        {ts(`task.${status}`)}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
              <Select value={categoryFilter} onValueChange={setCategoryFilter}>
                <SelectTrigger className="w-40">
                  <SelectValue placeholder={t("filter.categoryPlaceholder")} />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="all">{t("filter.allCategory")}</SelectItem>
                    <SelectItem value={UNCATEGORIZED_VALUE}>{t("filter.uncategorized")}</SelectItem>
                    {categories.map((category) => (
                      <SelectItem key={category.id} value={String(category.id)}>
                        {category.name}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
              <span className="text-muted-foreground text-xs tabular-nums">
                {t("count", { shown: filtered.length, total: tasks.length })}
              </span>
              {/* 只读化（H2）：新建任务、分类管理、并发设置、批量暂停/恢复/归档/移动/重命名/
                  删除的入口全部移除。任务从对话页发起，这里只负责看与筛选。 */}
              <span className="ml-auto inline-flex items-center gap-1.5 text-muted-foreground text-xs">
                <LockIcon className="size-3.5" />
                {t("readonly.badge")}
              </span>
            </div>

            {tasks.length === 0 ? (
              <div className="mx-4 rounded-lg border border-dashed lg:mx-6">
                <EmptyState illustration="flag" title={t("empty.noTasks")} />
              </div>
            ) : filtered.length === 0 ? (
              <div className="mx-4 rounded-lg border border-dashed lg:mx-6">
                <EmptyState illustration="flag" title={t("empty.noMatch")} description={t("empty.noMatchHint")} />
              </div>
            ) : (
              <Table className="**:data-[slot='table-cell']:px-4 **:data-[slot='table-head']:px-4">
                <TableHeader className="[&_tr]:border-t">
                  <TableRow>
                    <SortableTaskHead
                      field="id"
                      label="ID"
                      activeField={sortField}
                      direction={sortDirection}
                      className="font-mono"
                      onSort={sortTasksBy}
                    />
                    <TableHead>{t("table.name")}</TableHead>
                    <TableHead>{t("table.description")}</TableHead>
                    <TableHead>{t("table.target")}</TableHead>
                    <TableHead title={t("table.sourceTitle")}>{t("table.source")}</TableHead>
                    <SortableTaskHead
                      field="status"
                      label={t("table.status")}
                      activeField={sortField}
                      direction={sortDirection}
                      onSort={sortTasksBy}
                    />
                    <TableHead className="text-center">{t("table.goalProgress")}</TableHead>
                    <TableHead className="text-center" title={t("table.findingsTitle")}>
                      {t("table.findings")}{" "}
                      <span className="font-normal text-muted-foreground">{t("table.findingsSub")}</span>
                    </TableHead>
                    <TableHead className="text-center" title={t("table.outputTitle")}>
                      {t("table.output")}
                    </TableHead>
                    <TableHead className="text-center">{t("table.runningWorker")}</TableHead>
                    <SortableTaskHead
                      field="created"
                      label={t("table.created")}
                      activeField={sortField}
                      direction={sortDirection}
                      align="right"
                      onSort={sortTasksBy}
                    />
                    <SortableTaskHead
                      field="duration"
                      label={t("table.duration")}
                      activeField={sortField}
                      direction={sortDirection}
                      align="right"
                      onSort={sortTasksBy}
                    />
                    <TableHead className="text-right">Token</TableHead>
                    <TableHead className="sticky right-0 z-10 bg-card text-right shadow-[-1px_0_0_0_hsl(var(--border))]">
                      {t("table.actions")}
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {paginated.map((task, index) => (
                    <TaskRow
                      key={task.id}
                      task={task}
                      // 首批进场:前 10 行 50ms 递增、封顶 450ms;窗口关闭后不再下发延迟。
                      introDelay={listIntro && index < 10 ? Math.min(index * 50, 450) : undefined}
                      // running 任务才吃 nowSec;其余行传 0 —— props 不变,memo 就能拦下每秒 tick
                      // 带来的整表重渲染,只让在跑的那几行走时长。
                      nowSec={task.status === "running" ? nowSec : 0}
                    />
                  ))}
                </TableBody>
              </Table>
            )}
            <TablePagination
              page={page}
              pageSize={pageSize}
              total={filtered.length}
              onPageChange={setPage}
              onPageSizeChange={(nextPageSize) => {
                setPageSize(nextPageSize);
                setPage(1);
              }}
            />
          </div>
        </PaperSheet>
      </TabsContent>
      <TabsContent value="archived">
        <TaskArchivesPanel onChanged={load} />
      </TabsContent>
    </Tabs>
  );
}

// TaskSourceCell 回答「这个任务从哪来」。后端没有单独的 origin 字段，这里有三个可判定信号
// （都在 TaskDTO 里，不需要改后端）：
//   • source_task_ids        → 从别的任务派生（spawn_task 带内容继承）
//   • pending_approvals 存在 → 至少产生过需要人工放行的动作（MCP / 外部接入）
//   • 都没有                   → 内部派发（对话页 / 任务台）
// 注意：HTTP createTask 与编排 spawn_task 都会走同一条建任务路径，没有预留字段区分，
// 所以这里不做臆测，只用上面三个真实信号。
function TaskSourceCell({ task }: { task: Task }) {
  const t = useTranslations("tasksPage");
  const sources = task.source_task_ids ?? [];
  if (sources.length > 0) {
    return (
      <span className="inline-flex items-center gap-1" title={t("source.fromTasksTitle", { ids: sources.join(", ") })}>
        <GitBranchIcon className="size-3.5 shrink-0 text-muted-foreground" />
        {t("source.fromTasks", { id: sources[0] })}
        {sources.length > 1 && <span className="text-muted-foreground">+{sources.length - 1}</span>}
      </span>
    );
  }
  if (typeof task.pending_approvals === "number") {
    return (
      <span className="inline-flex items-center gap-1" title={t("source.externalTitle")}>
        <PlugIcon className="size-3.5 shrink-0 text-muted-foreground" />
        {t("source.external")}
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1" title={t("source.internalTitle")}>
      <MessagesSquareIcon className="size-3.5 shrink-0 text-muted-foreground" />
      {t("source.internal")}
    </span>
  );
}

function taskSortIcon(active: boolean, direction: SortDirection) {
  if (!active) {
    return <ArrowUpDownIcon className="size-3.5 opacity-40 transition-opacity group-hover/sort:opacity-100" />;
  }
  if (direction === "asc") return <ArrowUpIcon className="size-3.5" />;
  return <ArrowDownIcon className="size-3.5" />;
}

function SortableTaskHead({
  field,
  label,
  activeField,
  direction,
  align = "left",
  className,
  onSort,
}: {
  field: TaskSortField;
  label: string;
  activeField: TaskSortField;
  direction: SortDirection;
  align?: "left" | "right";
  className?: string;
  onSort: (field: TaskSortField) => void;
}) {
  const t = useTranslations("tasksPage");
  const active = activeField === field;
  let ariaSort: React.AriaAttributes["aria-sort"] = "none";
  if (active) ariaSort = direction === "asc" ? "ascending" : "descending";

  let actionLabel = t("sort.default", { label });
  if (active) actionLabel = t("sort.active", { label, direction });

  return (
    <TableHead className={className} aria-sort={ariaSort}>
      <button
        type="button"
        className={cn(
          "group/sort inline-flex h-full w-full items-center gap-1 outline-none focus-visible:underline",
          align === "right" && "justify-end",
        )}
        aria-label={actionLabel}
        onClick={() => onSort(field)}
      >
        <span>{label}</span>
        {taskSortIcon(active, direction)}
      </button>
    </TableHead>
  );
}

// TaskRow renders one row of the task table. Memoized so the per-second 运行时长 tick and
// the POLL_MS list refresh only re-render the rows whose data actually moved — a table page
// is 20 rows × (StatusBadge + Link + a Radix AlertDialog tree), far too heavy to rebuild
// wholesale on every parent render.
const TaskRow = React.memo(function TaskRow({
  task,
  nowSec,
  introDelay,
}: {
  task: Task;
  nowSec: number;
  /**
   * 首批进场延迟(ms):由父层 useStaggerOnce 的窗口下发,窗口关闭即 undefined。
   * <tr> 不能包 div,动画类与延迟直接挂在行上;翻页/筛选换批不下发,不重播。
   */
  introDelay?: number;
}) {
  const t = useTranslations("tasksPage");
  return (
    <TableRow
      className={cn("group border-border/60", introDelay !== undefined && "animate-fade-up")}
      style={introDelay !== undefined ? { animationDelay: `${introDelay}ms` } : undefined}
    >
      <TableCell>
        <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">{task.id}</code>
      </TableCell>
      <TableCell className="font-medium">
        <div className="flex max-w-xs items-center gap-2">
          <Link
            href={`/function/tasks/detail?id=${encodeURIComponent(task.id)}`}
            className="min-w-0 truncate font-medium hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            title={task.name ?? task.description}
          >
            {task.name ?? task.description}
          </Link>
          {task.active && <StarIcon className="size-4 shrink-0 fill-amber-400 text-amber-400" />}
          {taskIsPinned(task) && <PinIcon className="size-4 shrink-0 text-primary" aria-label={t("row.pinnedBadge")} />}
          {task.pending_approvals && task.pending_approvals > 0 ? (
            <Link
              href={`/system/intercept/approvals?task=${encodeURIComponent(task.id)}`}
              className="shrink-0 rounded-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              title={t("row.pendingApprovalsTitle", { n: task.pending_approvals })}
            >
              <Badge
                variant="outline"
                className="gap-1 border-amber-500/40 bg-amber-500/10 text-amber-600 dark:text-amber-400"
              >
                <ShieldAlertIcon className="size-3" />
                {t("row.pendingApprovals", { n: task.pending_approvals })}
              </Badge>
            </Link>
          ) : null}
        </div>
      </TableCell>
      <TableCell className="max-w-40 text-muted-foreground">
        <Link
          href={`/function/tasks/detail?id=${encodeURIComponent(task.id)}`}
          className="block truncate rounded-sm hover:text-foreground hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          title={task.description}
        >
          {task.description}
        </Link>
      </TableCell>
      <TableCell className="max-w-40 truncate text-muted-foreground" title={task.goal}>
        {task.goal}
      </TableCell>
      <TableCell className="whitespace-nowrap text-muted-foreground text-xs">
        <TaskSourceCell task={task} />
      </TableCell>
      <TableCell>
        <StatusBadge domain="task" value={task.status} dot />
      </TableCell>
      <TableCell className="text-center text-muted-foreground text-xs tabular-nums">
        {typeof task.goals_total === "number" && task.goals_total > 0 ? (
          `${task.goals_met}/${task.goals_total}`
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </TableCell>
      <TableCell className="text-center text-xs tabular-nums">
        {(() => {
          const f = task.findings;
          const total = f ? f.critical + f.high + f.medium + f.low : 0;
          if (!f || total === 0) return <span className="text-muted-foreground">0</span>;
          const seg = (n: number, cls: string) => <span className={n > 0 ? cls : "text-muted-foreground"}>{n}</span>;
          return (
            <span className="whitespace-nowrap font-medium" title={t("table.findingsTitle")}>
              {seg(f.critical, severityTextClass("critical"))}
              <span className="text-muted-foreground">/</span>
              {seg(f.high, severityTextClass("high"))}
              <span className="text-muted-foreground">/</span>
              {seg(f.medium, severityTextClass("medium"))}
              <span className="text-muted-foreground">/</span>
              {seg(f.low, severityTextClass("low"))}
            </span>
          );
        })()}
      </TableCell>
      <TableCell className="text-center text-xs tabular-nums">
        {(() => {
          // 价值指标：产出速率（条/时）+ 首发现耗时。两者都为 0 时给一个占位破折号，
          // 避免没有产出的任务在新增列上显示成"空单元格"。
          const perHour = task.findings_per_hour ?? 0;
          const firstMs = task.first_finding_ms ?? 0;
          if (perHour <= 0 && firstMs <= 0) return <span className="text-muted-foreground">—</span>;
          return (
            <div className="flex flex-col items-center gap-0.5" title={t("row.outputTitle")}>
              {perHour > 0 ? (
                <span className="whitespace-nowrap font-medium">
                  {fmtPerHour(perHour)} {t("row.perHourUnit")}
                </span>
              ) : (
                <span className="text-muted-foreground">—</span>
              )}
              {firstMs > 0 && (
                <span className="whitespace-nowrap text-muted-foreground text-xs">
                  {t("row.firstFinding", { time: fmtFirstFindingMS(firstMs) })}
                </span>
              )}
            </div>
          );
        })()}
      </TableCell>
      <TableCell className="text-center text-xs tabular-nums">
        {task.in_flight && task.in_flight > 0 ? (
          <span className="font-medium text-foreground">{task.in_flight}</span>
        ) : (
          <span className="text-muted-foreground">0</span>
        )}
      </TableCell>
      <TableCell className="whitespace-nowrap text-right text-muted-foreground text-xs tabular-nums">
        {fmtDateTime(task.created_unix)}
      </TableCell>
      <TableCell className="whitespace-nowrap text-right text-xs tabular-nums">
        {(() => {
          const secs = taskDuration(task, nowSec);
          if (secs <= 0) return <span className="text-muted-foreground">—</span>;
          return (
            <span className={task.status === "running" ? "text-foreground" : "text-muted-foreground"}>
              {fmtDuration(secs)}
            </span>
          );
        })()}
      </TableCell>
      <TableCell
        className="whitespace-nowrap text-right text-xs tabular-nums"
        title={
          task.tokens
            ? t("row.tokenTitle", {
                input: task.tokens.input_tokens,
                cache: task.tokens.cache_read_tokens,
                output: task.tokens.output_tokens,
              })
            : undefined
        }
      >
        {task.tokens ? (
          <span className="text-muted-foreground">
            {t("row.tokenIn")} <span className="text-foreground">{fmtTokens(task.tokens.input_tokens)}</span>
            {` / ${t("row.tokenCache")} `}
            <span className="text-foreground">{fmtTokens(task.tokens.cache_read_tokens)}</span>
            {` / ${t("row.tokenOut")} `}
            <span className="text-foreground">{fmtTokens(task.tokens.output_tokens)}</span>
          </span>
        ) : (
          "—"
        )}
      </TableCell>
      <TableCell className="sticky right-0 z-10 bg-card text-right shadow-[-1px_0_0_0_hsl(var(--border))] group-hover:bg-muted/50">
        <div className="flex items-center justify-end gap-0.5">
          <Button size="icon" variant="ghost" asChild aria-label={t("row.viewDetail")} title={t("row.viewDetail")}>
            <Link href={`/function/tasks/detail?id=${encodeURIComponent(task.id)}`}>
              <EyeIcon />
            </Link>
          </Button>
          {/* 只读化（H2）：暂停/继续/置顶/归档/删除全部移出列表页。 */}
        </div>
      </TableCell>
    </TableRow>
  );
});

// 三态图标分开写而非嵌套三元:仓库 Biome 基线禁 noNestedTernary。
function _taskControlIcon(pending: boolean, action: "pause" | "resume" | null) {
  if (pending) return <Loader2Icon className="animate-spin" />;
  if (action === "resume") return <PlayIcon />;
  return <PauseIcon />;
}

// TaskControlButton 是行内的暂停/继续开关。终态任务渲染为 disabled 而不是隐藏,
// 这样各行操作列宽度一致,按钮位置不会随状态跳动。
const ARCHIVE_PROCESSING_STATES = new Set<TaskArchiveState>([
  "archive_queued",
  "archiving",
  "restore_queued",
  "restoring",
  "delete_queued",
  "deleting",
]);
const ARCHIVE_FAILED_STATES = new Set<TaskArchiveState>(["archive_failed", "restore_failed", "delete_failed"]);

function archiveStateLabel(t: Translator, state: TaskArchiveState): string {
  return t(`archive.state.${state}`);
}

function ArchiveStateBadge({ state }: { state: TaskArchiveState }) {
  const t = useTranslations("tasksPage");
  let variant: "default" | "secondary" | "destructive" | "outline" = "outline";
  if (state === "ready") variant = "secondary";
  if (ARCHIVE_PROCESSING_STATES.has(state)) variant = "default";
  if (ARCHIVE_FAILED_STATES.has(state)) variant = "destructive";
  return <Badge variant={variant}>{archiveStateLabel(t, state)}</Badge>;
}

function formatArchiveBytes(bytes: number): string {
  if (bytes <= 0) return "—";
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GB`;
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${bytes} B`;
}

function archiveCompressionLabel(t: Translator, archive: TaskArchive): string {
  if (archive.original_size <= 0 || archive.compressed_size <= 0) return "—";
  const saved = Math.max(0, 100 - (archive.compressed_size / archive.original_size) * 100);
  return t("archive.compression", { size: formatArchiveBytes(archive.compressed_size), percent: saved.toFixed(0) });
}

function archiveDataTotal(archive: TaskArchive): number {
  return Object.values(archive.data_counts).reduce((sum, value) => sum + (Number(value) || 0), 0);
}

function archiveDate(value?: string): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleString();
}

function ArchiveDeleteDialog({
  archives,
  onConfirm,
  trigger,
}: {
  archives: TaskArchive[];
  onConfirm: () => Promise<void>;
  trigger: React.ReactNode;
}) {
  const t = useTranslations("tasksPage");
  const [open, setOpen] = React.useState(false);
  const [pending, setPending] = React.useState(false);
  const rows = archives.reduce((sum, archive) => sum + archiveDataTotal(archive), 0);
  const bytes = archives.reduce((sum, archive) => sum + archive.compressed_size, 0);
  return (
    <AlertDialog open={open} onOpenChange={(next) => !pending && setOpen(next)}>
      <AlertDialogTrigger asChild>{trigger}</AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("archive.deleteTitle", { count: archives.length })}</AlertDialogTitle>
          <AlertDialogDescription className="[overflow-wrap:anywhere]">
            {t("archive.deleteDescription", { size: formatArchiveBytes(bytes), rows: rows.toLocaleString() })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending}>{t("archive.deleteCancel")}</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={pending}
            onClick={async (event) => {
              event.preventDefault();
              setPending(true);
              try {
                await onConfirm();
                setOpen(false);
              } finally {
                setPending(false);
              }
            }}
          >
            {pending && <Spinner data-icon="inline-start" />}
            {t("archive.deleteConfirm")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function TaskArchivesPanel({ onChanged }: { onChanged: () => void }) {
  const t = useTranslations("tasksPage");
  const [archives, setArchives] = React.useState<TaskArchive[]>([]);
  const [query, setQuery] = React.useState("");
  const [stateFilter, setStateFilter] = React.useState("all");
  const [page, setPage] = React.useState(1);
  const [pageSize, setPageSize] = React.useState(20);
  const [total, setTotal] = React.useState(0);
  const [selected, setSelected] = React.useState<Set<number>>(() => new Set());
  const [loading, setLoading] = React.useState(true);
  const pendingRestoreIDs = React.useRef(new Set<number>());

  const load = React.useCallback(async () => {
    try {
      const result = await api.taskArchives({
        page,
        size: pageSize,
        q: query.trim(),
        state: stateFilter === "all" ? undefined : stateFilter,
      });
      setArchives(result.items);
      setTotal(result.total);
      setSelected((current) => {
        const visible = new Set(result.items.map((archive) => archive.id));
        return new Set([...current].filter((id) => visible.has(id)));
      });
      const pending = [...pendingRestoreIDs.current];
      if (pending.length > 0) {
        const states = await Promise.allSettled(pending.map((id) => api.taskArchive(id)));
        let restored = false;
        states.forEach((state, index) => {
          // 恢复完成后归档记录消失，返回 404；消息文本会随展示语言变化。
          if (state.status !== "rejected" || !(state.reason instanceof ApiError)) return;
          if (state.reason.status !== 404) return;
          pendingRestoreIDs.current.delete(pending[index]);
          restored = true;
        });
        if (restored) onChanged();
      }
    } catch (error) {
      toast.error(t("archive.loadFailed", { msg: (error as Error).message }));
    } finally {
      setLoading(false);
    }
  }, [onChanged, page, pageSize, query, stateFilter, t]);

  React.useEffect(() => {
    void load();
    const timer = setInterval(() => void load(), 2_000);
    return () => clearInterval(timer);
  }, [load]);

  const selectable = React.useMemo(
    () => archives.filter((archive) => !ARCHIVE_PROCESSING_STATES.has(archive.state)),
    [archives],
  );
  const selectedArchives = React.useMemo(
    () => archives.filter((archive) => selected.has(archive.id)),
    [archives, selected],
  );
  const restorable = selectedArchives.filter(
    (archive) => archive.state === "ready" || archive.state === "restore_failed",
  );
  const deletable = selectedArchives.filter(
    (archive) => archive.state === "ready" || archive.state === "delete_failed",
  );
  const selectedAll = selectable.length > 0 && selectable.every((archive) => selected.has(archive.id));
  const selectedSome = selectable.some((archive) => selected.has(archive.id));

  const afterAction = React.useCallback(() => {
    setSelected(new Set());
    void load();
    onChanged();
  }, [load, onChanged]);

  async function restoreMany(items: TaskArchive[]) {
    try {
      const result =
        items.length === 1
          ? {
              items: [
                {
                  id: String(items[0].id),
                  ok: true,
                  queued: true,
                  archive_id: (await api.restoreTaskArchive(items[0].id)).id,
                },
              ],
            }
          : await api.restoreTaskArchives(items.map((archive) => archive.id));
      const succeeded = result.items.filter((item) => item.ok).length;
      const failed = result.items.length - succeeded;
      for (const item of result.items) {
        const archiveID = Number(item.archive_id ?? item.id);
        if (item.ok && Number.isSafeInteger(archiveID) && archiveID > 0) pendingRestoreIDs.current.add(archiveID);
      }
      if (succeeded > 0) toast.success(t("archive.restoreQueued", { count: succeeded }));
      if (failed > 0) toast.error(t("archive.restoreRejected", { count: failed }));
      afterAction();
    } catch (error) {
      toast.error(t("archive.restoreFailed", { msg: (error as Error).message }));
    }
  }

  async function deleteMany(items: TaskArchive[]) {
    try {
      const result =
        items.length === 1
          ? {
              items: [
                {
                  id: String(items[0].id),
                  ok: true,
                  queued: true,
                  archive_id: (await api.deleteTaskArchive(items[0].id)).id,
                },
              ],
            }
          : await api.deleteTaskArchives(items.map((archive) => archive.id));
      const succeeded = result.items.filter((item) => item.ok).length;
      const failed = result.items.length - succeeded;
      if (succeeded > 0) toast.success(t("archive.deleteQueued", { count: succeeded }));
      if (failed > 0) toast.error(t("archive.deleteRejected", { count: failed }));
      afterAction();
    } catch (error) {
      toast.error(t("archive.permDeleteFailed", { msg: (error as Error).message }));
      throw error;
    }
  }

  async function retry(archive: TaskArchive) {
    try {
      if (archive.state === "archive_failed") await api.archiveTask(String(archive.task_id));
      if (archive.state === "restore_failed") await api.restoreTaskArchive(archive.id);
      if (archive.state === "delete_failed") await api.deleteTaskArchive(archive.id);
      toast.success(t("archive.retryQueued"));
      afterAction();
    } catch (error) {
      toast.error(t("archive.retryFailed", { msg: (error as Error).message }));
    }
  }

  return (
    <PaperSheet
      api={[
        "taskArchives",
        "taskArchive",
        "archiveTask",
        "deleteTaskArchive",
        "deleteTaskArchives",
        "restoreTaskArchive",
        "restoreTaskArchives",
      ]}
    >
      <div className="flex flex-col gap-4 px-0 pt-4">
        <div className="flex flex-wrap items-center gap-2 px-4 lg:px-6">
          <div className="relative w-full sm:max-w-xs">
            <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(event) => {
                setQuery(event.target.value);
                setPage(1);
              }}
              placeholder={t("archive.searchPlaceholder")}
              className="pl-8"
            />
          </div>
          <Select
            value={stateFilter}
            onValueChange={(value) => {
              setStateFilter(value);
              setPage(1);
            }}
          >
            <SelectTrigger className="w-36">
              <SelectValue placeholder={t("archive.statePlaceholder")} />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                <SelectItem value="all">{t("archive.allStates")}</SelectItem>
                <SelectItem value="archive_queued">{t("archive.state.archive_queued")}</SelectItem>
                <SelectItem value="archiving">{t("archive.state.archiving")}</SelectItem>
                <SelectItem value="ready">{t("archive.state.ready")}</SelectItem>
                <SelectItem value="restore_queued">{t("archive.state.restore_queued")}</SelectItem>
                <SelectItem value="restoring">{t("archive.state.restoring")}</SelectItem>
                <SelectItem value="delete_queued">{t("archive.state.delete_queued")}</SelectItem>
                <SelectItem value="deleting">{t("archive.state.deleting")}</SelectItem>
                <SelectItem value="archive_failed">{t("archive.state.archive_failed")}</SelectItem>
                <SelectItem value="restore_failed">{t("archive.state.restore_failed")}</SelectItem>
                <SelectItem value="delete_failed">{t("archive.state.delete_failed")}</SelectItem>
              </SelectGroup>
            </SelectContent>
          </Select>
          <span className="text-muted-foreground text-xs tabular-nums">{t("archive.total", { count: total })}</span>
          {selectedArchives.length > 0 && (
            <>
              <span className="text-xs tabular-nums">{t("archive.selected", { count: selectedArchives.length })}</span>
              {restorable.length > 0 && (
                <Button size="sm" variant="outline" onClick={() => void restoreMany(restorable)}>
                  <Undo2Icon data-icon="inline-start" />
                  {t("archive.restore", { count: restorable.length })}
                </Button>
              )}
              {deletable.length > 0 && (
                <ArchiveDeleteDialog
                  archives={deletable}
                  onConfirm={() => deleteMany(deletable)}
                  trigger={
                    <Button size="sm" variant="destructive">
                      <Trash2Icon data-icon="inline-start" />
                      {t("archive.deleteSelected", { count: deletable.length })}
                    </Button>
                  }
                />
              )}
            </>
          )}
        </div>
        {loading ? (
          <div className="flex justify-center py-20">
            <Spinner />
          </div>
        ) : archives.length === 0 ? (
          <div className="mx-4 rounded-lg border border-dashed lg:mx-6">
            <EmptyState
              illustration="flag"
              title={t("archive.emptyTitle")}
              description={t("archive.emptyDescription")}
            />
          </div>
        ) : (
          <Table className="**:data-[slot='table-cell']:px-4 **:data-[slot='table-head']:px-4">
            <TableHeader className="[&_tr]:border-t">
              <TableRow>
                <TableHead className="w-10">
                  <Checkbox
                    checked={selectedAll || (selectedSome ? "indeterminate" : false)}
                    onCheckedChange={(checked) =>
                      setSelected(checked === true ? new Set(selectable.map((archive) => archive.id)) : new Set())
                    }
                    aria-label={t("archive.selectPageAria")}
                  />
                </TableHead>
                <TableHead>{t("archive.colTask")}</TableHead>
                <TableHead>{t("archive.colOriginalStatus")}</TableHead>
                <TableHead>{t("archive.colCategory")}</TableHead>
                <TableHead>{t("archive.colArchivedAt")}</TableHead>
                <TableHead>{t("archive.colCompressedSize")}</TableHead>
                <TableHead>{t("archive.colDataCount")}</TableHead>
                <TableHead className="min-w-44">{t("archive.colProcessingState")}</TableHead>
                <TableHead className="text-right">{t("archive.colActions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {archives.map((archive) => {
                const processing = ARCHIVE_PROCESSING_STATES.has(archive.state);
                const canRestore = archive.state === "ready" || archive.state === "restore_failed";
                const canDelete = archive.state === "ready" || archive.state === "delete_failed";
                return (
                  <TableRow key={archive.id} data-state={selected.has(archive.id) ? "selected" : undefined}>
                    <TableCell>
                      <Checkbox
                        checked={selected.has(archive.id)}
                        disabled={processing}
                        onCheckedChange={(checked) =>
                          setSelected((current) => {
                            const next = new Set(current);
                            if (checked === true) next.add(archive.id);
                            else next.delete(archive.id);
                            return next;
                          })
                        }
                        aria-label={t("archive.selectRowAria", { id: archive.task_id })}
                      />
                    </TableCell>
                    <TableCell className="max-w-sm">
                      <div className="flex min-w-0 flex-col gap-0.5">
                        <span className="truncate font-medium">
                          {archive.task_name || archive.task_description || t("taskRef", { id: archive.task_id })}
                        </span>
                        <span className="truncate text-muted-foreground text-xs">
                          #{archive.task_id} / {archive.task_description}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell>
                      <StatusBadge domain="task" value={archive.original_status} dot />
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {archive.category_name || t("archive.uncategorized")}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground text-xs">
                      {archiveDate(archive.archived_at || archive.requested_at)}
                    </TableCell>
                    <TableCell
                      className="whitespace-nowrap text-xs"
                      title={t("archive.originalSizeTitle", { size: formatArchiveBytes(archive.original_size) })}
                    >
                      {archiveCompressionLabel(t, archive)}
                    </TableCell>
                    <TableCell className="text-xs tabular-nums">{archiveDataTotal(archive).toLocaleString()}</TableCell>
                    <TableCell>
                      <div className="flex min-w-0 flex-col gap-1.5">
                        <ArchiveStateBadge state={archive.state} />
                        {processing && <Progress value={archive.progress} />}
                        {archive.error && (
                          <p className="text-destructive text-xs [overflow-wrap:anywhere]">{archive.error}</p>
                        )}
                        {[...new Set(archive.warnings ?? [])].map((warning) => (
                          <p
                            key={warning}
                            className="text-amber-700 text-xs [overflow-wrap:anywhere] dark:text-amber-400"
                          >
                            {warning}
                          </p>
                        ))}
                        <span className="text-muted-foreground text-xs">
                          {archive.phase} / {archive.progress}%
                        </span>
                      </div>
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-0.5">
                        {ARCHIVE_FAILED_STATES.has(archive.state) && (
                          <Button
                            size="icon"
                            variant="ghost"
                            onClick={() => void retry(archive)}
                            aria-label={t("archive.retryAria")}
                            title={t("archive.retryTitle")}
                          >
                            <Undo2Icon />
                          </Button>
                        )}
                        {canRestore && (
                          <Button
                            size="icon"
                            variant="ghost"
                            onClick={() => void restoreMany([archive])}
                            aria-label={t("archive.restoreAria")}
                            title={t("archive.restoreTitle")}
                          >
                            <Undo2Icon />
                          </Button>
                        )}
                        {canDelete && (
                          <ArchiveDeleteDialog
                            archives={[archive]}
                            onConfirm={() => deleteMany([archive])}
                            trigger={
                              <Button
                                size="icon"
                                variant="ghost"
                                aria-label={t("archive.permDeleteAria")}
                                title={t("archive.permDeleteTitle")}
                              >
                                <Trash2Icon />
                              </Button>
                            }
                          />
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        )}
        <TablePagination
          page={page}
          pageSize={pageSize}
          total={total}
          onPageChange={setPage}
          onPageSizeChange={(size) => {
            setPageSize(size);
            setPage(1);
          }}
        />
      </div>
    </PaperSheet>
  );
}

const _emptyDeleteOptions = (): DeleteTaskOptions => ({
  delete_assets: false,
  delete_traffic: false,
  delete_files: false,
  delete_findings: false,
  delete_llm_records: false,
});

const _deleteOptionKeys: (keyof DeleteTaskOptions)[] = [
  "delete_assets",
  "delete_traffic",
  "delete_files",
  "delete_findings",
  "delete_llm_records",
];

// DeleteOptionFields renders the「同时清理关联数据」checkbox block shared by the
// single-task and bulk delete dialogs. idPrefix keeps the label/input ids unique when
// several dialogs live in the same table.
// BulkDeleteTasksDialog deletes every checked task with one shared set of cleanup options.
// The backend has no batch endpoint, so deletion runs one task at a time (see deleteTasks)
// and the button shows live progress.
// MoveTasksCategoryDialog confirms a target before applying, so a mis-click on a
// large selection cannot silently re-file every task. UNCATEGORIZED_VALUE stands
// in for "no category" because Select rejects an empty string value.
// CreateTaskSheet is the 新建任务 drawer. Its form state lives HERE, not in TasksPage: with
// 描述/目标 held by the page component every keystroke re-rendered the whole task table
// behind the drawer (plus its sticky column and 20 AlertDialog trees), which showed up as
// input lag. Now typing only re-renders the drawer.
// CategoryPicker 是新建任务里的单选分类选择器：可搜索已有分类；输入库里没有的名称后
// 回车（或点下拉里的「创建」）即时新建分类并选中，选中项以可移除的 tag 展示。分类是
// 全局资源，这里即时创建与「分类管理」里手动新建等价。只允许一个分类。
// IP/CIDR/ICP stay as identifiers; domain/keyword come from the translation catalog.
const COMPANY_SCOPE_LABELS: Record<string, string> = {
  ip: "IP",
  cidr: "CIDR",
  icp: "ICP",
};

function companyScopeLabel(t: Translator, kind: string): string {
  if (kind === "domain") return t("companyPicker.scopeDomain");
  if (kind === "keyword") return t("companyPicker.scopeKeyword");
  return COMPANY_SCOPE_LABELS[kind] ?? kind;
}

function _companyScopeSummary(t: Translator, company: Company): string {
  const rows = company.scope ?? [];
  if (rows.length === 0) return t("companyPicker.scopeNone");
  const preview = rows.slice(0, 3).map((row) => {
    const value = row.raw || row.value || row.domain || row.net || "";
    return t("companyPicker.scopeEntry", { label: companyScopeLabel(t, row.kind), value });
  });
  const base = preview.join(" / ");
  if (rows.length > preview.length) {
    return `${base} / ${t("companyPicker.scopeMore", { count: rows.length - preview.length })}`;
  }
  return base;
}
