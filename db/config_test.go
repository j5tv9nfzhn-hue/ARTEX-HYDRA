package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// TestPoolProfilesOrder pins the failover chain query: keyless profiles can't
// serve a request and excluded ones aren't fallback targets, so neither belongs
// in the chain; the rest come back by priority, highest first.
// Deliberately does NOT touch is_default — flipping the active profile would be a
// side effect on the shared dev database.
func TestPoolProfilesOrder(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	mk := func(name string, priority int, exclude bool, key string) int64 {
		id, err := d.SaveProfile(&LLMProfile{
			Name: name, Format: "openai", Model: "m", APIKey: key,
			Priority: priority, PoolExclude: exclude,
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { d.Exec(`DELETE FROM llm_profiles WHERE id=$1`, id) })
		return id
	}
	lo := mk("t-pool-lo", 1, false, "k1")
	hi := mk("t-pool-hi", 9, false, "k2")
	mk("t-pool-excluded", 99, true, "k3") // excluded despite the top priority
	mk("t-pool-nokey", 50, false, "")     // no key → cannot serve anything

	chain, err := d.PoolProfiles()
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, p := range chain {
		switch p.Name {
		case "t-pool-lo", "t-pool-hi":
			got = append(got, p.ID)
		case "t-pool-excluded":
			t.Fatal("pool_exclude profile entered the failover chain")
		case "t-pool-nokey":
			t.Fatal("keyless profile entered the failover chain")
		}
	}
	if len(got) != 2 || got[0] != hi || got[1] != lo {
		t.Fatalf("chain order = %v, want [hi=%d lo=%d]", got, hi, lo)
	}
}

func TestDeleteProfileContextHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var d DB
	if err := d.DeleteProfileContext(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteProfileContext error=%v, want context cancellation", err)
	}
}

// TestRefreshPromptIfSystemDefault：代码默认模板只在「当前版本仍由系统维护」时
// 自动追平；用户通过 UI 保存过的版本必须原样保留。
func TestRefreshPromptIfSystemDefault(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ag, err := d.GetAgentByKey("worker")
	if err != nil || ag == nil {
		t.Fatalf("worker agent: %v", err)
	}
	defer func() {
		d.Exec(`DELETE FROM agent_prompts WHERE agent_id=$1`, ag.ID)
		d.Exec(`UPDATE agents SET current_prompt_id=NULL WHERE id=$1`, ag.ID)
	}()

	if _, err := d.SavePrompt(ag.ID, "旧系统模板", "init", "system"); err != nil {
		t.Fatal(err)
	}
	n, err := d.RefreshPromptIfSystemDefault(ag.ID, "新系统模板")
	if err != nil || n == 0 {
		t.Fatalf("系统模板应被刷新: n=%d err=%v", n, err)
	}
	if cur, _ := d.CurrentPrompt(ag.ID); cur != "新系统模板" {
		t.Fatalf("刷新后模板=%q", cur)
	}

	if _, err := d.SavePrompt(ag.ID, "用户自定义", "edit", "ui"); err != nil {
		t.Fatal(err)
	}
	if n, err := d.RefreshPromptIfSystemDefault(ag.ID, "代码最新模板"); err != nil || n != 0 {
		t.Fatalf("用户编辑过的模板不应被覆盖: n=%d err=%v", n, err)
	}
	if cur, _ := d.CurrentPrompt(ag.ID); cur != "用户自定义" {
		t.Fatalf("用户模板被覆盖: %q", cur)
	}
}

func TestConfigStores(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	// LLM profile: save, active, key never serialized
	pid, err := d.SaveProfile(&LLMProfile{Name: "t-default", Format: "openai", Model: "gpt-x", APIKey: "secret123"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM llm_profiles WHERE id=$1`, pid)
	if err := d.SetActiveProfile(pid); err != nil {
		t.Fatal(err)
	}
	// 激活中的模型允许删（删完激活位交接给下一个候选），但不能把它变成
	// 「删了却还留在内存里」的幽灵：删之前先确认读得出来。
	act, err := d.ActiveProfile()
	if err != nil || act == nil || act.APIKey != "secret123" {
		t.Fatalf("active profile/key: %+v err=%v", act, err)
	}
	// list must hide the key, expose hint
	list, _ := d.ListProfiles()
	for _, p := range list {
		if p.ID == pid {
			b, _ := json.Marshal(p)
			if string(b) == "" || contains(string(b), "secret123") {
				t.Fatalf("api key leaked in list json: %s", b)
			}
			if p.APIKeyHint != "…t123" {
				t.Fatalf("hint want …t123, got %q", p.APIKeyHint)
			}
		}
	}

	// agents seeded; prompt versioning
	ag, err := d.GetAgentByKey("worker")
	if err != nil || ag == nil {
		t.Fatalf("worker agent: %v", err)
	}
	v1, err := d.SavePrompt(ag.ID, "你是根 Agent {{.ProxyAddr}}", "init", "test")
	if err != nil {
		t.Fatal(err)
	}
	v2, _ := d.SavePrompt(ag.ID, "你是根 Agent v2 {{.ProxyAddr}} {{.WorkerName}}", "edit", "test")
	if v2 != v1+1 {
		t.Fatalf("version should increment: %d -> %d", v1, v2)
	}
	cur, _ := d.CurrentPrompt(ag.ID)
	if cur != "你是根 Agent v2 {{.ProxyAddr}} {{.WorkerName}}" {
		t.Fatalf("current prompt wrong: %q", cur)
	}
	vers, _ := d.ListPromptVersions(ag.ID)
	if len(vers) < 2 {
		t.Fatalf("want >=2 versions, got %d", len(vers))
	}
	pv, _ := d.PromptVars(ag.ID)
	// worker（根 Agent 的运行配置行）must have at least the seeded catalog vars (ProxyAddr, WorkerName)
	if len(pv) < 2 {
		t.Fatalf("worker catalog want >=2 vars, got %d", len(pv))
	}
	d.Exec(`DELETE FROM agent_prompts WHERE agent_id=$1`, ag.ID)
	d.Exec(`UPDATE agents SET current_prompt_id=NULL WHERE id=$1`, ag.ID)

	// mcp + skill + visibility (bidirectional via one join)
	// Clean up any leftover MCP from prior runs to keep this test idempotent.
	d.Exec(`DELETE FROM mcp_servers WHERE name = 't-gh'`)
	mid, err := d.SaveMCP(&MCPServer{Name: "t-gh", Transport: "stdio", Command: "npx", Args: json.RawMessage(`["server-github"]`), Env: json.RawMessage(`{"GITHUB_TOKEN":"x"}`), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// agent-side write. MCP is id-keyed (generic visibility join); skills are now
	// filesystem-based, so their visibility is keyed by skill (directory) name in a
	// dedicated table. Clear any pre-existing MCP visibility rows so the assertions
	// below isolate on exactly what this test sets.
	d.Exec(`DELETE FROM agent_visibility WHERE agent_id = $1 AND resource_kind = 'mcp'`, ag.ID)
	if err := d.ToggleVisibility(ag.ID, "mcp", mid, true); err != nil {
		t.Fatal(err)
	}
	if err := d.SetAgentSkillVisibility(ag.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := d.ToggleSkillVisibility(ag.ID, "t-skill", true); err != nil {
		t.Fatal(err)
	}
	// agent-side read
	vm, _ := d.AgentVisible(ag.ID, "mcp")
	if len(vm) != 1 || vm[0] != mid {
		t.Fatalf("agent visible mcp: %+v", vm)
	}
	// resource-side read (same join row) → bidirectional
	ra, _ := d.ResourceAgents("mcp", mid)
	if len(ra) != 1 || ra[0] != ag.ID {
		t.Fatalf("resource agents: %+v", ra)
	}
	// toggle off
	d.ToggleVisibility(ag.ID, "mcp", mid, false)
	vm2, _ := d.AgentVisible(ag.ID, "mcp")
	if len(vm2) != 0 {
		t.Fatalf("after toggle off: %+v", vm2)
	}
	// skill visibility is name-keyed: verify the read, then deleting the skill's
	// visibility rows (called when a skill is removed) clears it.
	names, _ := d.AgentSkillNames(ag.ID)
	if len(names) != 1 || names[0] != "t-skill" {
		t.Fatalf("agent visible skills: %+v", names)
	}
	if err := d.DeleteSkillVisibility("t-skill"); err != nil {
		t.Fatal(err)
	}
	names2, _ := d.AgentSkillNames(ag.ID)
	if len(names2) != 0 {
		t.Fatalf("skill visibility should be cleared on delete: %+v", names2)
	}
	d.DeleteMCP(mid)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
