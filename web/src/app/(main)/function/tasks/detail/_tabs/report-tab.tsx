"use client";

import * as React from "react";

import { CheckIcon, CopyIcon, FileTextIcon } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { EmptyState } from "@/components/empty-state";
import { Markdown } from "@/components/markdown";
import { Button } from "@/components/ui/button";
import { Sheet, SheetFoot, SheetHead } from "@/components/editorial-sheet";
import { api } from "@/lib/api";
import { copyText } from "@/lib/utils";

export function ReportTab({
  taskId,
  analysisDomain,
}: {
  taskId: string;
  analysisDomain?: "web_security" | "software_re";
}) {
  const softwareRE = analysisDomain === "software_re";
  const t = useTranslations("taskDetail.report");
  const [report, setReport] = React.useState<string>("");
  const [loading, setLoading] = React.useState(true);
  const [copied, setCopied] = React.useState(false);

  React.useEffect(() => {
    let active = true;
    setLoading(true);
    api
      .report(taskId)
      .then((text) => {
        if (active) setReport(text);
      })
      .catch(() => {
        if (active) setReport("");
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [taskId]);

  async function copy() {
    if (!report) return;
    const ok = await copyText(report);
    if (ok) {
      setCopied(true);
      toast.success(t("copySuccess"));
      setTimeout(() => setCopied(false), 1500);
    } else {
      toast.error(t("copyError"));
    }
  }

  let content: React.ReactNode;
  if (loading) {
    content = (
      <div className="flex flex-col items-center justify-center gap-2 rounded-md border border-dashed py-16 text-muted-foreground text-sm">
        <FileTextIcon className="size-8 opacity-40" />
        {t("loading")}
      </div>
    );
  } else if (report) {
    content = (
      <div className="max-h-[60vh] overflow-auto rounded-md border bg-muted/20 p-4">
        <Markdown text={report} />
      </div>
    );
  } else {
    content = (
      <div className="rounded-md border border-dashed">
        <EmptyState illustration="flag" title={t(softwareRE ? "softwareEmpty" : "empty")} />
      </div>
    );
  }

  return (
    <Sheet>
      <SheetHead
        icon={FileTextIcon}
        title={t(softwareRE ? "softwareTitle" : "title")}
        count={report ? t("chars", { n: report.length.toLocaleString("zh-CN") }) : undefined}
        actions={
          report ? (
            <Button size="sm" variant="outline" onClick={copy}>
              {copied ? <CheckIcon data-icon="inline-start" /> : <CopyIcon data-icon="inline-start" />}
              {t("copy")}
            </Button>
          ) : null
        }
      />
      <div className="px-4 pt-1">{content}</div>
      <SheetFoot left={t(softwareRE ? "softwareTitle" : "title")} right={t("foot")} />
    </Sheet>
  );
}
