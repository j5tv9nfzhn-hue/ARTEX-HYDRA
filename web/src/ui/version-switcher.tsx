"use client";

import { Layers } from "lucide-react";

import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { applyUIVersion } from "@/lib/preferences/theme-utils";
import { setUIVersion, useUIVersion } from "@/ui/version";

/**
 * 骨架切换：v1（经典）↔ v2（新版）。与 `SkinSwitcher`（外观）正交 ——
 * 四个组合都合法，切换即时生效、不刷新、不丢登录态与偏好。
 */
export function UIVersionSwitcher() {
  const t = useTranslations("uiVersion");
  const version = useUIVersion();
  const next = version === "v2" ? "v1" : "v2";

  return (
    <Button
      size="icon"
      variant="ghost"
      onClick={() => {
        setUIVersion(next);
        applyUIVersion(next);
      }}

      aria-label={t("switchTo", { version: t(next) })}
      title={t("switchTo", { version: t(next) })}
      data-ui-state={version}
    >
      <Layers className="size-4" />
    </Button>
  );
}
