package agent

// 根工具面隔离的回归测试（Issue #6 §2 / §4-A）。
//
// 要锁死的不变量只有一条：**根永远拿不到执行面**。
// 具体到代码，就是 AssembleCoordSurface 里 AugmentTools 之后那道 EnforceRoleToolFace。
// 之前这条约束只在 base 上做过，AugmentTools 会把技能 / MCP / 宿主 / 平台工具
// 再 append 回来，于是根重新拿到 create_skill、create_mcp 这类"能自己动手"的工具。

import (
	"context"
	"encoding/json"
	"testing"

	permission "github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// stubTool 是一个只有名字的最小 CoreTool，用来构造"被误绑给根"的工具集。
// 语义上按"执行面工具"处理（非只读、非并发安全），因为本文件测的正是
// "执行面不能漏给根"——把 stub 写成只读会让断言失去意义。
type stubTool struct{ name string }

func (s stubTool) Name() string                           { return s.name }
func (s stubTool) Description() string                    { return s.name }
func (s stubTool) Prompt() string                         { return "" }
func (s stubTool) InputSchema() map[string]any            { return map[string]any{"type": "object"} }
func (s stubTool) IsReadOnly(json.RawMessage) bool        { return false }
func (s stubTool) IsConcurrencySafe(json.RawMessage) bool { return false }
func (s stubTool) CheckPermissions(context.Context, json.RawMessage, permission.Context) permission.Decision {
	return permission.Decision{Behavior: permission.Allow}
}
func (s stubTool) Call(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
	return actool.Result{}, nil
}

// TestRootToolFaceDropsExecutionTools 是本次修复的核心断言。
//
// 构造一个"装配后被污染的根工具面"：里面既有 CoordRootTools 该有的只读工具，
// 也有 AugmentTools 会注入的 platformTools / hostTools / 自定义 Bash。过滤后
// 允许的必须留下、执行面必须全部消失。
func TestRootToolFaceDropsExecutionTools(t *testing.T) {
	// 允许的（只读 + 调度 + 交卷）
	keep := []string{
		"Read", "LS", "Glob", "Grep",
		"list_findings", "list_assets", "node_detail",
		"search_all_worker_traces", "get_worker_trace", "record_fact",
		ToolDelegateTask, ToolSendToAgent, ToolWaitForAgents,
		ToolAgentsGraph, ToolStopAgent, ToolFinishTask,
	}
	// 必须被摘掉的（执行面 + 平台管理面）
	drop := []string{
		"Bash", "Write", "Edit", "MultiEdit", "NotebookEdit", "apply_patch",
		"report_finding", "insert_assets", "delete_assets_by_host",
		"create_skill", "update_skill_file", "create_custom_tool", "create_mcp",
		"spawn_task", "kill_worker", "restart_worker",
		"mcp__rea__scan", "custom-shell-runner",
	}
	var in []actool.CoreTool
	for _, n := range keep {
		in = append(in, stubTool{n})
	}
	for _, n := range drop {
		in = append(in, stubTool{n})
	}

	out := EnforceRoleToolFace(in, true, "test-root")
	got := map[string]bool{}
	for _, tl := range out {
		got[tl.Name()] = true
	}
	for _, n := range keep {
		if !got[n] {
			t.Errorf("根应保留 %q（只读/调度），实际被摘掉了", n)
		}
	}
	for _, n := range drop {
		if got[n] {
			t.Errorf("根不应拿到 %q —— 根一旦有它就能自己动手，隔离失效", n)
		}
	}
	if len(out) != len(keep) {
		t.Errorf("过滤后应恰好 %d 个工具，得到 %d", len(keep), len(out))
	}
}

// TestChildKeepsFullToolFace：子 agent 必须原样拿到完整执行面（同一个函数的另一侧）。
func TestChildKeepsFullToolFace(t *testing.T) {
	in := []actool.CoreTool{stubTool{"Bash"}, stubTool{"Write"}, stubTool{"report_finding"}}
	out := EnforceRoleToolFace(in, false, "test-child")
	if len(out) != len(in) {
		t.Fatalf("子 agent 工具面不应被裁剪：%d → %d", len(in), len(out))
	}
	if &out[0] != &in[0] {
		t.Fatalf("非根分支应原样返回，不应重新分配")
	}
}

// TestEnforceDoesNotMutateCallerSlice：装配层会复用 base 切片装配多个 agent，
// 就地过滤会把子 agent 的工具一起删掉。这条防的就是那种"看起来对、实际串味"。
func TestEnforceDoesNotMutateCallerSlice(t *testing.T) {
	in := []actool.CoreTool{stubTool{"Read"}, stubTool{"Bash"}}
	before := len(in)
	_ = EnforceRoleToolFace(in, true, "test-root")
	if len(in) != before {
		t.Fatalf("调用方切片被改写了：%d → %d", before, len(in))
	}
	if in[1].Name() != "Bash" {
		t.Fatalf("调用方切片内容被改写了：%v", in[1].Name())
	}
}

// TestRootAllowlistMatchesCoordRootTools：白名单必须与 CoordRootTools 同步。
//
// 这里必须喂一个**真实 ToolSet**，不能传 nil：CoordRootTools(nil) 会早退成
// 4 个内置工具，用它做对照等于什么都没验证（第一版就是这么漏掉 6 个图谱工具的）。
func TestRootAllowlistMatchesCoordRootTools(t *testing.T) {
	allow := rootToolAllowlist()
	ts := NewToolSet(nil, "")
	for _, tl := range CoordRootTools(ts) {
		if !allow[tl.Name()] {
			t.Errorf("CoordRootTools 里的 %q 不在白名单里，会被误摘", tl.Name())
		}
	}
	// 反向：白名单里的每个只读名都必须真的能由 CoordRootTools 产出，
	// 防止白名单写成"过时快照"（工具改名后白名单留着旧名字，等于悄悄放开一个洞）。
	present := map[string]bool{}
	for _, tl := range CoordRootTools(ts) {
		present[tl.Name()] = true
	}
	for _, name := range rootReadOnlyToolNames {
		if !present[name] {
			t.Errorf("白名单里的只读工具 %q 已不在 CoordRootTools 的工具面里（改名或删除？）", name)
		}
	}
	for _, name := range []string{ToolDelegateTask, ToolFinishTask, ToolWaitForAgents, ToolSendToAgent} {
		if !allow[name] {
			t.Errorf("调度工具 %q 不在白名单里，根将无法派活/交卷", name)
		}
	}
	// 反向：执行面绝不能被白名单放进来。
	for _, name := range []string{"Bash", "Write", "Edit", "create_skill", "create_mcp"} {
		if allow[name] {
			t.Errorf("白名单里不应出现执行面工具 %q", name)
		}
	}
}

// TestRootToolFaceNamesNonEmpty 是启动自检用的口径：白名单不能为空，
// 否则根会装配出一个"什么都没有"的工具面，然后所有任务都卡在第一步。
func TestRootToolFaceNamesNonEmpty(t *testing.T) {
	if names := RootToolFaceNames(); len(names) < 10 {
		t.Fatalf("根工具面白名单过小（%d）：装配出来会是空面", len(names))
	}
}
