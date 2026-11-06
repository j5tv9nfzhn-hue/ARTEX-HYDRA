"use client";

// 子代理实时状态的外部 store。
//
// 为什么不用组件 state：日志可能每秒几十条，把它塞进 Chat/页面组件的 state 会让
// 整个主对话树跟着重渲染——这正是"子代理刷日志导致主聊天卡死/滚动条乱跳"的成因。
// 这里把流放进模块级 store，110ms 合帧后才通知，且只有订阅了 store 的抽屉会更新。
//
// 为什么不复用对话流：两条 SSE 各自重连、各自续传。任何一条卡住都不会拖累另一条，
// 这也是需求里"主对话流与子代理日志流必须完全解耦"的落点。

import { api } from "@/lib/api";
import type { AgentLiveEvent, AgentLiveNode, AgentLiveSlots } from "@/lib/types";

/** 每个 agent 保留的日志条数：够看清"现在在干嘛"，又不会把内存吃穿。 */
const MAX_EVENTS_PER_AGENT = 200;
/** 合帧窗口：把一批事件合成一次 React 更新。 */
const FLUSH_MS = 110;

export type AgentLiveState = {
  /** idle=未连接 · connecting=正在连 · live=正常 · resync=游标失效过一次全量重放 */
  status: "idle" | "connecting" | "live" | "resync";
  seq: number;
  nodes: Record<string, AgentLiveNode>;
  slots: Record<string, AgentLiveSlots>;
  logs: Record<string, AgentLiveEvent[]>;
  lastEventAt: number;
};

const EMPTY: AgentLiveState = Object.freeze({
  status: "idle",
  seq: 0,
  nodes: {},
  slots: {},
  logs: {},
  lastEventAt: 0,
});

let state: AgentLiveState = EMPTY;
let draft: AgentLiveState = EMPTY;
let listening = 0;
let es: EventSource | null = null;
let taskFilter = "";
let flushTimer: ReturnType<typeof setTimeout> | null = null;
const listeners = new Set<() => void>();

function notify() {
  for (const l of listeners) l();
}

function scheduleFlush() {
  if (flushTimer !== null) return;
  flushTimer = setTimeout(() => {
    flushTimer = null;
    state = draft;
    notify();
  }, FLUSH_MS);
}

function reset(next: Partial<AgentLiveState>) {
  draft = { ...EMPTY, ...next, nodes: { ...next.nodes }, slots: { ...next.slots }, logs: { ...next.logs } };
  state = draft;
  notify();
}

function ingestSnapshot(agents: AgentLiveNode[], slots: Record<string, AgentLiveSlots>, seq: number) {
  const nodes: Record<string, AgentLiveNode> = {};
  for (const a of agents) nodes[a.id] = a;
  draft = { ...draft, nodes, slots, seq: Math.max(draft.seq, seq) };
  scheduleFlush();
}

function ingestEvent(ev: AgentLiveEvent) {
  const next: AgentLiveState = {
    ...draft,
    seq: Math.max(draft.seq, ev.seq),
    lastEventAt: Date.now(),
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

function handleFrame(raw: string, kind: "agent" | "snapshot" | "slots" | "resync") {
  let data: unknown;
  try {
    data = JSON.parse(raw);
  } catch {
    return; // 半帧/心跳残片：丢弃即可，下一次重放会补齐
  }
  if (kind === "snapshot") {
    const s = data as { seq: number; agents: AgentLiveNode[]; slots: Record<string, AgentLiveSlots> };
    ingestSnapshot(s.agents ?? [], s.slots ?? {}, s.seq ?? 0);
    // 快照是建树的关键帧，必须立即生效（不能等合帧），否则抽屉会先画一帧空树。
    state = { ...draft, status: "live" };
    draft = state;
    notify();
    return;
  }
  if (kind === "slots") {
    draft = { ...draft, slots: (data as Record<string, AgentLiveSlots>) ?? {} };
    scheduleFlush();
    return;
  }
  if (kind === "resync") {
    // 游标过期：服务端紧接着会发全量 snapshot，这里只做状态标记。
    state = { ...draft, status: "resync" };
    draft = state;
    notify();
    return;
  }
  ingestEvent(data as AgentLiveEvent);
}

function disconnect() {
  if (!es) return;
  es.close();
  es = null;
}

/** 建立连接。没有绑定任务时不连——空 task 的流只会推全量，纯属浪费。 */
function connect() {
  if (typeof window === "undefined" || es || !taskFilter) return;
  draft = { ...EMPTY, status: "connecting" };
  state = draft;
  notify();
  es = new EventSource(api.agentStreamUrl(0, taskFilter));
  es.addEventListener("open", () => {
    state = { ...draft, status: "live" };
    draft = state;
    notify();
  });
  for (const kind of ["agent", "snapshot", "slots", "resync"] as const) {
    es.addEventListener(kind, (e) => handleFrame((e as MessageEvent).data, kind));
  }
  // EventSource 自带指数退避重连，并会带上 Last-Event-ID；这里不重复实现，
  // 只把状态回落到 connecting，让 UI 显示"正在恢复"而不是假装还活着。
  es.addEventListener("error", () => {
    state = { ...draft, status: "connecting" };
    draft = state;
    notify();
  });
}

export const agentLive = {
  subscribe(fn: () => void) {
    listeners.add(fn);
    return () => listeners.delete(fn);
  },
  getSnapshot(): AgentLiveState {
    return state;
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
    reset({ status: "idle" });
  },
  /** 切换当前会话所属任务：换连接并清空旧任务的状态，避免两段数据混在树上。 */
  setTask(next: string | null | undefined) {
    const v = (next ?? "").trim();
    if (v === taskFilter) return;
    taskFilter = v;
    disconnect();
    reset({ status: "idle" });
    connect();
  },
};

/** 仅供单测/登出清理：把 store 恢复到初始态。 */
export function __resetAgentLive() {
  if (flushTimer !== null) {
    clearTimeout(flushTimer);
    flushTimer = null;
  }
  if (es) {
    es.close();
    es = null;
  }
  listening = 0;
  taskFilter = "";
  state = EMPTY;
  draft = EMPTY;
  notify();
}
