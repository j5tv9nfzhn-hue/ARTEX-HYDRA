/**
 * 侧边栏「悬停展开」的时序状态机（Hover-Reveal as Fixed Overlay）。
 *
 * 抽成纯逻辑是为了可测：悬停意图的正确性全在计时器的先后顺序上，
 * 而这部分在浏览器里很难稳定复现（指针抖动、事件顺序、动画帧）。
 * 组件只负责把 DOM 事件翻译成这里的四个动作。
 *
 * 为什么是两段延时：
 * - 开启延时挡住「指针横扫」——逐个图标都会触发 enter，立即展开会一路闪开。
 * - 关闭延时挡住「折返抖动」——覆盖层没有物理边界承接指针，离开后立刻收起
 *   会让指针落回图标、再次触发展开，形成闪烁。
 */

/** 进入热区到展开的等待时间（毫秒）。 */
export const SIDEBAR_REVEAL_OPEN_DELAY = 180;

/** 离开热区到收起的等待时间（毫秒）。比开启延时更长：宁可多留一会儿。 */
export const SIDEBAR_REVEAL_CLOSE_DELAY = 260;

/** 计时器抽象：测试里换成手动推进的假实现。 */
export type RevealScheduler = {
  set: (callback: () => void, ms: number) => number;
  clear: (id: number) => void;
};

const defaultScheduler: RevealScheduler = {
  set: (callback, ms) => globalThis.setTimeout(callback, ms) as unknown as number,
  clear: (id) => globalThis.clearTimeout(id),
};

export type RevealController = {
  /** 指针进入热区（图标轨道或已展开的覆盖层）。 */
  pointerEnter: () => void;
  /** 指针离开热区。 */
  pointerLeave: () => void;
  /** 键盘焦点进入侧栏：不等待，立即展开。 */
  focusIn: () => void;
  /** 键盘焦点离开。movedWithin 为 true 表示只是在侧栏内部移动。 */
  focusOut: (movedWithin: boolean) => void;
  /** Esc：不等待，立即收起。 */
  escape: () => void;
  /** 是否允许悬停展开（桌面端 + 图标折叠 + 当前已折叠）。 */
  setEnabled: (enabled: boolean) => void;
  /** 当前是否处于覆盖层展开态。 */
  isRevealed: () => boolean;
  /** 清空计时器。 */
  dispose: () => void;
};

/**
 * 创建悬停展开控制器。
 *
 * onChange 只在状态真正翻转时调用，避免同一状态重复 setState。
 */
export function createRevealController(
  onChange: (revealed: boolean) => void,
  scheduler: RevealScheduler = defaultScheduler,
): RevealController {
  let revealed = false;
  let enabled = false;
  let openTimer: number | null = null;
  let closeTimer: number | null = null;

  const clearOpen = () => {
    if (openTimer === null) return;
    scheduler.clear(openTimer);
    openTimer = null;
  };

  const clearClose = () => {
    if (closeTimer === null) return;
    scheduler.clear(closeTimer);
    closeTimer = null;
  };

  const setRevealed = (next: boolean) => {
    if (revealed === next) return;
    revealed = next;
    onChange(next);
  };

  return {
    pointerEnter() {
      if (!enabled) return;
      // 指针折返：先撤掉待执行的收起，连开合动画都不会触发。
      clearClose();
      if (revealed || openTimer !== null) return;
      openTimer = scheduler.set(() => {
        openTimer = null;
        setRevealed(true);
      }, SIDEBAR_REVEAL_OPEN_DELAY);
    },

    pointerLeave() {
      if (!enabled) return;
      // 还没展开就离开：直接撤销待执行的展开，保持收起。
      clearOpen();
      if (!revealed || closeTimer !== null) return;
      closeTimer = scheduler.set(() => {
        closeTimer = null;
        setRevealed(false);
      }, SIDEBAR_REVEAL_CLOSE_DELAY);
    },

    focusIn() {
      if (!enabled) return;
      clearOpen();
      clearClose();
      setRevealed(true);
    },

    focusOut(movedWithin) {
      if (!enabled || movedWithin) return;
      clearOpen();
      if (!revealed || closeTimer !== null) return;
      closeTimer = scheduler.set(() => {
        closeTimer = null;
        setRevealed(false);
      }, SIDEBAR_REVEAL_CLOSE_DELAY);
    },

    escape() {
      clearOpen();
      clearClose();
      setRevealed(false);
    },

    setEnabled(next) {
      enabled = next;
      if (next) return;
      clearOpen();
      clearClose();
      setRevealed(false);
    },

    isRevealed() {
      return revealed;
    },

    dispose() {
      clearOpen();
      clearClose();
    },
  };
}
