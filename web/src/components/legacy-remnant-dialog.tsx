"use client";

import * as React from "react";

import { ShieldAlertIcon, TriangleAlertIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api } from "@/lib/api";
import { storageApi } from "@/lib/storage-api";

// 旧版内置 Agent 的 key，与后端 db.legacyAgentKeys 保持同一份名单。
// 正常启动后它们已被清库；这里仍留一份，是为了让「升级了二进制但没重启服务」
// 的中间状态也能被识别出来。
const LEGACY_AGENT_KEYS = ["planner", "mainagent", "reviewer", "auto", "pentest"];

/** 用户选择「不再提示」后写入；清掉它就会重新检查。 */
const DISMISS_KEY = "artex.remnant.notice.dismissed";

/**
 * 升级后的一次性提示：检测到过期工具与数据残留。
 *
 * 数据全部来自真实接口（/api/agents 与存储的孤儿数据预检），不构造任何
 * 假数字；接口不可用时静默跳过，不弹一个空壳框。
 *
 * 配色只用语义 token（background / card / muted-foreground / destructive），
 * 亮色与深色都走同一套，不写死颜色。
 */
export function LegacyRemnantDialog() {
  const [open, setOpen] = React.useState(false);
  const [findings, setFindings] = React.useState<string[]>([]);

  React.useEffect(() => {
    try {
      if (window.localStorage.getItem(DISMISS_KEY) === "1") return;
    } catch {
      /* localStorage 被禁用时按「每次都提示」处理，不影响主流程。 */
    }
    let alive = true;
    void (async () => {
      const found: string[] = [];
      try {
        const agents = await api.agents();
        const legacy = agents.filter((a) => LEGACY_AGENT_KEYS.includes(a.key));
        if (legacy.length > 0) {
          found.push(`旧版内置 Agent ${legacy.length} 个：${legacy.map((a) => a.key).join("、")}`);
        }
      } catch {
        /* 接口不可达就不提示这一项。 */
      }
      try {
        const preview = await storageApi.orphanPreview();
        const n = preview.counts.facts + preview.counts.edges + preview.counts.activity;
        if (n > 0) {
          found.push(
            `图谱孤儿数据 ${n} 条（事实 ${preview.counts.facts} / 边 ${preview.counts.edges} / 活动 ${preview.counts.activity}）`,
          );
        }
      } catch {
        /* 同上。 */
      }
      if (alive && found.length > 0) {
        setFindings(found);
        setOpen(true);
      }
    })();
    return () => {
      alive = false;
    };
  }, []);

  const dismissForever = () => {
    try {
      window.localStorage.setItem(DISMISS_KEY, "1");
    } catch {
      /* 写不进去就只关掉这一次。 */
    }
    setOpen(false);
  };

  return (
    <Dialog open={open} onOpenChange={(o) => !o && setOpen(false)}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <TriangleAlertIcon className="size-4 text-amber-500" />
            检测到过期工具与数据残留
          </DialogTitle>
          <DialogDescription>
            本次升级更换了 Agent
            架构。下面这些残留不会被自动删除——它们可能牵涉历史任务、提示词版本与图谱数据，自动清理有丢数据的风险。
          </DialogDescription>
        </DialogHeader>

        <ul className="flex flex-col gap-1.5 rounded-lg border bg-muted/40 p-3 text-sm">
          {findings.map((f) => (
            <li key={f} className="flex items-start gap-2">
              <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-amber-500" />
              <span className="break-words">{f}</span>
            </li>
          ))}
        </ul>

        <div className="flex items-start gap-2 rounded-lg border border-destructive/25 bg-destructive/5 p-3 text-destructive text-sm">
          <ShieldAlertIcon className="mt-0.5 size-4 shrink-0" />
          <div className="space-y-1">
            <p className="font-medium">请联系 AI 进行手动处理。</p>
            <p className="text-destructive/80">
              处理过程会涉及数据库。动手前请务必先备份 <span className="font-mono">artex.db</span> 及其
              <span className="font-mono"> -wal</span> / <span className="font-mono">-shm</span> 文件。
            </p>
          </div>
        </div>

        <DialogFooter className="flex-row items-center justify-between sm:justify-between">
          <Button type="button" variant="ghost" onClick={dismissForever}>
            不再提示
          </Button>
          <Button type="button" onClick={() => setOpen(false)}>
            我知道了
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
