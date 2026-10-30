//go:build decommissioned

// 【P4 / B2：已下线】fetch_evidence：给 Planner 的唯一回溯通道（受限只读）。
//
// 它存在的唯一理由是 Reviewer 把 raw 关掉了——Planner 拿到的只有清洗后的结论，
// 所以需要一个小口子定向核对。Reviewer 与 Planner 都不在了，这个口子没有意义：
// 根 agent 能直接读子 agent 的交卷（submit_report 的 Brief）与 agents_graph。
//
// 连带：ReviewerEvidence / ReviewerEvidenceLoader / ReviewerEvidenceTouch 三个
// 钩子随之失去写入方与读取方。
//
// 本文件已用构建约束排除出编译。
package agent
