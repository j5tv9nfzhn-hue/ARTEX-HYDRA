"use client";

import { Monitor, Moon, Sun, Type } from "lucide-react";

import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { persistPreference } from "@/lib/preferences/preferences-storage";
import { applySkin } from "@/lib/preferences/theme-utils";
import { usePreferencesStore } from "@/stores/preferences/preferences-provider";

const THEME_CYCLE = ["light", "dark", "system"] as const;

// 主题名只进 aria-label，让屏幕阅读器听到「当前主题：深色」而不是
// light/dark/system；文案走 themeSwitcher 命名空间，键名就是枚举值。

/**
 * 外观一键切换：老主题（九套预设）↔ 新主题（纸本）。
 * 与偏好浮层里的「外观」是同一个偏好，这里只是给一个不用开浮层的入口。
 */
export function SkinSwitcher() {
  const t = useTranslations("themeSwitcher");
  const skin = usePreferencesStore((s) => s.skin);
  const themePreset = usePreferencesStore((s) => s.themePreset);
  const setSkin = usePreferencesStore((s) => s.setSkin);

  const next = skin === "editorial" ? "classic" : "editorial";
  return (
    <Button
      size="icon"
      onClick={() => {
        setSkin(next);
        applySkin(next, themePreset);
        void persistPreference("skin", next);
      }}
      aria-label={t(skin === "editorial" ? "skinAriaPaper" : "skinAriaClassic")}
      title={t(skin === "editorial" ? "skinTitlePaper" : "skinTitleClassic")}
      data-skin-state={skin}
    >
      <Type className="size-4" />
    </Button>
  );
}

export function ThemeSwitcher() {
  const t = useTranslations("themeSwitcher");
  const themeMode = usePreferencesStore((s) => s.themeMode);
  const setThemeMode = usePreferencesStore((s) => s.setThemeMode);

  const cycleTheme = () => {
    const currentIndex = THEME_CYCLE.indexOf(themeMode);
    const nextTheme = THEME_CYCLE[(currentIndex + 1) % THEME_CYCLE.length];

    setThemeMode(nextTheme);
    void persistPreference("theme_mode", nextTheme);
  };

  return (
    <Button size="icon" onClick={cycleTheme} aria-label={t("currentTheme", { theme: t(themeMode) })}>
      {/* SYSTEM */}
      <Monitor className="hidden [html[data-theme-mode=system]_&]:block" />

      {/* DARK (resolved) */}
      <Sun className="hidden dark:block [html[data-theme-mode=system]_&]:hidden" />

      {/* LIGHT (resolved) */}
      <Moon className="block dark:hidden [html[data-theme-mode=system]_&]:hidden" />
    </Button>
  );
}
