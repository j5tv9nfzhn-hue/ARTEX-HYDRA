/**
 * 对话页的「单一根 Agent」口径（HARNESS 化，2026-11-07）。
 *
 * 为什么单独一个文件：这几条规则原本是散在 `chat/page.tsx` 里的内联表达式
 * （`agentLabel`、`rootAgent`、`chatAgents`、`groupByAgent`），既没法单测，
 * 也容易被下一个人顺手改回「按 agent 分组 / 按 agent 选择」。抽成零 React 依赖
 * 的纯函数后，`agent-live-store.test.mjs` 同款的 node 单测就能把它们钉住。
 *
 * 三条硬规则（来自 Issue #6 §4-A 与产品定义）：
 *
 *  1. 对话页只有一个身份：根 Agent。子代理由根在任务里用工具临时派发，
 *     不是用户要选的角色 —— 所以**没有** agent 选择器，也没有"按 agent 分组"。
 *  2. 界面文案不暴露内部 key。旧线程的 `agent_key` 可能指向已下线的角色
 *     （auto / pentest / mainagent），这些行在 `/api/agents` 里根本查不到；
 *     以前会退化成把 `conversation.agent_key` 当名字显示，于是界面上出现
 *     "mainagent" 这种内部标识。
 *  3. 旧线程不能被"修正"成根 Agent：它们是真的由旧角色跑的，改标签会让历史
 *     失真。这里的处理是统一显示为"根 Agent"这一**产品名**，而不是改写数据。
 */

/** 根 Agent 的产品名。界面上一律用它，不用 agent.name 也不用 key。 */
export const ROOT_AGENT_LABEL = "根 Agent";

/** 已下线的角色 key：界面上绝不能出现这些词。 */
export const LEGACY_AGENT_KEYS = ["planner", "mainagent", "reviewer", "auto", "pentest"] as const;

type AgentLike = { key: string; name?: string | null };

/**
 * 判定一个 key 是否是"当前世代的根"。
 *
 * coord_root 是运行时模板行（不在 /api/agents 清单里），worker 是它的共用
 * 运行配置载体（`DefaultCoordRootAgentKey`）。两者都算根身份。
 */
export function isRootAgentKey(key: string | null | undefined): boolean {
  const k = (key ?? "").trim();
  return k === "coord_root" || k === "worker";
}

/**
 * 对话页显示的身份名。
 *
 * 规则：**永远是根 Agent 的产品名**。理由：单 Agent HARNESS 下对话页只有
 * 一个身份，把每条线程标注成不同的 agent 名会重新引入"我到底在跟谁说话"的
 * 歧义；而旧线程的真实归属在会话详情里仍可查（DB 不动）。
 */
export function conversationAgentLabel(): string {
  return ROOT_AGENT_LABEL;
}

/**
 * 新线程要落的 agent_key。
 *
 * 优先 coord_root；`/api/orchestration` 偶发失败时退回 worker（同一份运行配置），
 * 而不是把发送按钮锁死。两者都不是时返回空串，让调用方显式处理。
 */
export function draftAgentKey(agents: AgentLike[]): string {
  for (const a of agents) {
    if (isRootAgentKey(a.key)) return a.key;
  }
  // 退化为"第一个非已下线项"：清单里可能只有历史角色行（升级后没清理干净的库），
  // 把新线程落到 planner/mainagent 上等于把已经删掉的角色又激活一次。
  for (const a of agents) {
    if (!isLegacyAgentKey(a.key)) return a.key;
  }
  return "";
}

/**
 * 草稿页可选的 agent 列表：恒为 0 或 1 项。
 *
 * 存在这个函数（而不是直接 `agents.slice(0,1)`）是为了让"只允许一个"这件事
 * 成为被测试的约定：以后谁想加回选择器，必须先改这条测试。
 */
export function singleAgentSelection(agents: AgentLike[]): AgentLike[] {
  const root = agents.find((a) => isRootAgentKey(a.key)) ?? agents[0];
  return root ? [root] : [];
}

/**
 * 把线程分组成"一组"。
 *
 * 左侧栏以前按 agent 分组（`groupByAgent`），在单 Agent 形态下每组恒为一项，
 * 分组头纯属噪音；更糟的是它把 `conversation.agent_key` 当组名漏到界面上。
 * 现在固定返回单组：`{ key: "root", label: ROOT_AGENT_LABEL }`。
 */
export function singleThreadGroup(): { key: string; label: string } {
  return { key: "root", label: ROOT_AGENT_LABEL };
}

/**
 * 该 key 是否属于已下线的角色（用于"历史线程"标记，而非隐藏）。
 */
export function isLegacyAgentKey(key: string | null | undefined): boolean {
  const k = (key ?? "").trim();
  return (LEGACY_AGENT_KEYS as readonly string[]).includes(k);
}
