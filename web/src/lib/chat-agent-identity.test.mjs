import {
  conversationAgentLabel,
  draftAgentKey,
  isLegacyAgentKey,
  isRootAgentKey,
  LEGACY_AGENT_KEYS,
  ROOT_AGENT_LABEL,
  singleAgentSelection,
  singleThreadGroup,
} from "./chat-agent-identity.ts";
import assert from "node:assert/strict";
import test from "node:test";

/**
 * 对话页「单一根 Agent」口径的单测（Issue #6 §4-A）。
 *
 * 这些规则原本是 page.tsx 里的内联表达式：
 *   const agentLabel = agent?.name ?? agents[0]?.name ?? "主 Agent";
 *   const chatAgents = rootAgent ? [rootAgent] : [];
 * 三层兜底里没有一层是产品要的（会把"主 Agent"这种旧术语、以及别的 agent 的
 * 名字漏到界面上），而且没法测。抽出来之后，下面每条断言就是"不能改回去"的约束。
 */

test("对话页身份恒为根 Agent，与传入的 agent 清单无关", () => {
  // 关键：签名刻意不收参数 —— 一旦有人想按 agent 取名字，必须先改这个函数签名，
  // 那时就会看到这段注释。
  assert.equal(conversationAgentLabel(), ROOT_AGENT_LABEL);
  assert.equal(ROOT_AGENT_LABEL, "根 Agent");
});

test("根身份识别：coord_root 与 worker 都算根", () => {
  assert.equal(isRootAgentKey("coord_root"), true);
  assert.equal(isRootAgentKey("worker"), true, "worker 是 DefaultCoordRootAgentKey，同一份运行配置");
  assert.equal(isRootAgentKey("coord_child"), false, "执行者是子代理，不是对话页身份");
  assert.equal(isRootAgentKey("mainagent"), false, "已下线");
  assert.equal(isRootAgentKey(""), false);
  assert.equal(isRootAgentKey(null), false);
  assert.equal(isRootAgentKey(undefined), false);
});

test("新线程的 agent_key：优先 coord_root，其次 worker，绝不返回已下线角色", () => {
  assert.equal(draftAgentKey([{ key: "coord_root" }]), "coord_root");
  assert.equal(draftAgentKey([{ key: "worker" }, { key: "coord_root" }]), "worker", "清单顺序就是优先级");
  // 只有子代理时退化为它（可跑），但**绝不**落到已下线的角色上。
  assert.equal(draftAgentKey([{ key: "coord_child" }]), "coord_child");
  assert.equal(draftAgentKey([{ key: "planner" }, { key: "coord_child" }]), "coord_child", "跳过已下线角色");
  assert.equal(draftAgentKey([]), "", "空清单返回空串，调用方补字面量兜底");
  for (const legacy of LEGACY_AGENT_KEYS) {
    assert.notEqual(draftAgentKey([{ key: legacy }]), legacy, `不应把新线程落到已下线角色 ${legacy}`);
  }
});

test("草稿页可选列表恒为 0 或 1 项（没有 agent 选择器）", () => {
  const many = [{ key: "coord_root" }, { key: "worker" }, { key: "coord_child" }, { key: "custom-1" }];
  const picked = singleAgentSelection(many);
  assert.equal(picked.length, 1);
  assert.equal(picked[0].key, "coord_root", "应优先取根，而不是清单第一项");
  assert.deepEqual(singleAgentSelection([]), []);
  assert.equal(singleAgentSelection([{ key: "custom-only" }]).length, 1, "没有根时退化为唯一项");
});

test("左侧栏恒为单组，组名是产品名而不是内部 key", () => {
  const g = singleThreadGroup();
  assert.equal(g.label, ROOT_AGENT_LABEL);
  assert.equal(g.key, "root");
  // 组名绝不能是 agent_key：这就是旧 groupByAgent 把 "mainagent" 漏到界面的原因。
  assert.notEqual(g.label, "coord_root");
  assert.notEqual(g.label, "worker");
});

test("已下线角色可识别（用于给历史线程打标记）", () => {
  for (const k of ["planner", "mainagent", "reviewer", "auto", "pentest"]) {
    assert.equal(isLegacyAgentKey(k), true, `${k} 应被识别为已下线`);
  }
  assert.equal(isLegacyAgentKey("coord_root"), false);
  assert.equal(isLegacyAgentKey("worker"), false);
  assert.equal(isLegacyAgentKey("coord_child"), false);
});
