export const THEME_MODE_OPTIONS = [
  { label: "浅色", value: "light" },
  { label: "深色", value: "dark" },
  { label: "跟随系统", value: "system" },
] as const;

export const THEME_MODE_VALUES = THEME_MODE_OPTIONS.map((o) => o.value);
export type ThemeMode = (typeof THEME_MODE_VALUES)[number];
export type ResolvedThemeMode = "light" | "dark";

// ── 外观（双主题）────────────────────────────────────────────────────────────
//
// 「外观」与「主题预设」是两个维度，不要混：
//
//   skin = classic    老主题。九套预设（默认 / 琥珀终端 / 粗野主义 / 深空指挥室 /
//                     矩阵绿 / 纸本 / 柔和流行 / 柑橘 / 双墨）各自保持改造前的观感，
//                     一条样式都不动。
//   skin = editorial  新主题「纸本」。整套界面换成调试版前端那套语言：
//                     纸底墨字、发丝线、下划线页签、等宽数字、无卡片、无阴影。
//                     颜色固定取 presets/paper.css（与调试版前端逐值一致），
//                     版式由 styles/skin-editorial.css 负责。
//
// 两者都存在 cookie 里；切回 classic 时，会恢复切走之前选的那套预设。

export const SKIN_OPTIONS = [
  { label: "经典", value: "classic", hint: "原本的九套预设" },
  { label: "纸本", value: "editorial", hint: "书刊排版 / 发丝线 / 等宽数字" },
] as const;

export const SKIN_VALUES = SKIN_OPTIONS.map((o) => o.value);
export type Skin = (typeof SKIN_VALUES)[number];

/** 新主题固定使用的令牌预设。 */
export const EDITORIAL_SKIN_PRESET = "paper" as const;

// --- generated:themePresets:start ---

export const THEME_PRESET_OPTIONS = [
  {
    label: "默认",
    value: "default",
    primary: {
      light: "oklch(0.205 0 0)",
      dark: "oklch(0.922 0 0)",
    },
  },
  {
    label: "琥珀终端",
    value: "amber-crt",
    primary: {
      light: "oklch(0.58 0.14 58)",
      dark: "oklch(0.79 0.17 74)",
    },
  },
  {
    label: "粗野主义",
    value: "brutalist",
    primary: {
      light: "oklch(0.6489 0.237 26.9728)",
      dark: "oklch(0.7044 0.1872 23.1858)",
    },
  },
  {
    label: "深空指挥室",
    value: "command",
    primary: {
      light: "oklch(0.52 0.105 223.13)",
      dark: "oklch(0.79 0.15 211.5)",
    },
  },
  {
    label: "矩阵绿",
    value: "matrix",
    primary: {
      light: "oklch(0.52 0.15 152)",
      dark: "oklch(0.84 0.25 148)",
    },
  },
  {
    label: "纸本",
    value: "paper",
    primary: {
      light: "oklch(0.5 0.078 200)",
      dark: "oklch(0.735 0.09 196)",
    },
  },
  {
    label: "柔和流行",
    value: "soft-pop",
    primary: {
      light: "oklch(0.5106 0.2301 276.9656)",
      dark: "oklch(0.6801 0.1583 276.9349)",
    },
  },
  {
    label: "柑橘",
    value: "tangerine",
    primary: {
      light: "oklch(0.64 0.17 36.44)",
      dark: "oklch(0.64 0.17 36.44)",
    },
  },
  {
    label: "双墨",
    value: "two-inks",
    primary: {
      light: "oklch(0.548 0.187 33)",
      dark: "oklch(0.700 0.170 34)",
    },
  },
] as const;

export const THEME_PRESET_VALUES = THEME_PRESET_OPTIONS.map((p) => p.value);

export type ThemePreset = (typeof THEME_PRESET_OPTIONS)[number]["value"];

// --- generated:themePresets:end ---
