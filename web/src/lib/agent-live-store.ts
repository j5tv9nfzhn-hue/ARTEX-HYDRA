"use client";

// 子代理实时状态的外部 store。
//
// 为什么不用组件 state：日志可能每秒几十条，把它塞进 Chat/页面组件的 state 会让
// 整个主对话树跟着重渲染——这正是"子代理刷日志导致主聊天卡死/滚动条乱跳"的成因。
// 这里把流放进模块级 store，110ms 合帧后才通知，且只有订阅了 store 的抽屉会更新。
//
// 为什么不复用对话流：两条 SSE 各自重连、各自续传。任何一条卡住都不会拖累另一条，
// 这也是需求里"主对话流与子代理日志流必须完全解耦"的落点。
//
// ── 2026-11-07 重构（可测性）──────────────────────────────────────────────
// 原来的实现把状态机、EventSource、定时器、模块级变量全部揉在一个文件里，
// 于是它**没有任何单测**（Node 里没法 import `@/lib/api`，也没法在 node:test 里
// 假装 EventSource 存在）。现在拆成两半：
//
//   createAgentLiveStore(opts)  —— 纯逻辑：帧解析 → 合帧 → 快照。
//                                  不碰 window / EventSource，依赖全部注入。
//   agentLive                   —— 单例：把真实 EventSource 与 api 注进去。
//
// 单测见 agent-live-store.test.mjs，覆盖：合帧去重、快照先于增量生效、
// 日志上限、游标只增不减、resync 状态、切任务清空、断连回落 connecting。

import type { AgentLiveEvent, AgentLiveNode, AgentLiveSlots } from "@/lib/types";

/** 每个 agent 保留的日志条数：够看清"现在在干嘛"，又不会把内存吃穿。 */
export const MAX_EVENTS_PER_AGENT = 200;
/** 合帧窗口：把一批事件合成一次 React 更新。 */
export const FLUSH_MS = 110;

export type AgentLiveStatus = "idle" | "connecting" | "live" | "resync";

export type AgentLiveState = {
  /** idle=未连接 · connecting=正在连 · live=正常 · resync=游标失效过一次全量重放 */
  status: AgentLiveStatus;
  seq: number;
  nodes: Record<string, AgentLiveNode>;
  slots: Record<string, AgentLiveSlots>;
  logs: Record<string, AgentLiveEvent[]>;
  lastEventAt: number;
};

export const EMPTY_AGENT_LIVE: AgentLiveState = Object.freeze({
  status: "idle" as AgentLiveStatus,
  seq: 0,
  nodes: {},
  slots: {},
  logs: {},
  lastEventAt: 0,
});

/** 注入点：让单测能换成手动时钟 / 假 EventSource。 */
export type AgentLiveDeps = {
  /** 调度一次合帧；返回取消函数。默认 setTimeout（浏览器）。 */
  schedule?: (fn: () => void, ms: number) => () => void;
  /** 当前时间（毫秒）。默认 Date.now。 */
  now?: () => number;
};

export type AgentLiveStore = {
  subscribe: (fn: () => void) => () => void;
  getSnapshot: () => AgentLiveState;
  /** 处理一帧。返回 true 表示这一帧被接受（JSON 合法）。 */
  handleFrame: (raw: string, kind: "agent" | "snapshot" | "slots" | "resync") => boolean;
  /** 连接建立 / 断开时更新状态（EventSource 的 open / error 回调）。 */
  setStatus: (status: AgentLiveStatus) => void;
  /** 切换任务：清空旧任务的全部状态（避免两段数据混在树上）。 */
  reset: (next?: Partial<AgentLiveState>) => void;
  /** 立即把待合帧的草稿刷给订阅者（测试与卸载时用）。 */
  flush: () => void;
  /** 订阅者数量上限之外的所有逻辑都在这里，便于断言"没人订阅时也要释放"。 */
  destroy: () => void;
};

export function createAgentLiveStore(deps: AgentLiveDeps = {}): AgentLiveStore {
  const schedule =
    deps.schedule ??
    ((fn: () => void, ms: number) => {
      const t = setTimeout(fn, ms);
      return () => clearTimeout(t);
    });
  const now = deps.now ?? (() => Date.now());

  let state: AgentLiveState = EMPTY_AGENT_LIVE;
  let draft: AgentLiveState = EMPTY_AGENT_LIVE;
  let cancelFlush: (() => void) | null = null;
  const listeners = new Set<() => void>();

  function notify() {
    for (const l of listeners) l();
  }

  function scheduleFlush() {
    if (cancelFlush !== null) return;
    cancelFlush = schedule(() => {
      cancelFlush = null;
      state = draft;
      notify();
    }, FLUSH_MS);
  }

  function cancelPendingFlush() {
    if (cancelFlush !== null) {
      cancelFlush();
      cancelFlush = null;
    }
  }

  function reset(next: Partial<AgentLiveState> = {}) {
    cancelPendingFlush();
    draft = {
      ...EMPTY_AGENT_LIVE,
      ...next,
      nodes: { ...(next.nodes ?? {}) },
      slots: { ...(next.slots ?? {}) },
      logs: { ...(next.logs ?? {}) },
    };
    state = draft;
    notify();
  }

  function setStatus(status: AgentLiveStatus) {
    draft = { ...draft, status };
    state = draft;
    notify();
  }

  function ingestSnapshot(agents: AgentLiveNode[], slots: Record<string, AgentLiveSlots>, seq: number) {
    const nodes: Record<string, AgentLiveNode> = {};
    for (const a of agents) nodes[a.id] = a;
    draft = { ...draft, nodes, slots: { ...slots }, seq: Math.max(draft.seq, seq) };
    // 快照是建树的关键帧，必须立即生效（不能等合帧），否则抽屉会先画一帧空树。
    state = { ...draft, status: "live" };
    draft = state;
    notify();
  }

  function ingestEvent(ev: AgentLiveEvent) {
    const next: AgentLiveState = {
      ...draft,
      // 游标只增不减：乱序/重放事件不能把 seq 往回拨，否则重连时 Last-Event-ID
      // 会退回去，服务端把已经推过的增量再推一遍。
      seq: Math.max(draft.seq, ev.seq),
      lastEventAt: now(),
      nodes: { ...draft.nodes },
      logs: { ...draft.logs },
    };
    // 事件里带 state 就顺手更新节点，避免"日志在动、树上的状态还是旧的"。
    const node = ev.agent_id ? next.nodes[ev.agent_id] : undefined;
    if (node && ev.state) {
      next.nodes[ev.agent_id as string] = { ...node, state: ev.state, last_active: ev.at };
    }
    if (ev.agent_id && ev.summary) {
      const prev = next.logs[ev.agent_id] ?? [];
      const capped =
        prev.length >= MAX_EVENTS_PER_AGENT ? prev.slice(prev.length - MAX_EVENTS_PER_AGENT + 1) : prev.slice();
      capped.push(ev);
      next.logs[ev.agent_id] = capped;
    }
    draft = next;
    scheduleFlush();
  }

  /**
   * commitDraft 把草稿里的「游标类」字段同步进已发布快照。
   *
   * 为什么需要：合帧只保证**渲染频率**，不该让 `getSnapshot().seq` 落后于
   * 已收到的事件——重连逻辑读的就是这个 seq（Last-Event-ID）。如果它只在
   * 合帧时才更新，一次连接抖动就可能把已经收到的 5 条事件又请求一遍。
   * 所以 seq / lastEventAt 立即生效，nodes / logs 仍按 110ms 合帧。
   */
  function commitCursor() {
    if (state.seq === draft.seq && state.lastEventAt === draft.lastEventAt) return;
    state = { ...state, seq: draft.seq, lastEventAt: draft.lastEventAt };
  }

  function handleFrame(raw: string, kind: "agent" | "snapshot" | "slots" | "resync"): boolean {
    let data: unknown;
    try {
      data = JSON.parse(raw);
    } catch {
      return false; // 半帧/心跳残片：丢弃即可，下一次重放会补齐
    }
    if (kind === "snapshot") {
      const s = data as { seq?: number; agents?: AgentLiveNode[]; slots?: Record<string, AgentLiveSlots> };
      ingestSnapshot(s.agents ?? [], s.slots ?? {}, s.seq ?? 0);
      return true;
    }
    if (kind === "slots") {
      draft = { ...draft, slots: ((data as Record<string, AgentLiveSlots>) ?? {}) as Record<string, AgentLiveSlots> };
      scheduleFlush();
      commitCursor();
      return true;
    }
    if (kind === "resync") {
      // 游标过期：服务端紧接着会发全量 snapshot，这里只做状态标记。
      state = { ...draft, status: "resync" };
      draft = state;
      notify();
      return true;
    }
    ingestEvent(data as AgentLiveEvent);
    commitCursor();
    return true;
  }

  function flush() {
    cancelPendingFlush();
    state = draft;
    notify();
  }

  function destroy() {
    cancelPendingFlush();
    listeners.clear();
    state = EMPTY_AGENT_LIVE;
    draft = EMPTY_AGENT_LIVE;
  }

  return {
    subscribe(fn: () => void) {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
    getSnapshot: () => state,
    handleFrame,
    setStatus,
    reset,
    flush,
    destroy,
  };
}

// ── 单例：把真实的 EventSource + api 注进去 ────────────────────────────────

type Connector = (fn: () => void) => () => void;

let es: EventSource | null = null;
let listening = 0;
let taskFilter = "";
let openStream: Connector | null = null;

export const agentLive = {
  store: createAgentLiveStore(),

  subscribe(fn: () => void) {
    return agentLive.store.subscribe(fn);
  },
  getSnapshot(): AgentLiveState {
    return agentLive.store.getSnapshot();
  },

  /** 注入真实连接器（默认由 connect() 提供；导出便于测试与登出清理）。 */
  setConnector(connector: Connector | null) {
    openStream = connector;
  },

  start() {
    if (typeof window === "undefined") return;
    listening += 1;
    connect();
  },

  stop() {
    listening = Math.max(0, listening - 1);
    if (listening > 0) return;
    disconnect();
    agentLive.store.reset({ status: "idle" });
  },

  /** 切换当前会话所属任务：换连接并清空旧任务的状态。 */
  setTask(next: string | null | undefined) {
    const v = (next ?? "").trim();
    if (v === taskFilter) return;
    taskFilter = v;
    disconnect();
    agentLive.store.reset({ status: "idle" });
    connect();
  },
};

function disconnect() {
  if (!es) return;
  es.close();
  es = null;
}

/** 建立连接。没有绑定任务时不连——空 task 的流只会推全量，纯属浪费。 */
function connect() {
  if (typeof window === "undefined" || es || !taskFilter) return;
  agentLive.store.reset({ status: "connecting" });
  if (openStream) {
    openStream(() => connect());
    return;
  }
  // 动态 import 避免 node 环境（单测）因为 `@/lib/api` 的别名解析失败而崩。
  void import("@/lib/api").then(({ api }) => {
    if (!taskFilter) return;
    es = new EventSource(api.agentStreamUrl(0, taskFilter));
    es.addEventListener("open", () => {
      agentLive.store.setStatus("live");
    });
    for (const kind of ["agent", "snapshot", "slots", "resync"] as const) {
      es.addEventListener(kind, (e) => agentLive.store.handleFrame((e as MessageEvent).data, kind));
    }
    // EventSource 自带指数退避重连，并会带上 Last-Event-ID；这里不重复实现，
    // 只把状态回落到 connecting，让 UI 显示"正在恢复"而不是假装还活着。
    es.addEventListener("error", () => {
      agentLive.store.setStatus("connecting");
    });
  });
}

/** 仅供单测/登出清理：把 store 恢复到初始态。 */
export function __resetAgentLive() {
  disconnect();
  listening = 0;
  taskFilter = "";
  openStream = null;
  agentLive.store.destroy();
}
