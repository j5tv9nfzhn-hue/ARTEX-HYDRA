//go:build decommissioned

// 【P4 / B4：已下线】Planner 是旧调度链路的规划者角色（561 行）。
//
// 它存在的理由是「有一个角色负责决定现在该想什么」：事件驱动唤醒、debounce、
// 心跳、空转跳过、终局收尾轮，外加 add_intent / kill_work / steer_work 三套
// 派发工具。新架构里这个问题的答案是「根 agent 自己决定」——它的"规划"就是
// 它的下一步动作，因此不需要一个独立的、带自己上下文与工具面的角色。
//
// 职责去向（见 agent/prompts_coord.go）：
//   派活   -> delegate_task
//   判目标 -> 根 agent 自己的 turn
//   回顾   -> 读自己的会话历史（它本来就有）
//
// 本文件已用构建约束排除出编译。原始实现随 git 历史保留。
// 恢复方式：删掉首行构建约束，并同时恢复 server/task_llm.go 的装配、server/engine.go
// 的 plannerLoop 与 agent/promptcatalog.go 的提示词种子。

package agent

/* 原始实现已随构建约束排除出编译，全文可从 git 历史检回：
   git show 8ce4ca37^:agent/planner.go
*/
