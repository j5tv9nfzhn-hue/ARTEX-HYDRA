"use client";

/**
 * CJK 输入法「伴生 Enter 幽灵键」守卫（desktop-agent-ui 技能 ENG-07）。
 *
 * 拼音等输入法在候选词浮层按 Enter 确认选词时，浏览器会在触发 compositionend
 * 的同时（或几毫秒后）再抛出一个 keyCode=13 的伴生 keydown。此刻 isComposing
 * 已经复位为 false，仅靠 isComposing / keyCode===229 双检拦不住——用户只是想
 * 选词，消息却被直接发送出去。
 *
 * 守卫：记录最近一次 compositionend 的时间戳；500ms 内到达的 Enter 判定为伴生
 * 幽灵键，拦截，并【立即清零时间戳】——否则紧随其后的正常连续击键会被误吞。
 */
const GHOST_WINDOW_MS = 500;

let lastCompositionEndTime = 0;

/** compositionend 时调用：记下时间戳，供 isGhostEnter 判断伴生键。 */
export function markCompositionEnd(): void {
  lastCompositionEndTime = performance.now();
}

/**
 * 判断这次 Enter 是否为组字确认的伴生幽灵键；是则拦截。
 * 命中后立即复位时间戳（副作用，已写进签名注释），保证后续正常按键畅通——
 * 调用方拦截后无需再做任何清理。
 */
export function isGhostEnter(e: { key: string }): boolean {
  if (e.key !== "Enter" || lastCompositionEndTime === 0) return false;
  if (performance.now() - lastCompositionEndTime >= GHOST_WINDOW_MS) return false;
  lastCompositionEndTime = 0;
  return true;
}
