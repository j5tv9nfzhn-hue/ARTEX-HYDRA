"use client";

// 当前会话所属的任务 id。
//
// 停靠栏要"只显示当前会话所属任务"的子代理，但它挂在 (main) 布局上、拿不到
// 会话上下文。与其把 conv 一路透传穿过 layout，不如用一个极小的外部 store：
// 对话页在会话切换时写入，停靠栏订阅。值变化会触发事件流按新 task_id 重连。
//
// 空串的语义是"这个会话没有绑定任务"（通用对话）——此时停靠栏不显示任何 agent，
// 而不是退化成"显示全部"。

let taskId = "";
const listeners = new Set<() => void>();

export const currentTask = {
  subscribe(fn: () => void) {
    listeners.add(fn);
    return () => listeners.delete(fn);
  },
  getSnapshot(): string {
    return taskId;
  },
  set(next: string | null | undefined) {
    const v = (next ?? "").trim();
    if (v === taskId) return;
    taskId = v;
    for (const l of listeners) l();
  },
};
