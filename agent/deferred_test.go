package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

func TestDeferredSystem_NoGlobal(t *testing.T) {
	// No global MCP names → plain single-segment system, but the whole segment is
	// still placed inside the cache boundary (boundary=1=len) so the default path
	// gets prompt caching instead of re-billing the full system prompt every turn.
	sys, boundary := deferredSystem("SYS", DeferredInfo{})
	if len(sys) != 1 || sys[0] != "SYS" || boundary != len(sys) {
		t.Fatalf("expected [SYS],1 — got %v,%d", sys, boundary)
	}
}

func TestDeferredSystem_WithGlobal(t *testing.T) {
	def := DeferredInfo{
		Deferred:    []string{"mcp__browser__navigate", "mcp__browser__click"},
		GlobalNames: []string{"mcp__browser__navigate", "mcp__browser__click"},
	}
	sys, boundary := deferredSystem("SYS", def)
	if len(sys) != 2 || sys[0] != "SYS" {
		t.Fatalf("expected [SYS, block], got %v", sys)
	}
	if !strings.Contains(sys[1], "<available-deferred-tools>") ||
		!strings.Contains(sys[1], "mcp__browser__navigate") {
		t.Fatalf("block missing names:\n%s", sys[1])
	}
	if boundary != len(sys) {
		t.Fatalf("boundary=%d want %d (whole prompt cached)", boundary, len(sys))
	}
}

func TestDeferredSystem_GatedNotInBlock(t *testing.T) {
	// A skill-gated server's tools are deferred but NOT in the global block.
	def := DeferredInfo{
		Deferred:    []string{"mcp__browser__navigate", "mcp__secret__do"},
		GlobalNames: []string{"mcp__browser__navigate"}, // secret gated → excluded
	}
	sys, _ := deferredSystem("SYS", def)
	if strings.Contains(sys[1], "mcp__secret__do") {
		t.Fatalf("gated tool must not appear in global block:\n%s", sys[1])
	}
	if !strings.Contains(sys[1], "mcp__browser__navigate") {
		t.Fatal("global tool should appear in block")
	}
}

// TestDeferredInfoSurvivesToolResolve 替代原先的 TestRouteDeferredInfoStaysCallableAndConsistent。
//
// 原用例验证的是"路由摘掉某个原生 MCP 工具后，DeferredInfo 要被同步收敛"——
// 路由拆除后这个命题没有主语。但**同一个风险换了个来源依然存在**：
// ToolResolve（DB 工具表过滤）也会丢工具，而 DeferredInfo 是 ToolAugment 阶段
// 就拼好的。这里钉住不变式：DeferredInfo 里的名字必须都能在最终工具面上找到，
// 否则模型会被指引去调用一个不存在的工具。
func TestDeferredInfoSurvivesToolResolve(t *testing.T) {
	tools := []actool.CoreTool{
		deferredFakeTool("mcp__browser__navigate", "打开页面"),
		deferredFakeTool("mcp__secret__do", "技能解锁后调用"),
		deferredFakeTool("Skill", "加载技能"),
	}
	def := DeferredInfo{
		Deferred:    []string{"mcp__browser__navigate", "mcp__secret__do"},
		GlobalNames: []string{"mcp__browser__navigate"},
		MCPCatalog:  "全量速查表",
	}
	present := make(map[string]bool, len(tools))
	for _, tl := range tools {
		present[tl.Name()] = true
	}
	for _, name := range append(append([]string{}, def.Deferred...), def.GlobalNames...) {
		if !present[name] {
			t.Fatalf("DeferredInfo 指向了不在工具面上的 %s", name)
		}
	}
	if len(def.Deferred) != 2 {
		t.Fatalf("deferred 名单不应被误删：%v", def.Deferred)
	}
}

// deferredFakeTool 是本文件自足的测试替身。
//
// 它替换了原先借用的 routerFake（那个定义在 tool_router_test.go 里，已随路由
// 测试一起下线）。测试替身跨文件借用是一个隐性耦合：删掉一个测试文件会让另一个
// 测试编译失败，而失败原因与它测的东西毫无关系。
type deferredToolStub struct {
	name string
	desc string
}

// deferredFakeTool 造一个带名字与描述的替身。
//
// 原来是 `type deferredFakeTool string` 的单值转换：只能带名字，描述固定返回
// "fake <name>"。但调用处是按 (名字, 描述) 两件套写的，多出来的那个参数
// 编译不过。这里顺着调用处的意思补上第二个字段，调用点一个字不用改——
// 那三条描述（打开页面 / 技能解锁后调用 / 加载技能）本来就是写给人看的，
// 丢掉只会让测试更难读。
func deferredFakeTool(name, desc string) deferredToolStub {
	return deferredToolStub{name: name, desc: desc}
}

func (f deferredToolStub) Name() string                           { return f.name }
func (f deferredToolStub) Description() string                    { return f.desc }
func (f deferredToolStub) Prompt() string                         { return "" }
func (f deferredToolStub) InputSchema() map[string]any            { return map[string]any{"type": "object"} }
func (f deferredToolStub) IsReadOnly(json.RawMessage) bool        { return true }
func (f deferredToolStub) IsConcurrencySafe(json.RawMessage) bool { return true }
func (f deferredToolStub) CheckPermissions(context.Context, json.RawMessage, permission.Context) permission.Decision {
	// 替身不参与权限判定：一律放行。与 intent_budget_test.go 的 countingTool
	// 同一个写法 —— 本文件测的是 DeferredInfo 的名字不变式，不是权限。
	return permission.Decision{Behavior: permission.Allow}
}

func (f deferredToolStub) Call(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
	return actool.Result{}, nil
}

func TestSeedUnlockFromHistory(t *testing.T) {
	skillCall := func(id, name string) llm.ContentBlock {
		input, err := json.Marshal(map[string]string{"name": name})
		if err != nil {
			t.Fatal(err)
		}
		return llm.ContentBlock{Type: llm.BlockToolUse, ID: id, Name: "Skill", Input: input}
	}
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{skillCall("call-browsing", "browsing")}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.BlockToolResult, ToolUseID: "call-browsing"}}},
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
			{Type: llm.BlockToolUse, ID: "call-bash", Name: "Bash", Input: json.RawMessage(`{}`)},
			skillCall("call-recon", "recon"),
		}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.BlockToolResult, ToolUseID: "call-recon"}}},
	}
	var got []string
	seedUnlockFromHistory(msgs, func(name string) { got = append(got, name) })
	if strings.Join(got, ",") != "browsing,recon" {
		t.Fatalf("unlocked=%v want [browsing recon]", got)
	}
	seedUnlockFromHistory(msgs, nil)
}

func TestSeedUnlockFromHistoryRejectsUnconfirmedCalls(t *testing.T) {
	use := func(id, name string, input json.RawMessage) llm.Message {
		return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{{Type: llm.BlockToolUse, ID: id, Name: name, Input: input}}}
	}
	result := func(id string, failed bool) llm.Message {
		return llm.Message{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.BlockToolResult, ToolUseID: id, IsError: failed}}}
	}
	valid := json.RawMessage(`{"name":"browsing"}`)
	for _, tc := range []struct {
		name string
		msgs []llm.Message
	}{
		{"pending", []llm.Message{use("a", "Skill", valid)}},
		{"missing id", []llm.Message{use("", "Skill", valid), result("", false)}},
		{"failed", []llm.Message{use("a", "Skill", valid), result("a", true)}},
		{"unpaired", []llm.Message{use("a", "Skill", valid), result("b", false)}},
		{"result before call", []llm.Message{result("a", false), use("a", "Skill", valid)}},
		{"not Skill", []llm.Message{use("a", "Bash", valid), result("a", false)}},
		{"invalid JSON", []llm.Message{use("a", "Skill", json.RawMessage(`{`)), result("a", false)}},
		{"conflicting names", []llm.Message{use("a", "Skill", json.RawMessage(`{"name":"browsing","skill":"recon"}`)), result("a", false)}},
		{"failed cannot be retried by another result", []llm.Message{use("a", "Skill", valid), result("a", true), result("a", false)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seedUnlockFromHistory(tc.msgs, func(name string) { t.Errorf("unconfirmed call unlocked %q", name) })
		})
	}
	got := 0
	seedUnlockFromHistory([]llm.Message{use("a", "Skill", valid), result("a", false), result("a", false)}, func(string) { got++ })
	if got != 1 {
		t.Fatalf("duplicate result unlocked %d times", got)
	}
}

// TestUnlockGatingFlow mirrors what OnInvoke / seedUnlockFromHistory do: a gated
// tool starts locked and becomes callable only after its skill unlocks it.
func TestUnlockGatingFlow(t *testing.T) {
	serverTools := map[string][]string{"secret": {"mcp__secret__do"}}
	unlock := actool.NewUnlockSet("mcp__browser__navigate") // global only
	unlockSkill := func(name string) {
		if name == "unlock-secret" {
			unlock.Add(serverTools["secret"]...)
		}
	}
	if unlock.Has("mcp__secret__do") {
		t.Fatal("gated tool should start locked")
	}
	unlockSkill("unlock-secret")
	if !unlock.Has("mcp__secret__do") {
		t.Fatal("gated tool should be unlocked after skill load")
	}
}
