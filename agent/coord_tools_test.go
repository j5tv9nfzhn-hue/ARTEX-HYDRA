package agent

import (
	"path/filepath"
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

// TestCoordinatorToolsTakeNoResourceKeys 钉住调度工具**不参与资源锁**。
//
// wait_for_agents 会阻塞到子代理交卷（默认 300s）。如果它落到 keyGlobal 写锁，
// 整个进程的所有工具都会被挡在 global.RLock 上：子代理的 Bash/Read/record_fact
// 全部排队 → 子代理跑不完 → 父级等到超时。这是「命令卡顿 + 子代理不动」的
// 确定性成因，而且编译、单测、代码审查都不会报警，只有真实并发时才暴露。
func TestCoordinatorToolsTakeNoResourceKeys(t *testing.T) {
	ts := NewToolSet(nil, "")
	for _, tool := range ts.CoordTools(true) {
		reads, writes := toolAccessKeys(tool, nil, nil)
		if len(reads) != 0 || len(writes) != 0 {
			t.Fatalf("调度工具 %s 不应占用资源键，实得 reads=%v writes=%v", tool.Name(), reads, writes)
		}
	}
}

// TestCoordRootToolsHaveNoExecutionSurface 是「根只能指挥」的结构性门禁。
//
// 提示词里的“不要亲自执行”挡不住模型：只要 Bash 在面上，它就会先跑一条看看。
// 这里把根的面上没有执行器/写工具钉死；将来若有人为了“方便”把 Bash 加回去，
// 这条测试会直接失败。
func TestCoordRootToolsHaveNoExecutionSurface(t *testing.T) {
	got := coordToolNames(CoordRootTools(nil))
	for _, banned := range []string{"Bash", "Write", "Edit", "MultiEdit", "apply_patch", "insert_assets", "add_finding", "report_finding"} {
		if got[banned] {
			t.Fatalf("根工具面不应包含 %s（当前 %v）", banned, got)
		}
	}
	for _, want := range []string{"Read", "LS", "Glob", "Grep"} {
		if !got[want] {
			t.Fatalf("根工具面缺少只读工具 %s（当前 %v）", want, got)
		}
	}
}

// TestSessionRunnerAgentDirSeparatesChildren 钉住子代理的执行域隔离。
//
// 旧 worker 每个 intent 一个 runDir；coord 若让所有 agent 共用 <data>/work，
// Bash 的 keyShellOf 会把整棵树串行化。这条测试保证每个子代理拿到不同的目录，
// 也就是不同的 shell: 键。
func TestSessionRunnerAgentDirSeparatesChildren(t *testing.T) {
	r := &SessionRunner{WorkDir: filepath.Join(t.TempDir(), "work")}
	c := NewCoordinator("t")
	for _, reg := range []struct {
		id     string
		parent string
		root   bool
	}{
		{"root", "", true},
		{"recon-1", "root", false},
		{"sqli-1", "root", false},
	} {
		if _, err := c.Register(reg.id, reg.parent, "worker", reg.root, nil); err != nil {
			t.Fatalf("register %s: %v", reg.id, err)
		}
	}
	if got := r.agentDir("root", c); got != r.WorkDir {
		t.Fatalf("根应使用任务工作区 %q，实得 %q", r.WorkDir, got)
	}
	a, b := r.agentDir("recon-1", c), r.agentDir("sqli-1", c)
	if a == b {
		t.Fatalf("两个子代理不应共用执行目录 %q（Bash 会被串行化）", a)
	}
	if keyShellOf(a) == keyShellOf(b) {
		t.Fatalf("两个子代理的执行域键相同 %q（Bash 会被串行化）", keyShellOf(a))
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
