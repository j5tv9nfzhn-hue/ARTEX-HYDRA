"use client";

import { Monitor, Moon, Sun, Type } from "lucide-react";

import { Button } from "@/components/ui/button";
import { persistPreference } from "@/lib/preferences/preferences-storage";
import { applySkin } from "@/lib/preferences/theme-utils";
import { usePreferencesStore } from "@/stores/preferences/preferences-provider";

const THEME_CYCLE = ["light", "dark", "system"] as const;

// 主题枚举到中文名的映射：自动切换到无标题按钮，中文名只进 aria-label，
// 让屏幕阅读器听到「当前主题：深色」而不是 light/dark/system。
const THEME_NAMES: Record<(typeof THEME_CYCLE)[number], string> = {
  light: "浅色",
  dark: "深色",
  system: "跟随系统",
};

/**
 * 外观一键切换：老主题（九套预设）↔ 新主题（纸本）。
 * 与偏好浮层里的「外观」是同一个偏好，这里只是给一个不用开浮层的入口。
 */
export function SkinSwitcher() {
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
      aria-label={skin === "editorial" ? "当前为新主题（纸本），点击切回老主题" : "当前为老主题，点击切到新主题（纸本）"}
      title={skin === "editorial" ? "新主题 / 纸本" : "老主题"}
      data-skin-state={skin}
    >
      <Type className="size-4" />
    </Button>
  );
}

export function ThemeSwitcher() {
  const themeMode = usePreferencesStore((s) => s.themeMode);
  const setThemeMode = usePreferencesStore((s) => s.setThemeMode);

  const cycleTheme = () => {
    const currentIndex = THEME_CYCLE.indexOf(themeMode);
    const nextTheme = THEME_CYCLE[(currentIndex + 1) % THEME_CYCLE.length];

    setThemeMode(nextTheme);
    void persistPreference("theme_mode", nextTheme);
  };

  return (
    <Button size="icon" onClick={cycleTheme} aria-label={`当前主题：${THEME_NAMES[themeMode]}，点击切换`}>
      {/* SYSTEM */}
      <Monitor className="hidden [html[data-theme-mode=system]_&]:block" />

      {/* DARK (resolved) */}
      <Sun className="hidden dark:block [html[data-theme-mode=system]_&]:hidden" />

      {/* LIGHT (resolved) */}
      <Moon className="block dark:hidden [html[data-theme-mode=system]_&]:hidden" />
    </Button>
  );
}
