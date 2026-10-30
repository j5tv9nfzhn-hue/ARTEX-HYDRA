//go:build decommissioned

// 【P4 / B2：已下线】reviewer_verdicts / reviewer_evidence 两张表的存取层。
//
// 表本身**不删**：历史库里有真实数据，删建表语句会让升级路径丢审计。
// 但写入方与读取方都随 Reviewer 一起下线，因此存取层不必参与编译。
//
// 本文件已用构建约束排除出编译。
package db
