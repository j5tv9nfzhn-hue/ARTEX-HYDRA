package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// hasSkillTool reports whether the packed tool set contains the Skill meta-tool.
func hasSkillTool(tools []actool.CoreTool) bool {
	for _, t := range tools {
		if t.Name() == "Skill" {
			return true
		}
	}
	return false
}

// TestAssembleVisibleSkill verifies a filesystem skill made visible to an agent is
// packed into that agent's tool set (as the single Skill meta-tool) via the wired
// ToolAugment hook. Skills live on disk under skillDir; visibility is a per-agent
// (agent × skill_name) row keyed by the skill's directory name.
func TestAssembleVisibleSkill(t *testing.T) {
	pg, err := db.Open("file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "assembly.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Close() })

	// Filesystem skill fixture: <skillDir>/t-assemble/SKILL.md. The directory name
	// (t-assemble) is the visibility key matched against AgentSkillNames.
	skillDir := t.TempDir()
	const skillName = "t-assemble"
	if err := os.MkdirAll(filepath.Join(skillDir, skillName), 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: t-assemble\ndescription: do the thing\n---\ndo the thing"
	if err := os.WriteFile(filepath.Join(skillDir, skillName, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	originalAugment := agent.ToolAugment
	wireAgentAugment(pg, skillDir, nil)
	t.Cleanup(func() { agent.ToolAugment = originalAugment })

	// 角色名用 worker，不再用 planner —— planner 已在 db/legacyAgentKeys 里下线，
	// 旧库里那行会被 pruneLegacyAgents 删掉。断言里写 planner 的测试会一直红，
	// 而红的原因不是功能坏了，是**测试对象不存在**了：这正是三处"过期断言"的
	// 共同成因（另两处是 mgmt_test 的 len(agents)>=5、task_templates_test 的
	// PUT /api/agents/planner/prompt）。
	//
	// worker 是当前世代的实际载体：它既是 DefaultCoordRootAgentKey（根），
	// 也是子代理的可见性身份，所以"技能可见性"这条链路在它身上才真的跑得通。
	const coreKey = "worker"
	core, err := pg.GetAgentByKey(coreKey)
	if err != nil || core == nil {
		t.Fatalf("worker agent missing: %v", err)
	}
	// goals 当"另一个 agent"的反例，证明"显式绑给 A 的技能不会漏给 B"仍成立。
	other, err := pg.GetAgentByKey("goals")
	if err != nil || other == nil {
		t.Fatalf("goals agent missing: %v", err)
	}

	// Clear worker grants in this isolated fixture and restore seeded defaults
	// during cleanup. Explicit single-agent binding is tested below.
	orig, _ := pg.AgentSkillNames(core.ID)
	if err := pg.SetAgentSkillVisibility(core.ID, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.SetAgentSkillVisibility(core.ID, orig) })

	// No explicit binding: core roles discover the skill by default.
	extra, _, cleanup := agent.ToolAugment(context.Background(), coreKey)
	cleanup()
	if !hasSkillTool(extra) {
		t.Fatal("unbound skill should be discoverable by a core role")
	}

	// Once explicitly restricted to goals, it must not leak to worker.
	if err := pg.ToggleSkillVisibility(other.ID, skillName, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.ToggleSkillVisibility(other.ID, skillName, false) })

	// before: explicitly goals-only → no Skill meta-tool for worker
	extra, _, cleanup = agent.ToolAugment(context.Background(), coreKey)
	cleanup()
	if hasSkillTool(extra) {
		t.Fatalf("expected no Skill meta-tool before the skill is made visible")
	}

	// make the skill visible to worker
	if err := pg.ToggleSkillVisibility(core.ID, skillName, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.ToggleSkillVisibility(core.ID, skillName, false) })

	// after: the Skill meta-tool is packed in
	extra, _, cleanup = agent.ToolAugment(context.Background(), coreKey)
	defer cleanup()
	if !hasSkillTool(extra) {
		t.Fatalf("expected the Skill meta-tool after making the skill visible")
	}

	// AugmentTools appends extra to base without filtering base
	combined, _, c2 := agent.AugmentTools(context.Background(), coreKey, nil)
	defer c2()
	if !hasSkillTool(combined) {
		t.Fatalf("AugmentTools should surface the Skill meta-tool")
	}
	if err := pg.ToggleSkillVisibility(core.ID, skillName, false); err != nil {
		t.Fatal(err)
	}
	extra, _, cleanup = agent.ToolAugment(context.Background(), coreKey)
	cleanup()
	if hasSkillTool(extra) {
		t.Fatal("revoked explicit worker grant still visible while goals is bound")
	}
}
