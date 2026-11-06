package server

import (
	"encoding/json"
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"
)

// TestHostToolSchemasAreCallable 对 host 工具面做与 agent 侧同口径的检查。
//
// host 工具（编排 + 平台）的 schema 由 objSchema/wrTool 手写，最容易出现的缺陷是
// required 里列了 properties 中不存在的参数——模型无论怎么传都缺参，每次调用都
// 被 handler 判错，表现就是"这个工具一调就报错"。这类问题在单测里是确定性的，
// 不该留给线上模型去撞。
func TestHostToolSchemasAreCallable(t *testing.T) {
	s := &Server{}
	var tools []actool.CoreTool
	tools = append(tools, s.orchestrationTools()...)
	tools = append(tools, s.platformTools()...)
	if len(tools) == 0 {
		t.Fatal("host 工具面为空：提供者又退化成 return nil 了")
	}
	seen := map[string]bool{}
	for _, tool := range tools {
		name := tool.Name()
		if name == "" {
			t.Errorf("存在无名工具")
			continue
		}
		if seen[name] {
			t.Errorf("工具 %q 重复注册", name)
		}
		seen[name] = true

		raw, err := json.Marshal(tool.InputSchema())
		if err != nil {
			t.Fatalf("%s：schema 无法序列化: %v", name, err)
		}
		var sch struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(raw, &sch); err != nil {
			t.Fatalf("%s：schema 非法: %v", name, err)
		}
		if sch.Type != "" && sch.Type != "object" {
			t.Errorf("%s：顶层 type=%q，期望 object", name, sch.Type)
		}
		for _, req := range sch.Required {
			if _, ok := sch.Properties[req]; !ok {
				t.Errorf("%s：required 里的 %q 不在 properties 中", name, req)
			}
		}
		for prop := range sch.Properties {
			if strings.Contains(prop, " ") || strings.TrimSpace(prop) != prop {
				t.Errorf("%s：属性名 %q 含空格或首尾空白", name, prop)
			}
		}
	}
}

// TestPlatformToolKeysResolve 守住「绑定名单 ⊆ 提供者实现」这条不变式。
func TestPlatformToolKeysResolve(t *testing.T) {
	s := &Server{}
	impl := map[string]bool{}
	for _, tool := range s.platformTools() {
		impl[tool.Name()] = true
	}
	if len(platformToolKeys) == 0 {
		t.Fatal("platformToolKeys 不能为空")
	}
	for _, key := range platformToolKeys {
		if !impl[key] {
			t.Errorf("platformToolKeys 里的 %q 没有实现（绑定到空气）", key)
		}
	}
}
