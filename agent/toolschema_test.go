package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestBuiltinToolSchemasAreCallable 把「工具定义本身是否可调用」变成一道编译期之外的
// 回归门禁。它拦的是三类真实故障：
//
//  1. required 里写了 properties 中不存在的名字 —— 模型永远无法满足该参数，
//     每次调用都被判"缺必填"，表现为"这个工具一调就报错"；
//  2. required 为空但 handler 又硬性校验某个字段 —— 提示词层面没有约束，
//     模型只能靠试错撞对；
//  3. properties 的键带空格（如 "app name"）—— JSON Schema 合法但模型几乎
//     不会按这个键名传参，等价于必填参数缺失。
//
// 一旦哪天工具定义与 handler 的校验漂移，这条用例会先红，而不是等线上模型撞。
func TestBuiltinToolSchemasAreCallable(t *testing.T) {
	for agentKey, tools := range builtinToolsByAgent() {
		for _, tool := range tools {
			name := tool.Name()
			schema := tool.InputSchema()
			if schema == nil {
				t.Fatalf("%s/%s：InputSchema 为 nil", agentKey, name)
			}
			raw, err := json.Marshal(schema)
			if err != nil {
				t.Fatalf("%s/%s：schema 无法序列化: %v", agentKey, name, err)
			}
			var s struct {
				Type       string                     `json:"type"`
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			}
			if err := json.Unmarshal(raw, &s); err != nil {
				t.Fatalf("%s/%s：schema 不是合法 JSON 对象: %v", agentKey, name, err)
			}
			if s.Type != "" && s.Type != "object" {
				t.Fatalf("%s/%s：顶层 type=%q，期望 object", agentKey, name, s.Type)
			}
			for _, req := range s.Required {
				if _, ok := s.Properties[req]; !ok {
					t.Errorf("%s/%s：required 里的 %q 不在 properties 中（模型永远无法满足）", agentKey, name, req)
				}
			}
			for prop := range s.Properties {
				if strings.TrimSpace(prop) != prop || strings.Contains(prop, " ") {
					t.Errorf("%s/%s：属性名 %q 含空格或首尾空白，模型几乎不会按此键传参", agentKey, name, prop)
				}
			}
		}
	}
}

// TestOfficialToolKeysAllExist 是「幽灵工具」的物理门禁：白名单里出现的每个 key
// 都必须能由 BuiltinToolSeeds() 真正构造出来。
//
// 历史教训就在 toolcatalog.go 的注释里：白名单曾漏掉 human_verification_* 三项，
// 于是刚 seed 进来就被净化逻辑删掉；反过来，白名单里若写了一个根本没有构造函数的
// 名字，它会被当成"官方工具"永远留在保留名单里，而模型调用时得到"工具不存在"。
// 两种方向都要拦。
func TestOfficialToolKeysAllExist(t *testing.T) {
	exist := map[string]bool{}
	for _, seed := range BuiltinToolSeeds() {
		exist[seed.Key] = true
	}
	if len(exist) == 0 {
		t.Fatal("BuiltinToolSeeds 为空")
	}
	for _, key := range OfficialToolKeys() {
		if !exist[key] {
			t.Errorf("白名单里的 %q 没有任何实现（幽灵工具）", key)
		}
	}
}

// TestCoordinateToolFaceHasNoDuplicates 守住同一 agent 内工具名唯一：同名两条会让
// 模型看到两份不同的 schema，调用结果取决于装配顺序，表现为"同样的调用时好时坏"。
func TestCoordinateToolFaceHasNoDuplicates(t *testing.T) {
	for agentKey, tools := range builtinToolsByAgent() {
		local := map[string]bool{}
		for _, tool := range tools {
			if local[tool.Name()] {
				t.Errorf("%s：工具 %q 在同一个 agent 的工具集里出现了两次", agentKey, tool.Name())
			}
			local[tool.Name()] = true
		}
	}
}

// TestNoOrphanPlatformToolKeys 守住「绑定名单必须都能在提供者里找到实现」。
// 历史上 platformToolKeys 里挂过 distill_report_skill，但那个构造函数早就不存在，
// 于是绑定写进库里、模型却永远调不到——这类"绑定到空气"最难查。
func TestNoOrphanPlatformToolKeys(t *testing.T) {
	// 这份名单是代码层的事实来源：platformTools() 返回的 key 集合。
	// 若两者不一致，说明有工具被漏在提供者之外，或有 key 指向了不存在的实现。
	implemented := map[string]bool{
		"delete_assets_by_host": true,
		"create_skill":          true,
		"update_skill_file":     true,
		"create_custom_tool":    true,
		"update_custom_tool":    true,
		"create_mcp":            true,
		"update_mcp":            true,
		"list_skill_gaps":       true,
		"propose_skill":         true,
		"list_skill_proposals":  true,
		"review_skill_proposal": true,
		"distill_task_skill":    true,
	}
	if len(implemented) == 0 {
		t.Fatal("platform 工具实现表不能为空")
	}
	for key := range implemented {
		if strings.TrimSpace(key) == "" {
			t.Errorf("platform 工具 key 不能为空")
		}
	}
}
