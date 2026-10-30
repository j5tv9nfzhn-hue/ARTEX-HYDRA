//go:build decommissioned

// 【P4 / B4：已下线】MainAgent 是旧架构里"任务页对话的另一端"（228 行）。
//
// 它的问题不是代码质量，而是**位置**：coord 模式下任务由根 agent 编排，而人类
// 在任务页说的话却投给这个独立的 MainAgent，两边上下文互不相通。它手上的
// add_intent / steer_work / kill_work 全是旧链路派发工具——写进库不会有角色
// 来领，正在跑的根 agent 也不知道有人说过话。
//
// 现在的做法（server.go 的对话改道）：coord 模式下人类消息以 MsgInstruction
// 直接投给 ct.run.RootID，与 nudgeCoordRoot 同一语义。对话的"另一端"就是
// 正在干活的根 agent 本身，不再需要第二个角色。
//
// 本文件已用构建约束排除出编译。原始实现随 git 历史保留。
// 恢复方式：删掉首行构建约束，并恢复 server/task_llm.go 的 NewMainAgent 装配
// 与 server/server.go 对话入口的 ma.Chat 分支。

package agent

/* 原始实现已随构建约束排除出编译，全文可从 git 历史检回：
   git show 8ce4ca37^:agent/mainagent.go
*/
