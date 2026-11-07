package agent

// 调度层工具面的**最终闸门**（角色隔离的可验证落点）。
//
// 背景：`server/engine_coord_wire.go` 用 `opts.ToolsFor(isRoot)` 给根下发
// CoordRootTools（只读 + 记账），子 agent 拿完整执行面。但那条路径**不是唯一的**
// 注入点：装配随后会走 `AugmentTools`，而它会把命中的三层工具再 append 回工具面：
//
//	1. 技能 / MCP（ToolAugment 按 agent_visibility 注入）
//	2. 宿主工具（hostTools：流量录制 + 跨任务编排 + 自定义工具）
//	3. 平台工具（platformTools：create_skill / create_mcp / create_custom_tool …）
//
// 只要有任意一条 agent_visibility 或 tools 表绑定把上述任一项给了根，根就会重新
// 拿到"能自己动手"的能力——Bash 一旦回到根手里，"根不亲自执行"就退回成一句建议，
// 而模型永远会选成本更低的那条路（自己跑命令而不是派子代理）。
//
// 所以在 `AugmentTools` 之后、`ToolResolve` 之后再加一道**名字白名单**：
// 根的工具面 = CoordRootTools 的成员 ∪ 协调器工具（delegate_task / finish_task …）。
// 白名单是结构性约束：DB 里怎么配都不会把执行面漏回给根。
//
// 为什么白名单而不是黑名单：黑名单要求穷举所有"危险工具"，漏一个就漏一条路；
// 而以后新增任何工具时，白名单的默认行为是"根看不到"，需要显式加白才能给根，
// 这是安全的方向。

import (
	"log"
	"sort"
	"strings"

	actool "github.com/Autumn-27/norma/tool"
)

// logRoleToolFaceDrop 记录一次"根工具面被收口"的事实。
//
// 只在真的有工具被摘掉时打，且按名字排序——装配每轮都会跑一次，日志要能被
// grep + 去重；顺序随 map 迭代变化会让"同一件事"在日志里长得像两件事。
func logRoleToolFaceDrop(label string, dropped []string) {
	sort.Strings(dropped)
	log.Printf("[toolface] %s: 根工具面收口，摘掉 %d 个非只读/非调度工具：%s",
		label, len(dropped), strings.Join(dropped, ", "))
}

// rootAllowedExtraTools 是根**额外**允许的协调器工具。
//
// 这些不是执行能力，而是"调度 + 交卷"的出口：没有它们根无法派活，也无法结束任务。
// 名字一律用常量（见 coord_tools.go），写错一个字不会报错、只会静默地少一个出口。
var rootAllowedExtraTools = map[string]bool{
	ToolDelegateTask:  true,
	ToolSendToAgent:   true,
	ToolWaitForAgents: true,
	ToolAgentsGraph:   true,
	ToolStopAgent:     true,
	ToolFinishTask:    true,
	// submit_report 是**子**的出口，但装配层用同一个白名单函数兜底时可能遇到
	// 根被误配成子键的历史数据；留着它不会让根获得执行能力（它只结束自己）。
	ToolSubmitReport: true,
	// get_tool_help 是瘦身层注入的兜底帮助入口（tool_lean.go），不含执行能力。
	"get_tool_help": true,
}

// rootReadOnlyToolNames 是 CoordRootTools 下发的"只读 + 记账"工具名。
//
// 【为什么不直接调 CoordRootTools(nil) 生成】它有一句 `if ts == nil { return out }`
// 的早退，只用它生成白名单会**漏掉 6 个图谱工具**（list_findings / list_assets /
// node_detail / search_all_worker_traces / get_worker_trace / record_fact），
// 于是根会被自己的白名单摘掉"看漏洞"的能力——第一次实现就是这么错的，
// 日志里看得一清二楚（"摘掉 24 个…list_findings, node_detail, record_fact"）。
//
// 所以这里显式列名，并用 TestRootAllowlistMatchesCoordRootTools（喂一个真实
// ToolSet）锁住两边一致：CoordRootTools 增删工具时测试会红，提醒同步这里。
var rootReadOnlyToolNames = []string{
	"Read", "LS", "Glob", "Grep",
	"list_findings", "list_assets", "node_detail",
	"search_all_worker_traces", "get_worker_trace", "record_fact",
}

// rootToolAllowlist 由两张表拼成：只读面 + 调度面。
func rootToolAllowlist() map[string]bool {
	allow := make(map[string]bool, len(rootAllowedExtraTools)+len(rootReadOnlyToolNames))
	for name := range rootAllowedExtraTools {
		allow[name] = true
	}
	for _, name := range rootReadOnlyToolNames {
		allow[name] = true
	}
	return allow
}

// EnforceRoleToolFace 是根工具面的最后一道过滤，按角色收口。
//
// isRoot=false 时原样返回（子 agent 保留完整执行面）。
// isRoot=true 时只保留白名单里的工具，并把被摘掉的工具名记进日志——
// "根看到了什么、被摘了什么"必须可对账，否则又变成一处静默行为。
//
// 返回的切片是新分配的，不会改写调用方的底层数组（装配层会复用 base 切片
// 装配多个 agent，就地过滤会把子 agent 的工具也删掉）。
func EnforceRoleToolFace(tools []actool.CoreTool, isRoot bool, label string) []actool.CoreTool {
	if !isRoot {
		return tools
	}
	allow := rootToolAllowlist()
	out := make([]actool.CoreTool, 0, len(tools))
	var dropped []string
	for _, t := range tools {
		if t == nil {
			continue
		}
		if allow[t.Name()] {
			out = append(out, t)
			continue
		}
		dropped = append(dropped, t.Name())
	}
	if len(dropped) > 0 {
		logRoleToolFaceDrop(label, dropped)
	}
	return out
}

// RootToolFaceNames 返回根应该看到的工具名集合，供启动自检与测试对比。
func RootToolFaceNames() []string {
	allow := rootToolAllowlist()
	out := make([]string, 0, len(allow))
	for name := range allow {
		out = append(out, name)
	}
	return out
}
