//go:build decommissioned

// 【P4 / B2：已下线】Reviewer 是第四角色：单次 LLM 调用、无 harness 循环、
// 不持有任何工具，唯一职责是把 Worker 的交卷"清洗"成判决（Fact/Clue/Verdict）。
//
// 它存在的根本原因是**不信任 Agent 的自述**：原始输出被判定为不可直接采信，
// 于是插入一个外部模型做语义桥接。新架构的判断是——这个不信任应该用**结构**
// 解决，而不是用第二个模型解决：submit_report 的 outcome 与 open_items 是必填
// 字段，交卷方必须自己给出结论与未闭环项，等待方直接读结构化结果。
//
// 本文件已用构建约束排除出编译。原始实现随 git 历史保留（926 行）。
// 恢复方式：删掉首行构建约束，并同时恢复 server/reviewer.go 与 handoff.go 的调用。
package agent
