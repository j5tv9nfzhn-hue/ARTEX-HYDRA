import { createRevealController, SIDEBAR_REVEAL_CLOSE_DELAY, SIDEBAR_REVEAL_OPEN_DELAY } from "./sidebar-reveal.ts";
import assert from "node:assert/strict";
import test from "node:test";

/** 手动推进的假计时器：把「等 180ms」变成可断言的确定性行为。 */
function fakeScheduler() {
  let nextId = 1;
  const pending = new Map();
  let now = 0;

  const scheduler = {
    set(callback, ms) {
      const id = nextId++;
      pending.set(id, { callback, due: now + ms });
      return id;
    },
    clear(id) {
      pending.delete(id);
    },
  };

  return {
    scheduler,
    /** 推进时间并执行到期的计时器。 */
    advance(ms) {
      now += ms;
      for (const [id, item] of [...pending.entries()]) {
        if (item.due > now) continue;
        pending.delete(id);
        item.callback();
      }
    },
    pendingCount: () => pending.size,
  };
}

function setup(enabled = true) {
  const clock = fakeScheduler();
  const changes = [];
  const controller = createRevealController((revealed) => changes.push(revealed), clock.scheduler);
  controller.setEnabled(enabled);
  return { clock, changes, controller };
}

test("悬停超过开启延时后展开", () => {
  const { clock, controller, changes } = setup();

  controller.pointerEnter();
  clock.advance(SIDEBAR_REVEAL_OPEN_DELAY - 1);
  assert.equal(controller.isRevealed(), false, "未到延时不应展开");

  clock.advance(1);
  assert.equal(controller.isRevealed(), true);
  assert.deepEqual(changes, [true]);
});

test("指针扫过未达延时则完全不展开", () => {
  const { clock, controller, changes } = setup();

  controller.pointerEnter();
  clock.advance(80);
  controller.pointerLeave();
  clock.advance(1000);

  assert.equal(controller.isRevealed(), false, "扫过不应触发展开");
  assert.deepEqual(changes, [], "不应产生任何状态翻转");
});

test("关闭延时内折返不会收起，也不会重放展开动画", () => {
  const { clock, controller, changes } = setup();

  controller.pointerEnter();
  clock.advance(SIDEBAR_REVEAL_OPEN_DELAY);
  assert.equal(controller.isRevealed(), true);

  controller.pointerLeave();
  clock.advance(SIDEBAR_REVEAL_CLOSE_DELAY - 50);
  controller.pointerEnter();
  clock.advance(SIDEBAR_REVEAL_CLOSE_DELAY + 500);

  assert.equal(controller.isRevealed(), true, "折返后应保持展开");
  assert.deepEqual(changes, [true], "折返不应产生额外的状态翻转");
});

test("离开超过关闭延时后收起", () => {
  const { clock, controller, changes } = setup();

  controller.pointerEnter();
  clock.advance(SIDEBAR_REVEAL_OPEN_DELAY);
  controller.pointerLeave();
  clock.advance(SIDEBAR_REVEAL_CLOSE_DELAY);

  assert.equal(controller.isRevealed(), false);
  assert.deepEqual(changes, [true, false]);
});

test("关闭延时比开启延时更长", () => {
  assert.ok(SIDEBAR_REVEAL_CLOSE_DELAY > SIDEBAR_REVEAL_OPEN_DELAY, "关闭延时必须更长，否则折返会闪");
});

test("键盘聚焦立即展开，不等延时", () => {
  const { controller, changes } = setup();

  controller.focusIn();
  assert.equal(controller.isRevealed(), true);
  assert.deepEqual(changes, [true]);
});

test("焦点在侧栏内部移动不收起", () => {
  const { clock, controller } = setup();

  controller.focusIn();
  controller.focusOut(true);
  clock.advance(SIDEBAR_REVEAL_CLOSE_DELAY * 2);

  assert.equal(controller.isRevealed(), true, "内部移动不应收起");
});

test("焦点移出侧栏后收起", () => {
  const { clock, controller } = setup();

  controller.focusIn();
  controller.focusOut(false);
  clock.advance(SIDEBAR_REVEAL_CLOSE_DELAY);

  assert.equal(controller.isRevealed(), false);
});

test("Esc 立即收起并撤销待执行计时器", () => {
  const { clock, controller, changes } = setup();

  controller.pointerEnter();
  clock.advance(SIDEBAR_REVEAL_OPEN_DELAY);
  controller.escape();

  assert.equal(controller.isRevealed(), false);
  assert.equal(clock.pendingCount(), 0, "Esc 后不应残留计时器");
  assert.deepEqual(changes, [true, false]);
});

test("禁用后立即收起且不再响应悬停", () => {
  const { clock, controller, changes } = setup();

  controller.pointerEnter();
  clock.advance(SIDEBAR_REVEAL_OPEN_DELAY);
  controller.setEnabled(false);

  assert.equal(controller.isRevealed(), false);
  assert.deepEqual(changes, [true, false]);

  controller.pointerEnter();
  clock.advance(SIDEBAR_REVEAL_OPEN_DELAY * 2);
  assert.equal(controller.isRevealed(), false, "禁用状态不应展开");
  assert.equal(clock.pendingCount(), 0);
});

test("未启用时连计时器都不排", () => {
  const { clock, controller, changes } = setup(false);

  controller.pointerEnter();
  controller.focusIn();
  clock.advance(SIDEBAR_REVEAL_OPEN_DELAY * 2);

  assert.equal(clock.pendingCount(), 0);
  assert.equal(controller.isRevealed(), false);
  assert.deepEqual(changes, []);
});

test("重复进入不会叠加多个开启计时器", () => {
  const { clock, controller } = setup();

  controller.pointerEnter();
  controller.pointerEnter();
  controller.pointerEnter();
  assert.equal(clock.pendingCount(), 1, "应只有一个待执行的开启计时器");

  clock.advance(SIDEBAR_REVEAL_OPEN_DELAY);
  assert.equal(controller.isRevealed(), true);
});
