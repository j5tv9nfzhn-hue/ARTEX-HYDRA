package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

func names(tools []actool.CoreTool) map[string]actool.CoreTool {
	m := map[string]actool.CoreTool{}
	for _, t := range tools {
		m[t.Name()] = t
	}
	return m
}

// TestToolResolveKeepsCoordinatorTools：旧库里即使残留 delegate_task 等同名行，
// 也不能把调度层工具裁掉——根 agent 变回"只能对话"的根因之一就是这里。
func TestToolResolveKeepsCoordinatorTools(t *testing.T) {
	pg, err := db.Open("file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "coord-tools.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	originalResolve, originalTrafficBinding := agent.ToolResolve, agent.FindingTrafficBindingEnabled
	wireTools(pg, nil)
	t.Cleanup(func() {
		agent.ToolResolve = originalResolve
		agent.FindingTrafficBindingEnabled = originalTrafficBinding
	})
	t.Setenv("AGENT_CORE_DISABLE_INTERACTIVE_SHELL", "1")

	// 模拟历史行：enabled=false 且没有绑定任何 agent。
	if err := pg.SeedTool("delegate_task", "stale row", mustJSON(map[string]any{}), mustJSON([]string{})); err != nil {
		t.Fatal(err)
	}
	if err := pg.UpdateTool("delegate_task", "stale row", mustJSON(map[string]any{}), mustJSON([]string{}), false); err != nil {
		t.Fatal(err)
	}

	ts := agent.NewToolSet(nil, "")
	root := names(agent.ToolResolve(context.Background(), "worker", ts.CoordTools(true)))
	for _, name := range []string{agent.ToolDelegateTask, agent.ToolWaitForAgents, agent.ToolAgentsGraph, agent.ToolStopAgent, agent.ToolFinishTask} {
		if _, ok := root[name]; !ok {
			t.Fatalf("调度工具 %s 被历史 DB 行裁掉（当前 %v）", name, root)
		}
	}
}

func TestWireTools(t *testing.T) {
	pg, err := db.Open("file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "tools-wire.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	originalResolve, originalTrafficBinding := agent.ToolResolve, agent.FindingTrafficBindingEnabled
	wireTools(pg, nil)
	t.Cleanup(func() {
		agent.ToolResolve = originalResolve
		agent.FindingTrafficBindingEnabled = originalTrafficBinding
	})
	// Prevent interactive-shell Bash decoration: if the worker agent has
	// interactive_shell=true in the DB, ToolResolve would append a note to
	// Bash's description. Disable that for this test so the undecorated
	// pass-through assertion holds regardless of DB state.
	t.Setenv("AGENT_CORE_DISABLE_INTERACTIVE_SHELL", "1")

	// Seeding populated the catalog.
	rf, err := pg.GetTool("record_fact")
	if err != nil || rf == nil {
		t.Fatalf("record_fact not seeded: %v", err)
	}

	// Build a base as the worker does: its domain tools + defaults, resolved for
	// "worker" then for "planner".
	ts := agent.NewToolSet(nil, "")
	base := append(ts.WorkerTools(), actool.DefaultTools()...)
	ctx := context.Background()

	worker := names(agent.ToolResolve(ctx, "worker", base))
	if _, ok := worker["record_fact"]; !ok {
		t.Error("worker lost record_fact")
	}
	// SDK generic tools aren't seeded → ToolResolve passes them through unchanged
	// (same object, original description). Two decorations ARE by design and are
	// therefore part of the expected string:
	//   1. 平台环境提示 platformShellNote (#9)，非 bash 平台（Windows → PowerShell）追加
	//   2. 【写文件】指引 bashWriteNote，按「这个 agent 实际拿到的工具集」生成 ——
	//      worker 默认被裁掉 Write/Edit，写死文案会让模型去调不存在的工具。
	// 除这两条外不得再有任何装饰：任何第三处改写都说明又有人在全局改 SDK 工具描述。
	// No startup prune touches non-catalog rows, so future user-defined custom
	// tools survive too.
	if bash, ok := worker["Bash"]; !ok {
		t.Error("worker lost Bash (should pass through)")
	} else if want := actool.NewBash().Description() + bashWriteNote(base) + platformShellNote(); bash.Description() != want {
		t.Errorf("Bash should carry only the two by-design notes, got %q want %q", bash.Description(), want)
	}
	// 第六阶段（626bda05）把 PlannerTools 收敛成与上游逐项一致的 20 个，其中不含
	// record_fact —— 记录事实是 mainagent（root 编排者）的职责，rootTmpl 里要求
	// 沉淀结论的正是它。这里守住的是「planner 面不被悄悄加回写工具」，以及
	// 「写事实的能力没有整体消失」：后者由 mainagent 这一侧的断言保证。
	planner := names(agent.ToolResolve(ctx, "planner", base))
	if _, ok := planner["record_fact"]; ok {
		t.Error("planner regained record_fact; 第六阶段的 planner 面不应含写工具")
	}
	mainagent := names(agent.ToolResolve(ctx, "mainagent", base))
	if _, ok := mainagent["record_fact"]; !ok {
		t.Error("mainagent lost record_fact —— rootTmpl 明确要求编排者用它沉淀结论")
	}

	// Edit description + add a default to a scalar param, then confirm the resolved
	// worker tool reflects both. Restore from code default on cleanup.
	t.Cleanup(func() { _ = pg.UpsertToolForce(rf.Key, rf.Description, rf.Schema, mustJSON(rf.Agents)) })
	var schema map[string]any
	_ = json.Unmarshal(rf.Schema, &schema)
	if props, ok := schema["properties"].(map[string]any); ok {
		if conf, ok := props["confidence"].(map[string]any); ok {
			conf["default"] = "inferred"
		}
	}
	edited := mustJSON(schema)
	if err := pg.UpdateTool(rf.Key, "EDITED DESC", edited, mustJSON([]string{"worker"}), true); err != nil {
		t.Fatal(err)
	}

	// An explicit worker-only edit must override the broader code default.
	planner = names(agent.ToolResolve(ctx, "planner", base))
	if _, ok := planner["record_fact"]; ok {
		t.Error("explicit worker-only binding leaked to planner")
	}
	worker2 := names(agent.ToolResolve(ctx, "worker", base))
	got := worker2["record_fact"]
	if got == nil {
		t.Fatal("record_fact missing after edit")
	}
	if got.Description() != "EDITED DESC" {
		t.Errorf("description = %q, want EDITED DESC", got.Description())
	}
	// Default injection: omit confidence → handler input should carry the default.
	// We can't run the real handler (nil store), but InputSchema must show the default.
	sc := got.InputSchema()
	props := sc["properties"].(map[string]any)
	conf := props["confidence"].(map[string]any)
	if conf["default"] != "inferred" {
		t.Errorf("confidence.default = %v, want inferred", conf["default"])
	}
	if err := pg.UpdateTool(rf.Key, "EDITED DESC", edited, mustJSON([]string{"worker", "planner"}), false); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"worker", "planner"} {
		resolved := names(agent.ToolResolve(ctx, key, base))
		if _, ok := resolved["record_fact"]; ok {
			t.Errorf("disabled tool leaked to %s", key)
		}
	}

}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
