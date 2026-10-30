import { EDITORIAL_SKIN_PRESET, type ResolvedThemeMode, type Skin, type ThemeMode } from "./theme";

export function resolveThemeMode(mode: ThemeMode): ResolvedThemeMode {
  if (mode === "system") {
    const prefersDark = window.matchMedia?.("(prefers-color-scheme: dark)")?.matches;
    return prefersDark ? "dark" : "light";
  }
  return mode === "dark" ? "dark" : "light";
}

export function applyThemeMode(mode: ThemeMode): ResolvedThemeMode {
  const resolved = resolveThemeMode(mode);
  const doc = document.documentElement;
  doc.setAttribute("data-theme-mode", mode);
  doc.classList.add("disable-transitions");
  doc.classList.toggle("dark", resolved === "dark");
  doc.style.colorScheme = resolved;
  requestAnimationFrame(() => {
    doc.classList.remove("disable-transitions");
  });
  return resolved;
}

/**
 * 只改「令牌预设」。新主题下预设被钉死为纸本 —— 界面语言已经换掉了，
 * 再让它跟着九套色板漂移，就不再是同一个主题了。
 */
export function applyThemePreset(value: string) {
  const doc = document.documentElement;
  const skin = doc.getAttribute("data-skin");
  doc.setAttribute("data-theme-preset", skin === "editorial" ? EDITORIAL_SKIN_PRESET : value);
}

/**
 * 切「外观」：老主题 / 新主题。
 *
 * 切到 editorial 时把预设临时钉到 paper（令牌层），切回 classic 时立刻恢复
 * 用户原先选的那套预设 —— 所以「切过去看一眼再切回来」不会弄丢选择。
 */
export function applySkin(value: Skin, preset: string) {
  const doc = document.documentElement;
  doc.classList.add("disable-transitions");
  doc.setAttribute("data-skin", value);
  doc.setAttribute("data-theme-preset", value === "editorial" ? EDITORIAL_SKIN_PRESET : preset);
  requestAnimationFrame(() => {
    doc.classList.remove("disable-transitions");
  });
}

export function subscribeToSystemTheme(onChange: (mode: ResolvedThemeMode) => void): () => void {
  if (typeof window === "undefined") return () => undefined;
  const media = window.matchMedia?.("(prefers-color-scheme: dark)");
  if (!media) return () => undefined;

  const listener = (event: MediaQueryListEvent) => {
    onChange(event.matches ? "dark" : "light");
  };

  media.addEventListener("change", listener);

  return () => {
    media.removeEventListener("change", listener);
  };
}
