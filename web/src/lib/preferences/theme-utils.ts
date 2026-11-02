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

/**
 * 切「骨架」：v1（经典）↔ v2（新版）。
 *
 * 这是 P4 解耦的关键动作：切到 v2 时把 `data-skin` 钉回 classic，让
 * styles/skin-editorial.css（1434 行换肤层）**整份失效** —— 那套换肤是作用在
 * 「旧骨架的 shadcn 组件」上的，继续作用在新骨架上只会两套规则互相覆盖。
 * v2 的观感由 styles/ui-v2.css（:root[data-ui="v2"]）独立承担。
 *
 * 切回 v1 时从 data-skin-prev 恢复用户原先的皮肤：来回切换不丢选择。
 */
export function applyUIVersion(value: "v1" | "v2"): void {
  const doc = document.documentElement;
  doc.classList.add("disable-transitions");
  const current = doc.getAttribute("data-skin") ?? "classic";
  doc.setAttribute("data-ui", value);
  if (value === "v2") {
    doc.setAttribute("data-skin-prev", current);
    doc.setAttribute("data-skin", "classic");
  } else {
    doc.setAttribute("data-skin", doc.getAttribute("data-skin-prev") ?? current);
  }
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
