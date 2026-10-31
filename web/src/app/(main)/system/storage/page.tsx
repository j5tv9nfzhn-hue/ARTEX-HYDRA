"use client";

import * as React from "react";

import Link from "next/link";

import { AlertTriangleIcon, DatabaseIcon, RefreshCwIcon, ShieldCheckIcon, Trash2Icon } from "lucide-react";
import { Masthead } from "@/components/editorial";
import { Sheet, SheetHead } from "@/components/editorial-sheet";
import { useTranslations } from "next-intl";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { type CompactResult, type GcResult, type StorageStats, storageApi } from "@/lib/storage-api";
import type { WorkspaceListing } from "@/lib/types";

// 系统 → 存储与清理。
//
// 三块内容对应三个真实存在的问题，彼此不重叠：
//   数据库：库文件和空闲页（VACUUM 是唯一能立刻把文件缩回去的动作）；
//   内存：Go 运行时 + 进程内缓存（只能观察 + 主动归还，不存在「清空缓存」魔法）；
//   记忆库：探索图与活动流的体积（可清理的历史垃圾），漏洞/资产不在这里删。
//
// 删除动作一律人工触发（预览 → 确认）。唯一的例外是「活动流自动保留」：默认关闭，
// 用户显式设了天数后由后台按批兜底清理（server/activity_retention.go）。

const TABLE_PREVIEW = 15;

type ConfirmState = {
  title: string;
  body: string;
  danger?: boolean;
  onConfirm: () => void;
};

function fmtBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  let digits = 2;
  if (v >= 100) digits = 0;
  else if (v >= 10) digits = 1;
  return `${v.toFixed(digits)} ${units[i]}`;
}

function fmtNum(n: number): string {
  return Number.isFinite(n) ? n.toLocaleString("zh-CN") : "-";
}

function Stat({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-3 border-b py-1.5 last:border-b-0">
      <span className="text-muted-foreground text-xs">{label}</span>
      <span className="font-medium text-sm tabular-nums">{value}</span>
    </div>
  );
}

export default function StoragePage() {
  const t = useTranslations("storagePage");
  const [stats, setStats] = React.useState<StorageStats | null>(null);
  const [ws, setWs] = React.useState<WorkspaceListing | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [busy, setBusy] = React.useState<string | null>(null);
  const [error, setError] = React.useState<string | null>(null);
  const [notice, setNotice] = React.useState<string | null>(null);
  const [confirm, setConfirm] = React.useState<ConfirmState | null>(null);
  const [showAll, setShowAll] = React.useState(false);
  const [days, setDays] = React.useState(30);
  // null = 跟着服务端值走；用户一改就变成草稿，保存成功后回到 null。
  const [keepDays, setKeepDays] = React.useState<number | null>(null);
  const [keepMaxRows, setKeepMaxRows] = React.useState<number | null>(null);

  // biome-ignore lint/correctness/useExhaustiveDependencies: 依赖里刻意不放 t——
  // useTranslations 每次渲染都可能返回新函数，放进 useCallback 会让 load 反复重建 →
  // effect 无限重取。
  const load = React.useCallback(async () => {
    setError(null);
    const [nextStats, nextWs] = await Promise.allSettled([storageApi.stats(), api.workspaceList("")]);
    if (nextStats.status === "fulfilled") setStats(nextStats.value);
    else setError(nextStats.reason instanceof Error ? nextStats.reason.message : t("loadFailed"));
    setWs(nextWs.status === "fulfilled" ? nextWs.value : null);
    setLoading(false);
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  const run = React.useCallback(
    async (key: string, fn: () => Promise<string | null>) => {
      setBusy(key);
      setError(null);
      setNotice(null);
      try {
        const msg = await fn();
        await load();
        if (msg) setNotice(msg);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        setBusy(null);
      }
    },
    [load],
  );

  const db = stats?.db;
  const mem = stats?.mem;
  const mdb = stats?.memory_db;
  const wq = stats?.memory_db?.write_queue;
  const wal = stats?.memory_db?.wal;
  const keep = stats?.activity_retention;
  const savedKeepDays = keep?.days ?? 0;
  const savedMaxRows = keep?.max_rows ?? 0;
  const effectiveKeepDays = keepDays ?? savedKeepDays;
  const effectiveMaxRows = keepMaxRows ?? savedMaxRows;
  const retentionDirty =
    (keepDays !== null && keepDays !== savedKeepDays) || (keepMaxRows !== null && keepMaxRows !== savedMaxRows);
  const freeRatio = db && db.page_count > 0 ? db.freelist / db.page_count : 0;
  const tables = db?.tables ?? [];
  const visibleTables = showAll ? tables : tables.slice(0, TABLE_PREVIEW);
  const topDirs = [...(ws?.entries ?? [])]
    .filter((e) => e.dir)
    .sort((a, b) => b.size - a.size)
    .slice(0, 6);
  const wsTotal = ws?.total_size ?? 0;
  const wsFiles = ws?.total_files ?? 0;

  const tablesBody = (() => {
    if (loading) return <p className="text-muted-foreground text-sm">{t("loading")}</p>;
    if (tables.length === 0) return <p className="text-muted-foreground text-sm">{t("tablesEmpty")}</p>;
    return (
      <>
        <div className="grid gap-x-6 sm:grid-cols-2 lg:grid-cols-3">
          {visibleTables.map((row) => (
            <Stat key={row.name} label={row.name} value={fmtNum(row.rows)} />
          ))}
        </div>
        {tables.length > TABLE_PREVIEW ? (
          <Button variant="ghost" size="sm" className="mt-3" onClick={() => setShowAll((v) => !v)}>
            {showAll ? t("showLess") : t("showAll", { count: tables.length })}
          </Button>
        ) : null}
      </>
    );
  })();

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <Masthead
          title={t("title")}
          subtitle={t("subtitle")}
          meta={<Badge variant="secondary">{t("badgeManual")}</Badge>}
          titleClassName="text-xl"
        />
        <Button variant="outline" disabled={loading || busy !== null} onClick={() => void load()}>
          <RefreshCwIcon className="size-4" />
          {t("refresh")}
        </Button>
      </div>

      <Alert>
        <ShieldCheckIcon />
        <AlertTitle>{t("boundaryTitle")}</AlertTitle>
        <AlertDescription>
          <p>{t("boundary")}</p>
          <p>{t("boundary2")}</p>
        </AlertDescription>
      </Alert>

      {error ? (
        <Alert variant="destructive">
          <AlertTriangleIcon />
          <AlertTitle>{t("errorTitle")}</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}

      {notice ? (
        <Alert>
          <DatabaseIcon />
          <AlertDescription>{notice}</AlertDescription>
        </Alert>
      ) : null}

      <div className="grid gap-4 lg:grid-cols-3">
        {/* 数据库 */}
        <Sheet>
          <SheetHead icon={DatabaseIcon} title={t("dbTitle")} />
          <div className="space-y-3 px-4 py-3">
            <p className="text-muted-foreground text-sm">{t("dbDesc")}</p>
            {loading || !db ? (
              <p className="text-muted-foreground text-sm">{t("loading")}</p>
            ) : (
              <>
                <div>
                  <Stat label={t("dbSize")} value={fmtBytes(db.size)} />
                  <Stat label={t("dbWal")} value={fmtBytes(db.wal_size)} />
                  <Stat label={t("dbShm")} value={fmtBytes(db.shm_size)} />
                  <Stat label={t("dbPages")} value={fmtNum(db.page_count)} />
                  <Stat label={t("dbFreePages")} value={fmtNum(db.freelist)} />
                  <Stat label={t("dbFreeRatio")} value={`${(freeRatio * 100).toFixed(1)}%`} />
                  <Stat label={t("dbAutoVacuum")} value={db.auto_vacuum} />
                  <Stat label={t("dbJournal")} value={db.journal_mode || "-"} />
                </div>
                <p className="break-all text-xs text-muted-foreground">{db.path}</p>
                <Button
                  variant="outline"
                  className="w-full"
                  disabled={busy !== null}
                  onClick={() =>
                    setConfirm({
                      title: t("compactConfirmTitle"),
                      body: t("compactConfirmDesc"),
                      onConfirm: () =>
                        void run("compact", async () => {
                          const r: CompactResult = await storageApi.compact();
                          return t("dbCompactDone", {
                            before: fmtBytes(r.before),
                            after: fmtBytes(r.after),
                            freed: fmtBytes(r.freed),
                            ms: r.duration_ms,
                          });
                        }),
                    })
                  }
                >
                  <Trash2Icon className="size-4" />
                  {busy === "compact" ? t("running") : t("dbCompact")}
                </Button>
                <p className="text-xs text-muted-foreground">{t("dbCompactHint")}</p>
              </>
            )}
          </div>
        </Sheet>

        {/* 内存 */}
        <Sheet>
          <SheetHead title={t("memTitle")} />
          <div className="space-y-3 px-4 py-3">
            <p className="text-muted-foreground text-sm">{t("memDesc")}</p>
            {loading || !mem ? (
              <p className="text-muted-foreground text-sm">{t("loading")}</p>
            ) : (
              <>
                <div>
                  <Stat label={t("heapAlloc")} value={fmtBytes(mem.heap_alloc)} />
                  <Stat label={t("heapSys")} value={fmtBytes(mem.heap_sys)} />
                  <Stat label={t("stackInuse")} value={fmtBytes(mem.stack_inuse)} />
                  <Stat label={t("sysTotal")} value={fmtBytes(mem.sys)} />
                  <Stat label={t("gcCount")} value={fmtNum(mem.num_gc)} />
                  <Stat label={t("goroutines")} value={fmtNum(mem.goroutines)} />
                  <Stat
                    label={t("cacheNotify")}
                    value={`${fmtNum(mem.notify_ring)} / ${fmtNum(mem.notify_ring_cap)}`}
                  />
                  <Stat label={t("cacheTasks")} value={fmtNum(mem.task_handles)} />
                  <Stat label={t("cacheTransports")} value={fmtNum(mem.http_transports)} />
                </div>
                <Button
                  variant="outline"
                  className="w-full"
                  disabled={busy !== null}
                  onClick={() =>
                    void run("gc", async () => {
                      const r: GcResult = await storageApi.gc();
                      return t("gcDone", { before: fmtBytes(r.heap_before), after: fmtBytes(r.heap_after) });
                    })
                  }
                >
                  {busy === "gc" ? t("running") : t("gcNow")}
                </Button>
                <Button
                  variant="ghost"
                  className="w-full"
                  disabled={busy !== null}
                  onClick={() =>
                    setConfirm({
                      title: t("notifyConfirmTitle"),
                      body: t("notifyConfirmDesc"),
                      onConfirm: () =>
                        void run("notify", async () => {
                          const r = await storageApi.clearNotify();
                          return t("notifyDone", { count: r.cleared });
                        }),
                    })
                  }
                >
                  {busy === "notify" ? t("running") : t("clearNotify")}
                </Button>
              </>
            )}
          </div>
        </Sheet>

        {/* 记忆库 */}
        <Sheet>
          <SheetHead title={t("mdbTitle")} />
          <div className="space-y-3 px-4 py-3">
            <p className="text-muted-foreground text-sm">{t("mdbDesc")}</p>
            {loading || !mdb ? (
              <p className="text-muted-foreground text-sm">{t("loading")}</p>
            ) : (
              <>
                <div>
                  <Stat label={t("mdbFacts")} value={fmtNum(mdb.facts)} />
                  <Stat label={t("mdbEdges")} value={fmtNum(mdb.edges)} />
                  <Stat label={t("mdbActivity")} value={fmtNum(mdb.activity)} />
                  <Stat label={t("mdbFindings")} value={fmtNum(mdb.findings)} />
                  <Stat label={t("mdbAssets")} value={fmtNum(mdb.assets)} />
                  <Stat label={t("mdbLinks")} value={fmtNum(mdb.links)} />
                  <Stat
                    label={t("orphanLabel")}
                    value={`${fmtNum(mdb.orphan_facts)} / ${fmtNum(mdb.orphan_edges)} / ${fmtNum(mdb.orphan_activity)}`}
                  />
                </div>
                <Button
                  variant="outline"
                  className="w-full"
                  disabled={busy !== null}
                  onClick={() =>
                    void run("orphanPreview", async () => {
                      const p = await storageApi.orphanPreview();
                      setConfirm({
                        title: t("orphanConfirmTitle"),
                        body: t("orphanConfirmDesc", {
                          facts: fmtNum(p.counts.facts),
                          edges: fmtNum(p.counts.edges),
                          activity: fmtNum(p.counts.activity),
                        }),
                        danger: true,
                        onConfirm: () =>
                          void run("orphan", async () => {
                            const r = await storageApi.orphanPurge();
                            const rows = (r.rows ?? { facts: 0, edges: 0, activity: 0 }) as {
                              facts: number;
                              edges: number;
                              activity: number;
                            };
                            return t("orphanDone", {
                              facts: fmtNum(rows.facts),
                              edges: fmtNum(rows.edges),
                              activity: fmtNum(rows.activity),
                              freed: fmtBytes(r.freed ?? 0),
                            });
                          }),
                      });
                      return null;
                    })
                  }
                >
                  <Trash2Icon className="size-4" />
                  {busy === "orphanPreview" || busy === "orphan" ? t("running") : t("purgeOrphans")}
                </Button>
                <div className="flex items-end gap-2">
                  <div className="flex-1">
                    <Label htmlFor="activity-days" className="text-xs">
                      {t("activityDaysLabel")}
                    </Label>
                    <Input
                      id="activity-days"
                      type="number"
                      min={1}
                      max={3650}
                      value={days}
                      onChange={(e) => setDays(Math.max(1, Math.min(3650, Number(e.target.value) || 30)))}
                    />
                  </div>
                  <Button
                    variant="outline"
                    disabled={busy !== null}
                    onClick={() =>
                      void run("activityPreview", async () => {
                        const p = await storageApi.activityPreview(days);
                        setConfirm({
                          title: t("activityConfirmTitle"),
                          body: t("activityConfirmDesc", { days, rows: fmtNum(p.rows) }),
                          danger: true,
                          onConfirm: () =>
                            void run("activity", async () => {
                              const r = await storageApi.activityPurge(days);
                              const rows = typeof r.rows === "number" ? r.rows : 0;
                              return t("activityDone", { rows: fmtNum(rows), freed: fmtBytes(r.freed ?? 0) });
                            }),
                        });
                        return null;
                      })
                    }
                  >
                    {busy === "activityPreview" || busy === "activity" ? t("running") : t("purgeActivity")}
                  </Button>
                </div>
                <p className="text-xs text-muted-foreground">{t("mdbNote")}</p>
              </>
            )}
          </div>
        </Sheet>
      </div>

      {/*
        写入管道与 WAL 并排：它们讲的是同一件事的两半——前者是"批量提交省了多少"，
        后者是"检查点跟不跟得上"。活动保留单独占一行（下面），因为它带输入框、
        操作按钮和说明文字，塞进窄列会被挤成一团。
        窄屏一律退化为单列，顺序仍是 写入 → WAL → 保留。
      */}
      <div className="grid gap-4 md:grid-cols-2">
        <Sheet>
          <SheetHead title={t("wqTitle")} />
          <div className="space-y-3 px-4 py-3">
            <p className="text-muted-foreground text-sm">{t("wqDesc")}</p>
            {loading || !wq ? (
              <p className="text-muted-foreground text-sm">{t("loading")}</p>
            ) : (
              <>
                <div className="grid gap-x-6 sm:grid-cols-2">
                  <Stat label={t("wqBatches")} value={fmtNum(wq.batches)} />
                  <Stat label={t("wqJobs")} value={fmtNum(wq.jobs)} />
                  <Stat label={t("wqAvgBatch")} value={(wq.avg_batch ?? 0).toFixed(2)} />
                  <Stat label={t("wqLastBatch")} value={fmtNum(wq.last_batch)} />
                  <Stat label={t("wqMaxBatch")} value={fmtNum(wq.max_batch)} />
                  <Stat label={t("wqWait")} value={`${(wq.avg_wait_ms ?? 0).toFixed(2)} ms`} />
                  <Stat label={t("wqRollbacks")} value={fmtNum(wq.rollbacks)} />
                  <Stat label={t("wqDepth")} value={fmtNum(wq.queue_depth)} />
                </div>
                <p className="text-xs text-muted-foreground">{t("wqNote")}</p>
              </>
            )}
          </div>
        </Sheet>

        <Sheet>
          <SheetHead title={t("walTitle")} />
          <div className="space-y-3 px-4 py-3">
            <p className="text-muted-foreground text-sm">{t("walDesc")}</p>
            {loading || !wal ? (
              <p className="text-muted-foreground text-sm">{t("loading")}</p>
            ) : (
              <>
                <div className="grid gap-x-6 sm:grid-cols-2">
                  <Stat label={t("walSize")} value={fmtBytes(wal.size_bytes)} />
                  <Stat label={t("walFrames")} value={fmtNum(wal.frames)} />
                  <Stat label={t("walBackfilled")} value={fmtNum(wal.backfilled)} />
                  {/*
                    待回填 = frames - backfilled。这是整块面板里唯一需要盯的数字：
                    它为 0 说明检查点跟得上；持续变大说明有读事务钉住了旧快照。
                    frames 在 WAL 被截断后会归零，所以不能直接拿它当积压量看。
                  */}
                  <Stat
                    label={t("walLag")}
                    value={fmtNum(Math.max(0, (wal.frames ?? 0) - (wal.backfilled ?? 0)))}
                  />
                  <Stat
                    label={t("walState")}
                    value={
                      /*
                        三态而不是两态：busy 与 healthy 是两件独立的事。
                        体积正常但被读者挡住，仍然是需要知道的状态——它意味着
                        下一次写入密集时 WAL 会开始涨。
                      */
                      wal.busy ? (
                        <Badge variant="secondary">{t("walStateBlocked")}</Badge>
                      ) : wal.healthy ? (
                        <Badge variant="outline">{t("walStateHealthy")}</Badge>
                      ) : (
                        <Badge variant="destructive">{t("walStateLagging")}</Badge>
                      )
                    }
                  />
                </div>
                <p className="text-xs text-muted-foreground">{t("walNote")}</p>
              </>
            )}
          </div>
        </Sheet>
      </div>

      <Sheet>
        <SheetHead title={t("retentionTitle")} />
        <div className="space-y-3 px-4 py-3">
          <p className="text-muted-foreground text-sm">{t("retentionDesc")}</p>
          <div className="flex items-end gap-2">
            <div className="flex-1">
              <Label htmlFor="activity-retention-days" className="text-xs">
                {t("retentionDaysLabel")}
              </Label>
              <Input
                id="activity-retention-days"
                type="number"
                min={0}
                max={3650}
                value={effectiveKeepDays}
                onChange={(e) => setKeepDays(Math.max(0, Math.min(3650, Number(e.target.value) || 0)))}
              />
            </div>
            <div className="flex-1">
              <Label htmlFor="activity-retention-rows" className="text-xs">
                {t("retentionMaxRowsLabel")}
              </Label>
              <Input
                id="activity-retention-rows"
                type="number"
                min={0}
                max={10000000}
                step={10000}
                value={effectiveMaxRows}
                onChange={(e) => setKeepMaxRows(Math.max(0, Math.min(10000000, Number(e.target.value) || 0)))}
              />
            </div>
            <Button
              variant="outline"
              disabled={busy !== null || !retentionDirty}
              onClick={() =>
                void run("retention", async () => {
                  const days = effectiveKeepDays;
                  const rows = effectiveMaxRows;
                  await storageApi.activityRetention(days, rows);
                  setKeepDays(null);
                  setKeepMaxRows(null);
                  return t("retentionSaved", { days, rows: fmtNum(rows) });
                })
              }
            >
              {busy === "retention" ? t("running") : t("retentionSave")}
            </Button>
          </div>
          <div>
            <Stat
              label={t("retentionLast")}
              value={
                keep?.last_run_unix_ms
                  ? new Date(keep.last_run_unix_ms).toLocaleString("zh-CN")
                  : t("retentionNever")
              }
            />
            <Stat label={t("retentionLastRows")} value={fmtNum(keep?.last_deleted ?? 0)} />
            <Stat label={t("retentionLastCapped")} value={fmtNum(keep?.last_capped ?? 0)} />
          </div>
          <p className="text-xs text-muted-foreground">{t("retentionNote")}</p>
        </div>
      </Sheet>

      {/* 表行数 */}
      <Sheet>
        <SheetHead title={t("tablesTitle")} />
        <div className="px-4 py-3">
          <p className="text-muted-foreground text-sm">
            {t("tablesDesc")}
            {db?.tables_partial ? ` / ${t("tablesPartial")}` : ""}
          </p>
          {tablesBody}
        </div>
      </Sheet>

      {/* 文件 */}
      <Sheet>
        <SheetHead title={t("filesTitle")} />
        <div className="space-y-3 px-4 py-3">
          <p className="text-muted-foreground text-sm">{t("filesDesc")}</p>
          {!ws ? (
            <p className="text-muted-foreground text-sm">{t("filesEmpty")}</p>
          ) : (
            <>
              <div className="flex flex-wrap items-center gap-3 text-sm">
                <span className="font-medium">{t("filesTotal", { size: fmtBytes(wsTotal) })}</span>
                <span className="text-muted-foreground">{t("filesCount", { count: wsFiles })}</span>
                <Button asChild variant="link" size="sm" className="px-0">
                  <Link href="/function/workspace">{t("filesOpen")}</Link>
                </Button>
              </div>
              <div className="grid gap-x-6 sm:grid-cols-2 lg:grid-cols-3">
                {topDirs.map((e) => (
                  <Stat key={e.path} label={e.name} value={fmtBytes(e.size)} />
                ))}
              </div>
            </>
          )}
        </div>
      </Sheet>

      <AlertDialog open={confirm !== null} onOpenChange={(open) => !open && setConfirm(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirm?.title}</AlertDialogTitle>
            <AlertDialogDescription>{confirm?.body}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <Button variant="outline" onClick={() => setConfirm(null)}>
              {t("cancel")}
            </Button>
            <Button
              variant={confirm?.danger ? "destructive" : "default"}
              onClick={() => {
                const action = confirm?.onConfirm;
                setConfirm(null);
                action?.();
              }}
            >
              {t("confirm")}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
