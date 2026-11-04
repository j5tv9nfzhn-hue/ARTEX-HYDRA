package agent

import (
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

func coordToolNames(tools []actool.CoreTool) map[string]bool {
	out := make(map[string]bool, len(tools))
	for _, t := range tools {
		out[t.Name()] = true
	}
	return out
}

// TestCoordToolsRootAndChildLifecycle 钉住根与子的生命周期出口：
// 两者都必须有派发/监控工具；根只能用 finish_task 收尾，子只能用 submit_report。
func TestCoordToolsRootAndChildLifecycle(t *testing.T) {
	ts := NewToolSet(nil, "")
	root := coordToolNames(ts.CoordTools(true))
	child := coordToolNames(ts.CoordTools(false))

	for _, name := range []string{ToolDelegateTask, ToolSendToAgent, ToolWaitForAgents, ToolAgentsGraph, ToolStopAgent} {
		if !root[name] {
			t.Fatalf("root 缺少编排工具 %s（当前 %v）", name, root)
		}
		if !child[name] {
			t.Fatalf("child 缺少编排工具 %s（当前 %v）", name, child)
		}
	}
	if !root[ToolFinishTask] {
		t.Fatalf("root 必须能用 finish_task 结束整个任务")
	}
	if root[ToolSubmitReport] {
		t.Fatalf("root 不应拿到 submit_report：它会把整个任务当成一次子任务交卷")
	}
	if !child[ToolSubmitReport] {
		t.Fatalf("child 必须能用 submit_report 交卷")
	}
	if child[ToolFinishTask] {
		t.Fatalf("child 不应拿到 finish_task：那会越权结束整个任务")
	}
}

// TestRootPromptForbidsTextOnlyTurns 对齐 strix：根不能停在纯文本回合，
// 必须用 delegate_task 派活、agents_graph 监控、finish_task 收尾。
func TestRootPromptForbidsTextOnlyTurns(t *testing.T) {
	p := RootSystemPrompt(CoordVars{Goal: "审计目标", MaxTurns: 20})
	for _, want := range []string{
		"delegate_task", "agents_graph", "wait_for_agents", "stop_agent", "finish_task",
		"almost every output MUST be a tool call", "A text-only turn does nothing",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("根提示词缺少关键约束 %q", want)
		}
	}
	if strings.Contains(p, "你手上没有执行工具") {
		t.Fatal("根提示词不应再声称自己没有执行工具：实际工具面包含通用执行工具，矛盾会让模型停在对话")
	}
}
