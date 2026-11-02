import { create } from "zustand";

/**
 * 新版骨架的视图状态。
 *
 * 对齐 STRIX 的核心做法：下钻用**面板**而不是路由。STRIX 只有 6 个 View，
 * 漏洞详情（VulnerabilityDetail）与代理详情（AgentDetailModal）都是面板/弹层，
 * 不产生新 URL。v2 沿用同一模型：`view` 决定一屏，`selected` 决定屏内下钻。
 *
 * v1 完全不使用本 store —— 双轨并行，互不干扰。
 */
export type V2View = "overview" | "runs" | "findings" | "intercept" | "settings";

export interface V2Selection {
  /** 实体类型，取自路由末段（如 findings / tasks / approvals）。 */
  kind: string;
  id: string;
}

export interface V2ViewState {
  view: V2View;
  selected: V2Selection | null;
  panelOpen: boolean;
  /** 切换视图：清空下钻状态，保证「点侧栏总是回到该域的顶层」。 */
  setView: (view: V2View) => void;
  openPanel: (kind: string, id: string) => void;
  closePanel: () => void;
}

export const useViewStore = create<V2ViewState>((set) => ({
  view: "overview",
  selected: null,
  panelOpen: false,
  setView: (view) => set({ view, selected: null, panelOpen: false }),
  openPanel: (kind, id) => set({ selected: { kind, id }, panelOpen: true }),
  closePanel: () => set({ panelOpen: false }),
}));
