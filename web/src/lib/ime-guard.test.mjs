import assert from "node:assert/strict";
import test from "node:test";

import { isGhostEnter, markCompositionEnd } from "./ime-guard.ts";

// 这组用例守的是「按回车没反应、要再按一次 / 点一下才发出去」那个回归。
//
// 幽灵键窗口原本是 500ms：用户按下 Enter 确认候选词后，compositionend 记下时间戳，
// 紧接着真正用来发送的那次 Enter 落在窗口内，被当成伴生键吞掉。窗口收到 60ms 后，
// 只有浏览器在同一次任务里补发的伴生键会被拦，人的第二次按键不会。

const enter = { key: "Enter" };

test("紧贴 compositionend 的 Enter 视为伴生键，拦一次", () => {
  markCompositionEnd();
  assert.equal(isGhostEnter(enter), true, "同一个任务里的伴生键应被拦下");
  assert.equal(isGhostEnter(enter), false, "拦下后必须清零，不能继续吞后续按键");
});

test("确认候选词后隔一会儿再按 Enter，必须放行", async () => {
  markCompositionEnd();
  await new Promise((r) => setTimeout(r, 90));
  assert.equal(isGhostEnter(enter), false, "90ms 后再按的 Enter 是用户真正想发送的那次");
});

test("非 Enter 键不受影响", () => {
  markCompositionEnd();
  assert.equal(isGhostEnter({ key: "a" }), false);
  // 上面那次没有消费时间戳；Enter 仍应被当作伴生键。
  assert.equal(isGhostEnter(enter), true);
});
