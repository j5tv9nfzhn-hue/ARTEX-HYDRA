"use client";

import * as React from "react";

import Link from "next/link";
import { useSearchParams } from "next/navigation";

import { ArrowLeftIcon, BookOpenIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { KpiRow, Masthead } from "@/components/editorial";
import { RecordRow, Sheet, SheetFoot, SheetHead } from "@/components/editorial-sheet";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { api } from "@/lib/api";
import type { SkillCall, SkillItem } from "@/lib/types";

/**
 * 技能详情：说明、步骤正文（SKILL.md）、文件清单与最近调用。
 *
 * 地址：/system/skills/detail?name=<技能目录名>
 *
 * 「步骤序列 / 验收标准」不是独立字段 —— 它们写在 SKILL.md 正文里，所以这里把
 * 正文原样摊开（等宽、可滚动），而不是在前端二次解析 Markdown 结构再自称是步骤。
 */
function SkillDetailInner() {
  const t = useTranslations("skillDetail");
  const searchParams = useSearchParams();
  const name = searchParams.get("name") ?? "";

  const [skill, setSkill] = React.useState<SkillItem | null>(null);
  const [files, setFiles] = React.useState<string[]>([]);
  const [calls, setCalls] = React.useState<SkillCall[]>([]);
  const [body, setBody] = React.useState("");
  const [bodyFile, setBodyFile] = React.useState("");
  const [loaded, setLoaded] = React.useState(false);

  React.useEffect(() => {
    if (!name) {
      setLoaded(true);
      return;
    }
    let alive = true;
    api
      .skills()
      .then((rows) => {
        if (alive) setSkill(rows.find((x) => x.name === name) ?? null);
      })
      .catch(() => {
        if (alive) setSkill(null);
      })
      .finally(() => {
        if (alive) setLoaded(true);
      });
    api
      .skillFiles(name)
      .then((rows) => {
        if (alive) setFiles(rows);
      })
      .catch(() => undefined);
    api
      .skillUsage(name, 20)
      .then((rows) => {
        if (alive) setCalls(rows);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [name]);

  // SKILL.md 是技能的入口文件；没有就退回第一个 .md，再没有就留空。
  React.useEffect(() => {
    if (files.length === 0) return;
    const pick = files.find((f) => /(^|\/)SKILL\.md$/i.test(f)) ?? files.find((f) => f.toLowerCase().endsWith(".md"));
    if (!pick) return;
    let alive = true;
    api
      .readSkillFile(name, pick)
      .then((text) => {
        if (alive) {
          setBody(text);
          setBodyFile(pick);
        }
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [files, name]);

  const back = (
    <Button asChild size="sm" variant="outline">
      <Link href="/system/skills">
        <ArrowLeftIcon data-icon="inline-start" />
        {t("back")}
      </Link>
    </Button>
  );

  if (!loaded) {
    return (
      <div className="flex flex-1 items-center justify-center gap-2 p-10 text-muted-foreground text-sm">
        <Spinner />
        {t("loading")}
      </div>
    );
  }

  if (!skill) {
    return (
      <div className="flex flex-1 flex-col gap-3">
        <div className="flex items-start justify-between gap-3">
          <Masthead title={t("title")} titleClassName="text-xl" />
          {back}
        </div>
        <p className="border-border/70 border-t pt-6 text-center text-muted-foreground text-sm">
          {t("notFound", { name })}
        </p>
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <Masthead
          title={skill.name}
          meta={
            <>
              <span>{skill.origin || t("originUnknown")}</span>
              <span className="tabular-nums">{t("callsMeta", { n: skill.calls })}</span>
              {skill.last_used ? (
                <>
                  <span className="tabular-nums">{skill.last_used}</span>
                </>
              ) : null}
              {skill.out_of_sync ? (
                <>
                  <span>{t("outOfSync")}</span>
                </>
              ) : null}
            </>
          }
          subtitle={skill.description}
          titleClassName="text-xl"
        />
        {back}
      </div>

      <KpiRow className="grid grid-cols-2 divide-y divide-border/60 border-border/60 border-y sm:grid-cols-3 sm:divide-x sm:divide-y-0 lg:grid-cols-5">
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.calls")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{skill.calls}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.tasks")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{skill.tasks}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.files")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{files.length}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.agents")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{skill.usage_agents.length}</span>
        </div>
        <div className="flex flex-col gap-0.5 px-3 py-2">
          <span className="text-xs tracking-[0.08em] text-muted-foreground">{t("kpi.mcps")}</span>
          <span className="font-semibold text-[22px] leading-none tabular-nums">{(skill.mcps ?? []).length}</span>
        </div>
      </KpiRow>

      <div className="grid min-w-0 gap-4 xl:grid-cols-3">
        <Sheet className="min-w-0 xl:col-span-2">
          <SheetHead
            icon={BookOpenIcon}
            title={t("body")}
            count={bodyFile || undefined}
            hint={body ? `${body.length.toLocaleString("zh-CN")} ${t("chars")}` : undefined}
          />
          <div data-editorial="body" className="min-h-0 px-4 py-3">
            {body ? (
              <pre className="max-h-[64vh] overflow-auto whitespace-pre-wrap font-mono text-xs leading-relaxed [overflow-wrap:anywhere]">
                {body}
              </pre>
            ) : (
              <p className="py-6 text-center text-muted-foreground text-xs">{t("bodyEmpty")}</p>
            )}
          </div>
          <SheetFoot left={t("bodyFoot")} right={skill.license || ""} />
        </Sheet>

        <div className="flex min-w-0 flex-col gap-4">
          <Sheet>
            <SheetHead title={t("files")} count={`${files.length}`} />
            {files.length === 0 ? (
              <div data-editorial="body" className="px-4 py-5 text-center text-muted-foreground text-xs">
                {t("filesEmpty")}
              </div>
            ) : (
              <div data-editorial="body" className="px-4 pt-1 pb-2">
                {files.map((f) => (
                  <RecordRow key={f} cols="20px minmax(0,1fr)" lead={<span className="text-xs">—</span>}>
                    <code className="block truncate font-mono text-xs">{f}</code>
                  </RecordRow>
                ))}
              </div>
            )}
          </Sheet>

          <Sheet>
            <SheetHead title={t("usage")} count={`${calls.length}`} hint={t("usageHint")} />
            {calls.length === 0 ? (
              <div data-editorial="body" className="px-4 py-5 text-center text-muted-foreground text-xs">
                {t("usageEmpty")}
              </div>
            ) : (
              <div data-editorial="body" className="px-4 pt-1 pb-2">
                {calls.map((c, i) => (
                  <RecordRow
                    key={`${c.ts}-${i}`}
                    lead={<span className="text-[10.5px] tabular-nums">{c.ts.slice(5, 16)}</span>}
                    cols="88px minmax(0,1fr) auto"
                    values={[
                      c.task_id ? (
                        <Link
                          key="t"
                          href={`/function/tasks/detail?id=${c.task_id}`}
                          className="text-xs text-primary hover:underline tabular-nums"
                        >
                          #{c.task_id}
                        </Link>
                      ) : (
                        <span key="t" className="text-xs text-muted-foreground">
                          {t("chat")}
                        </span>
                      ),
                    ]}
                  >
                    <span className="block truncate font-mono text-xs">{c.agent_key || "—"}</span>
                    <span className="mt-0.5 block truncate text-xs text-muted-foreground">
                      {t("argsLen", { n: c.args_len })}
                    </span>
                  </RecordRow>
                ))}
              </div>
            )}
            <SheetFoot left={t("usageFoot")} />
          </Sheet>

          {(skill.usage_agents ?? []).length > 0 ? (
            <Sheet>
              <SheetHead title={t("agents")} count={`${skill.usage_agents.length}`} />
              <div data-editorial="body" className="px-4 pt-1 pb-2">
                {skill.usage_agents.map((a) => (
                  <RecordRow key={a} lead="@" cols="40px minmax(0,1fr)">
                    <Link
                      href={`/system/agents/detail?key=${encodeURIComponent(a)}`}
                      className="block truncate font-mono text-[12px] hover:underline"
                    >
                      {a}
                    </Link>
                  </RecordRow>
                ))}
              </div>
            </Sheet>
          ) : null}
        </div>
      </div>
    </div>
  );
}

export default function SkillDetailPage() {
  return (
    <React.Suspense fallback={null}>
      <SkillDetailInner />
    </React.Suspense>
  );
}
