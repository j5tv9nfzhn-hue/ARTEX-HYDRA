//go:build decommissioned

// 【P4 / B2：已下线】Reviewer 的接线层：把交接单送进 agent.Review、把判决落
// reviewer_verdicts 表、写线索，以及"先落库再唤醒 Planner"的硬闸门
// （awaitReviewerVerdict）与它的熔断降级（degradeReviewerVerdict）。
//
// 闸门这段复杂度完全由"交卷必须经过中间角色"这一设计产生：既然唤醒必须等
// 判决落库，就得有超时、熔断、降级三种兜底。交卷变成一条消息之后，它没有存在意义。
//
// 本文件已用构建约束排除出编译。恢复方式：删掉首行构建约束。
package server
