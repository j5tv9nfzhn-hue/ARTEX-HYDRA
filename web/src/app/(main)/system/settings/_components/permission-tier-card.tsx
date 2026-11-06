"use client";

import * as React from "react";

import { ShieldAlertIcon, ShieldCheckIcon } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { api } from "@/lib/api";
import type { PermissionTier } from "@/lib/types";
import { cn } from "@/lib/utils";
import { Sheet, SheetHead } from "@/ui/container";

/**
 * 工具权限档位（完全访问 / 标准 / 只读）。
 *
 * 三档的**执行点在后端**：装配 agent 时读全局档位并写进 Options.PermissionMode，
 * 由 norma 的 permission.Evaluate 在每次工具调用前判定。所以这个卡片只是视图 +
 * 一个开关，不做任何本地拦截——前端若也拦一遍，两套口径迟早会漂移。
 *
 * 交互约定：
 *   - 切换即刻提交（没有"保存"按钮）：档位是一个三选一的全局开关，多一步确认
 *     只会让人以为没生效；
 *   - 失败要回滚选中项：否则界面显示"只读"、后端仍是"完全访问"，是最坏的错配；
 *   - 选中只读时显式提示影响面：这一档会让写文件/执行命令类工具直接被拒，
 *     而界面上其他功能看起来一切正常，不提示会被误判成"工具坏了"。
 */
export function PermissionTierCard() {
  const t = useTranslations("settingsPage");

  const [tier, setTier] = React.useState<PermissionTier | null>(null);
  const [saving, setSaving] = React.useState<PermissionTier | null>(null);
  const [loadError, setLoadError] = React.useState<string | null>(null);

  React.useEffect(() => {
    let alive = true;
    api
      .permissionTier()
      .then((res) => {
        if (!alive) return;
        // 后端在"从未配置过"时会回显 default_tier（完全访问），正常情况回显库里的值。
        setTier(res.tier ?? res.default_tier ?? "bypass");
        setLoadError(null);
      })
      .catch((err) => {
        if (!alive) return;
        setLoadError(String(err));
      });
    return () => {
      alive = false;
    };
  }, []);

  const change = React.useCallback(
    async (next: PermissionTier) => {
      if (!tier || saving || next === tier) return;
      const prev = tier;
      setTier(next); // 乐观更新：切换手感优先，失败再回滚
      setSaving(next);
      try {
        const res = await api.setPermissionTier(next);
        setTier(res.tier);
        toast.success(t("permissionTier.toast.saved", { tier: t(`permissionTier.${res.tier}.label`) }));
      } catch (err) {
        setTier(prev);
        toast.error(t("permissionTier.toast.failed", { error: String(err) }));
      } finally {
        setSaving(null);
      }
    },
    [saving, t, tier],
  );

  const options: PermissionTier[] = ["bypass", "default", "plan"];
  // 先算好展示用的派生值：直接在 JSX 里写三元套三元会触发 noNestedTernary，
  // 而且嵌套分支读起来比"先命名再渲染"难得多。
  const badgeVariant = tier === "plan" ? "destructive" : "secondary";
  const headerCount =
    tier === null ? undefined : <Badge variant={badgeVariant}>{t(`permissionTier.${tier}.label`)}</Badge>;

  let body: React.ReactNode;
  if (loadError) {
    body = <p className="text-destructive text-sm">{t("permissionTier.loadFailed", { error: loadError })}</p>;
  } else if (tier === null) {
    body = <p className="text-muted-foreground text-sm">{t("permissionTier.loading")}</p>;
  } else {
    body = (
      <RadioGroup value={tier} onValueChange={(v) => void change(v as PermissionTier)} className="flex flex-col gap-2">
        {options.map((key) => {
          const id = `permission-tier-${key}`;
          const disabled = saving !== null;
          return (
            <label
              key={key}
              htmlFor={id}
              className={cn(
                "flex cursor-pointer items-start gap-2 rounded-md border px-3 py-2 transition-colors hover:bg-accent/40",
                tier === key && "border-primary/60 bg-accent/30",
              )}
            >
              <RadioGroupItem id={id} value={key} disabled={disabled} className="mt-0.5" />
              <span className="flex flex-col gap-0.5">
                <Label htmlFor={id} className="cursor-pointer font-medium text-sm">
                  {t(`permissionTier.${key}.label`)}
                  {key === "bypass" ? (
                    <span className="ml-1 font-normal text-muted-foreground text-xs">
                      （{t("permissionTier.defaultHint")}）
                    </span>
                  ) : null}
                </Label>
                <span className="text-muted-foreground text-xs">{t(`permissionTier.${key}.desc`)}</span>
              </span>
            </label>
          );
        })}
      </RadioGroup>
    );
  }

  return (
    <Sheet api={["permissionTier", "setPermissionTier"]} className="mb-4 break-inside-avoid md:mb-6">
      <SheetHead
        icon={tier === "plan" ? ShieldAlertIcon : ShieldCheckIcon}
        title={t("permissionTier.title")}
        count={headerCount}
      />
      <div className="flex flex-col gap-3 px-4 py-3">
        <p className="text-muted-foreground text-sm">{t("permissionTier.desc")}</p>

        {body}

        {saving ? <p className="text-muted-foreground text-xs">{t("permissionTier.saving")}</p> : null}

        {tier === "plan" ? <p className="text-destructive text-xs">{t("permissionTier.readOnlyWarning")}</p> : null}
      </div>
    </Sheet>
  );
}
