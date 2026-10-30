//go:build decommissioned

// 【P4 / B2：已下线】GET /api/tasks/{id}/reviewer-verdicts 的只读视图。
//
// Reviewer 的产物表保留（历史数据有审计价值），但新链路不再产生判决，
// 这个接口对新任务永远返回空列表，留着会误导前端与排障。
//
// 本文件已用构建约束排除出编译。
package server
