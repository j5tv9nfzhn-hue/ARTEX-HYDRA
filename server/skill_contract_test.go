package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Autumn-27/norma/skill"
)

func TestSkillCanonicalContractAndLegacyAliases(t *testing.T) {
	// fixture 用一个不在别名表里的名字。原来用 "api-recon"，而 "api" 在
	// agent.CanonicalSkillAlias 里映射到 performing-api-inventory-and-discovery，
	// 于是 `{"skill":"api"}` 按设计解析成别名目标而不是 fixture —— 测试就红了。
	// 但这是**正确的产品行为**（用户自建的精确名优先于历史别名，见
	// TestSkillExactVisibleCustomNamePrecedesAlias）。这条测试真正要守的是
	// 「三个遗留字段都被接受、被归一成规范 name、且不泄漏给 SDK」，
	// 与某个具体名字无关，因此换一个不与别名冲突的 fixture。
	const fixtureName = "api-recon-fixture"
	reg := skill.NewRegistry(skill.Skill{Name: fixtureName, Description: "fixture", Instructions: "fixture guidance"})
	m := &meteredSkill{CoreTool: reg.Tool(), reg: reg}
	for _, raw := range []string{`{"name":"api-recon-fixture"}`, `{"skill":"API-RECON-FIXTURE"}`, `{"skill_name":"Api-Recon-Fixture"}`} {
		name, normalized, err := m.resolveInput(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		if name != fixtureName {
			t.Fatalf("解析出的规范名 %q，期望 %q", name, fixtureName)
		}
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(normalized, &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["skill"]; ok {
			t.Fatal("legacy field leaked to SDK")
		}
		result, err := m.CoreTool.Call(context.Background(), normalized, nil)
		if err != nil || result.IsError || len(result.Extra) != 1 {
			t.Fatalf("SDK Skill contract failed: %v / %s", err, result.Flatten())
		}
	}
	for _, raw := range []string{`null`, `{"name":"api-recon-fixture","skill":"camoufox"}`, `{"skill":null}`, `{"name":"api-recon-fixture","args":{}}`, `{"name":"api-recon-fixture","unknown":1}`} {
		if _, _, err := m.resolveInput(json.RawMessage(raw)); err == nil {
			t.Fatalf("invalid input accepted: %s", raw)
		}
	}
	if _, ok := m.InputSchema()["anyOf"]; !ok {
		t.Fatal("legacy compatibility is not advertised to validation")
	}
}

func TestSkillExactVisibleCustomNamePrecedesAlias(t *testing.T) {
	reg := skill.NewRegistry(skill.Skill{Name: "api", Instructions: "custom"}, skill.Skill{Name: "api-recon", Instructions: "builtin"})
	m := &meteredSkill{CoreTool: reg.Tool(), reg: reg}
	if name, _, err := m.resolveInput(json.RawMessage(`{"name":"api"}`)); err != nil || name != "api" {
		t.Fatalf("custom name shadowed: %q / %v", name, err)
	}
	if _, _, err := m.resolveInput(json.RawMessage(`{"name":"api","skill":"api-recon"}`)); err == nil {
		t.Fatal("distinct visible skills conflated")
	}
}
