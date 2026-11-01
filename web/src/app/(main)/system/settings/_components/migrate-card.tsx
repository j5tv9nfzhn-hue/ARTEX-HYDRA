"use client";

import * as React from "react";

import {
  CheckCircle2Icon,
  DatabaseZapIcon,
  FolderSearchIcon,
  TriangleAlertIcon,
} from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Sheet, SheetHead } from "@/components/editorial-sheet";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import type { MigrateCandidate, MigrateResult } from "@/lib/types";

/**
 * 数据迁移卡片（WEB 版 ⇄ 桌面端共用同一份前端）。
 *
 * 两件事：体检一个旧安装目录（库多大、多少行、有没有密钥、旧程序是否还在跑），
 * 然后把它导进来。两种语义：
 *   - 合并（默认）：保留当前库，把旧库的行按表并进来，主键冲突的行跳过；
 *   - 替换：先清空当前库每张表再灌旧库——动手前后端会先 VACUUM INTO 备份。
 *
 * 导入是同步 HTTP 调用（后端一次跑完才返回），所以这里只有"正在导入"的等待态，
 * 不做逐步进度条：29MB 的库通常十几秒，杀软扫描下可能几分钟。
 */

/** 字节数 → 人类可读大小。 */
function humanSize(n?: number): string {
  if (!n || n <= 0) return "—";
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

export function MigrateCard() {
  const t = useTranslations("settingsPage");

  const [path, setPath] = React.useState("");
  const [candidates, setCandidates] = React.useState<MigrateCandidate[]>([]);
  const [selected, setSelected] = React.useState<MigrateCandidate | null>(null);
  const [scanning, setScanning] = React.useState(false);
  const [checking, setChecking] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [mode, setMode] = React.useState<"merge" | "replace">("merge");
  const [confirm, setConfirm] = React.useState("");
  const [result, setResult] = React.useState<MigrateResult | null>(null);

  /** 目录形态的三种说法；放在组件里而不是顶层，t 的命名空间才对得上。 */
  // 迁移只区分「本仓库的 WEB 版目录」与「认不出来的目录」。
  const kindText = (kind: MigrateCandidate["kind"]) =>
    kind === "web" ? t("migrate.kindWeb") : t("migrate.kindUnknown");

  const scan = React.useCallback(async () => {
    setScanning(true);
    try {
      const res = await api.migrateScan();
      setCandidates(res.candidates ?? []);
      if (!res.candidates?.length) toast.message(t("migrate.toast.none"));
    } catch (err) {
      toast.error(t("migrate.toast.failed", { error: String(err) }));
    } finally {
      setScanning(false);
    }
  }, [t]);

  const inspect = React.useCallback(async () => {
    const p = path.trim();
    if (!p) {
      toast.error(t("migrate.requiresPath"));
      return;
    }
    setChecking(true);
    try {
      const c = await api.migrateInspect(p);
      setSelected(c);
      setResult(null);
    } catch (err) {
      toast.error(t("migrate.toast.failed", { error: String(err) }));
    } finally {
      setChecking(false);
    }
  }, [path, t]);

  const run = React.useCallback(async () => {
    if (!selected) {
      toast.error(t("migrate.requiresPath"));
      return;
    }
    setBusy(true);
    setResult(null);
    try {
      const res = await api.migrateImport(
        selected.path,
        mode,
        mode === "replace" ? confirm.trim() : undefined,
      );
      setResult(res);
      toast.success(t("migrate.toast.done", { rows: res.copied_rows }));
    } catch (err) {
      toast.error(t("migrate.toast.failed", { error: String(err) }));
    } finally {
      setBusy(false);
    }
  }, [confirm, mode, selected, t]);

  return (
    <Sheet api={["migrateImport", "migrateInspect", "migrateScan"]} className="mb-4 break-inside-avoid md:mb-6">
      <SheetHead icon={DatabaseZapIcon} title={t("migrate.title")} />
      <div className="flex flex-col gap-4 px-4 py-3">
        <p className="text-muted-foreground text-sm">{t("migrate.desc")}</p>
        {/* 手填路径：覆盖"我知道旧版在哪个盘"的场景 */}
        <div className="flex flex-col gap-2">
          <Label htmlFor="migrate-path">{t("migrate.pathLabel")}</Label>
          <div className="flex gap-2">
            <Input
              id="migrate-path"
              value={path}
              placeholder={t("migrate.pathPlaceholder")}
              onChange={(e) => setPath(e.target.value)}
              spellCheck={false}
            />
            <Button variant="outline" onClick={inspect} disabled={checking || busy}>
              <FolderSearchIcon className="size-4" />
              {checking ? t("migrate.checking") : t("migrate.inspect")}
            </Button>
          </div>
        </div>

        {/* 自动扫描：桌面/下载/文档 + 当前安装目录的同级 */}
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="secondary" size="sm" onClick={scan} disabled={scanning || busy}>
            {scanning ? t("migrate.scanning") : t("migrate.scan")}
          </Button>
          <span className="text-muted-foreground text-xs">{t("migrate.scanHint")}</span>
        </div>

        {candidates.length > 0 ? (
          <div className="flex flex-col gap-2">
            <div className="text-muted-foreground text-xs">{t("migrate.candidates")}</div>
            {candidates.map((c) => (
              <button
                key={c.path}
                type="button"
                onClick={() => {
                  setSelected(c);
                  setPath(c.path);
                  setResult(null);
                }}
                className={`rounded-md border p-2 text-left text-xs transition-colors hover:bg-accent ${
                  selected?.path === c.path ? "border-primary bg-accent" : ""
                }`}
              >
                <div className="flex flex-wrap items-center gap-1.5">
                  <Badge variant="outline">{kindText(c.kind)}</Badge>
                  {c.version ? <Badge variant="secondary">v{c.version}</Badge> : null}
                  <span className="font-mono">{humanSize(c.db_size_bytes)}</span>
                  <span>
                    {t("migrate.rows")}：{c.total_rows.toLocaleString()}
                  </span>
                  {c.is_current ? <Badge>{t("migrate.current")}</Badge> : null}
                  {c.running ? <Badge variant="destructive">{t("migrate.running")}</Badge> : null}
                </div>
                <div className="text-muted-foreground mt-1 break-all font-mono">{c.path}</div>
              </button>
            ))}
          </div>
        ) : null}

        {selected ? (
          <div className="rounded-md border p-3">
            <div className="flex flex-wrap items-center gap-2 text-xs">
              <Badge variant="outline">{kindText(selected.kind)}</Badge>
              <span className="break-all font-mono">{selected.path}</span>
            </div>
            <ul className="text-muted-foreground mt-2 space-y-0.5 text-xs">
              <li>
                {t("migrate.dbLine", {
                  size: humanSize(selected.db_size_bytes),
                  rows: selected.total_rows.toLocaleString(),
                  tables: selected.tables.length,
                })}
              </li>
              <li>
                {t("migrate.filesLine", {
                  data: selected.data_entries,
                  skills: selected.skill_entries,
                })}
              </li>
              <li>
                {t("migrate.keysLine", {
                  cred: selected.has_credential_key ? t("migrate.yes") : t("migrate.no"),
                  jwt: selected.has_jwt_key ? t("migrate.yes") : t("migrate.no"),
                })}
              </li>
              {selected.note ? <li>{selected.note}</li> : null}
              {selected.is_current ? (
                <li className="text-destructive">{t("migrate.sameDir")}</li>
              ) : null}
              {selected.running ? (
                <li className="text-destructive">{t("migrate.runningHint")}</li>
              ) : null}
            </ul>

            {/* 模式：合并默认，替换需要手打确认词 */}
            <div className="mt-3 flex flex-col gap-2">
              <Label>{t("migrate.modeLabel")}</Label>
              <div className="flex flex-wrap gap-2">
                <Button
                  size="sm"
                  variant={mode === "merge" ? "default" : "outline"}
                  onClick={() => setMode("merge")}
                  disabled={busy}
                >
                  {t("migrate.modeMerge")}
                </Button>
                <Button
                  size="sm"
                  variant={mode === "replace" ? "default" : "outline"}
                  onClick={() => setMode("replace")}
                  disabled={busy}
                >
                  {t("migrate.modeReplace")}
                </Button>
              </div>
              <p className="text-muted-foreground text-xs">
                {mode === "merge" ? t("migrate.modeMergeHint") : t("migrate.modeReplaceHint")}
              </p>
              {mode === "replace" ? (
                <Input
                  value={confirm}
                  placeholder={t("migrate.confirmPlaceholder")}
                  onChange={(e) => setConfirm(e.target.value)}
                  spellCheck={false}
                />
              ) : null}
              <div>
                <Button
                  onClick={run}
                  disabled={
                    busy ||
                    selected.is_current ||
                    selected.running ||
                    (mode === "replace" && confirm.trim() !== "REPLACE")
                  }
                >
                  {busy ? t("migrate.starting") : t("migrate.start")}
                </Button>
              </div>
            </div>
          </div>
        ) : null}

        {result ? (
          <div className="rounded-md border p-3 text-xs">
            <div className="flex items-center gap-2 font-medium">
              <CheckCircle2Icon className="size-4 text-emerald-500" />
              {t("migrate.resultTitle")}
            </div>
            <ul className="text-muted-foreground mt-2 space-y-0.5">
              <li>
                {t("migrate.resultRows", {
                  copied: result.copied_rows.toLocaleString(),
                  skipped: result.skipped_rows.toLocaleString(),
                })}
              </li>
              <li>
                {t("migrate.resultFiles", {
                  data: result.data_files,
                  skills: result.skill_files,
                })}
              </li>
              <li className="break-all font-mono">
                {t("migrate.backup")}：{result.backup_path}
              </li>
            </ul>
            {result.tables.length > 0 ? (
              <table className="mt-2 w-full">
                <thead>
                  <tr className="text-muted-foreground text-left">
                    <th className="font-normal">{t("migrate.tableName")}</th>
                    <th className="text-right font-normal">{t("migrate.tableCopied")}</th>
                    <th className="text-right font-normal">{t("migrate.tableSkipped")}</th>
                  </tr>
                </thead>
                <tbody className="font-mono">
                  {result.tables.slice(0, 12).map((row) => (
                    <tr key={row.name}>
                      <td className="break-all">{row.name}</td>
                      <td className="text-right">{row.copied.toLocaleString()}</td>
                      <td className="text-right">{row.skipped.toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            ) : null}
            {result.warnings.length > 0 ? (
              <div className="mt-2 flex gap-1.5 text-amber-600">
                <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
                <ul className="space-y-0.5">
                  {result.warnings.map((w) => (
                    <li key={w}>{w}</li>
                  ))}
                </ul>
              </div>
            ) : null}
            {result.restart_required ? (
              <p className="mt-2 font-medium text-destructive">{t("migrate.restart")}</p>
            ) : null}
          </div>
        ) : null}
      </div>
    </Sheet>
  );
}
