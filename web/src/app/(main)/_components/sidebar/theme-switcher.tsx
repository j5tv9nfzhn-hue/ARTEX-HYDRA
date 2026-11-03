"use client";

import { Monitor, Moon, Sun } from "lucide-react";

import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import { persistPreference } from "@/lib/preferences/preferences-storage";
import { usePreferencesStore } from "@/stores/preferences/preferences-provider";

const THEME_CYCLE = ["light", "dark", "system"] as const;

// 主题名只进 aria-label，让屏幕阅读器听到「当前主题：深色」而不是

/**
 * 明暗三态循环：浅色 → 深色 → 跟随系统。
 *
 * 这里原本还有一个 SkinSwitcher（老主题 ↔ 纸本外观的一键切换入口）。
 * 纸本 / 编年主题连同 data-skin 这一维度已整体移除，控制台只剩科技风预设，
 * 那个按钮点下去只会原地打转，所以连组件带偏好一起删掉了。
 */

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
