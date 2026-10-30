//go:build decommissioned

// 【P4 / B4：测试随被测角色一起下线】它用 Worker 验证输出语言指令，Worker 已下线。
//
// 测试仍然有效——它测的行为是对的，只是被删掉的角色不再承载这些行为。
// 若将来恢复旧链路，删掉首行构建约束即可一并恢复。

package agent
