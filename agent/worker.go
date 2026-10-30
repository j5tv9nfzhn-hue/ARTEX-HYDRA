//go:build decommissioned

// 【P4 / B4：已下线】Worker 是旧调度链路的执行者角色（776 行）。
//
// 它按「一条意图一个 Worker」的方式执行：从 frontier 领取意图、跑 harness 循环、
// 跑完结算。新架构里这项工作由 coord 的子 agent 承担，走的是
// agent/runner_coord.go 的 SessionRunner —— 两者的差别不在"谁干活"，而在
// **生命周期由谁掌握**：旧 Worker 由 workerLoop 从队列里领取，新子 agent 由
// 根 agent 显式 delegate_task 派生并在交卷时结束。
//
// 注意：WorkerSessionID 与 WebSearchOpts 两个符号**不属于**这个角色，它们是
// 跨角色共享的基础设施，已迁至 agent/shared_opts.go 并以完全相同的行为保留。
//
// 本文件已用构建约束排除出编译。原始实现随 git 历史保留。

package agent

/* 原始实现已随构建约束排除出编译，全文可从 git 历史检回：
   git show 8ce4ca37^:agent/worker.go
*/
