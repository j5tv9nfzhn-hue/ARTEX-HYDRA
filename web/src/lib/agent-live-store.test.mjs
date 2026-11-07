import { createAgentLiveStore, MAX_EVENTS_PER_AGENT } from "./agent-live-store.ts";
import assert from "node:assert/strict";
import test from "node:test";

/**
 * agent-live-store 的状态机单测（Issue #6 §4-A 里点名缺失的那份）。
 *
 * 为什么这个文件重要：右侧 AgentDock 的树、日志、槽位水位全靠这个 store。
 * 它之前一行测试都没有 —— 出现"面板不出现/树不更新/日志串台"时，只能靠
 * 在浏览器里点，而这类问题恰恰是"看起来在跑、其实什么都没收到"。
 *
 * 用可控调度器替换 setTimeout：合帧是 110ms 一次，用真实计时器写测试既慢
 * 又不稳（CI 上偶发）。这里由测试自己决定何时"到帧"。
 */
function manualScheduler() {
  const queue = [];
  return {
    schedule: (fn) => {
      queue.push(fn);
      return () => {
        const i = queue.indexOf(fn);
        if (i >= 0) queue.splice(i, 1);
      };
    },
    flushFrame() {
      const pending = queue.splice(0, queue.length);
      for (const fn of pending) fn();
    },
    get pending() {
      return queue.length;
    },
  };
}

function newStore() {
  const sched = manualScheduler();
  let clock = 1_000;
  const store = createAgentLiveStore({ schedule: sched.schedule, now: () => clock++ });
  return { store, sched };
}

const snapshotFrame = {
  seq: 3,
  agents: [
    { id: "root", parent_id: "", state: "running", task_id: "t1" },
    { id: "sqli-1", parent_id: "root", state: "running", task_id: "t1" },
  ],
  slots: { t1: { limit: 4, used: 1, free: 3, chain: "3->2->1->0" } },
};

test("snapshot 立即生效：不等合帧就能建树", () => {
  const { store, sched } = newStore();
  assert.equal(store.getSnapshot().status, "idle");
  store.handleFrame(JSON.stringify(snapshotFrame), "snapshot");
  const st = store.getSnapshot();
  // 关键：如果这里还是空树，抽屉会先画一帧空白再跳出来（用户看到的"闪"）。
  assert.equal(st.status, "live");
  assert.equal(Object.keys(st.nodes).length, 2);
  assert.equal(st.nodes["sqli-1"].parent_id, "root");
  assert.equal(st.seq, 3);
  assert.equal(sched.pending, 0, "snapshot 不应排队等待合帧");
});

test("增量事件合帧：一帧内多次事件只通知一次", () => {
  const { store, sched } = newStore();
  let notifications = 0;
  store.subscribe(() => {
    notifications += 1;
  });
  store.handleFrame(JSON.stringify(snapshotFrame), "snapshot");
  const afterSnapshot = notifications; // snapshot 自己通知了一次

  store.handleFrame(JSON.stringify({ seq: 4, agent_id: "sqli-1", summary: "跑 sqlmap", at: 1 }), "agent");
  store.handleFrame(JSON.stringify({ seq: 5, agent_id: "sqli-1", summary: "命中注入点", at: 2 }), "agent");
  assert.equal(notifications, afterSnapshot, "合帧窗口内不应通知");

  sched.flushFrame();
  assert.equal(notifications, afterSnapshot + 1, "一帧只通知一次");
  const st = store.getSnapshot();
  assert.equal(st.logs["sqli-1"].length, 2);
  assert.equal(st.seq, 5);
});

test("游标只增不减：乱序或重放事件不能把 seq 往回拨", () => {
  const { store } = newStore();
  store.handleFrame(JSON.stringify({ seq: 10, agent_id: "a", summary: "x", at: 1 }), "agent");
  store.handleFrame(JSON.stringify({ seq: 4, agent_id: "a", summary: "旧的重放", at: 0 }), "agent");
  // seq 若被拨回 4，重连时 Last-Event-ID 会退回去，服务端会重推已收过的增量。
  assert.equal(store.getSnapshot().seq, 10);
});

test("日志有上限：不会把整条时间线都留在内存里", () => {
  const { store, sched } = newStore();
  for (let i = 0; i < MAX_EVENTS_PER_AGENT + 25; i++) {
    store.handleFrame(JSON.stringify({ seq: i + 1, agent_id: "w", summary: `step ${i}`, at: i }), "agent");
  }
  sched.flushFrame();
  const logs = store.getSnapshot().logs.w;
  assert.equal(logs.length, MAX_EVENTS_PER_AGENT);
  // 保留的必须是最后 N 条：丢掉最新的会让面板停在很久以前。
  assert.equal(logs[logs.length - 1].summary, `step ${MAX_EVENTS_PER_AGENT + 24}`);
});

test("只有带 summary 的事件才进日志，但 state 仍更新节点", () => {
  const { store, sched } = newStore();
  store.handleFrame(JSON.stringify(snapshotFrame), "snapshot");
  store.handleFrame(JSON.stringify({ seq: 4, agent_id: "sqli-1", state: "completed", at: 9 }), "agent");
  sched.flushFrame();
  const st = store.getSnapshot();
  assert.equal(st.nodes["sqli-1"].state, "completed", "状态变更必须反映到树上");
  assert.equal(Object.keys(st.logs).length, 0);
});

test("slots 帧替换整张表（水位来自服务端，不在前端累加）", () => {
  const { store, sched } = newStore();
  store.handleFrame(JSON.stringify(snapshotFrame), "snapshot");
  store.handleFrame(JSON.stringify({ t1: { limit: 4, used: 4, free: 0, chain: "0", full: true } }), "slots");
  sched.flushFrame();
  const slots = store.getSnapshot().slots.t1;
  assert.equal(slots.used, 4);
  assert.equal(slots.full, true, "满载标记必须透传到 UI（队列恒空，Full 才是信号）");
});

test("resync 帧把状态标出来，等快照重建", () => {
  const { store } = newStore();
  store.handleFrame(JSON.stringify(snapshotFrame), "snapshot");
  store.handleFrame(JSON.stringify({ reason: "cursor_expired" }), "resync");
  assert.equal(store.getSnapshot().status, "resync");
});

test("坏帧不崩：半帧/心跳残片直接丢弃", () => {
  const { store } = newStore();
  assert.equal(store.handleFrame('{"seq":1,"agents":[', "snapshot"), false);
  // 状态不受影响，仍然是初始值 —— 下一次重放会补齐。
  assert.equal(store.getSnapshot().status, "idle");
});

test("切任务清空旧状态，避免两个任务的树混在一起", () => {
  const { store } = newStore();
  store.handleFrame(JSON.stringify(snapshotFrame), "snapshot");
  assert.equal(Object.keys(store.getSnapshot().nodes).length, 2);
  store.reset({ status: "idle" });
  const st = store.getSnapshot();
  assert.deepEqual(st.nodes, {});
  assert.deepEqual(st.logs, {});
  assert.equal(st.seq, 0);
});

test("断连回落 connecting，不假装还活着", () => {
  const { store } = newStore();
  store.handleFrame(JSON.stringify(snapshotFrame), "snapshot");
  store.setStatus("connecting");
  assert.equal(store.getSnapshot().status, "connecting");
});

test("destroy 之后不再通知订阅者（页面卸载不漏内存）", () => {
  const { store } = newStore();
  let n = 0;
  store.subscribe(() => {
    n += 1;
  });
  store.handleFrame(JSON.stringify(snapshotFrame), "snapshot");
  const before = n;
  store.destroy();
  store.handleFrame(JSON.stringify(snapshotFrame), "snapshot");
  assert.equal(n, before, "destroy 后不应再有通知");
});
