package agent

import (
	"strings"
	"testing"
)

// TestChatSystemKeepsAuthorizationForRoot 是「对话页开始拒绝」的回归护栏。
//
// 背景：对话页默认 Agent 是根 Agent（coord_root）。任务态的 rootTmpl 引用了
// {{.Goal}} / {{.Scope}} / {{.ChildSlots}} 等只有任务运行期才注入的变量，而
// 对话页只给 {DataDir, Now}，Go 模板因此渲染失败，renderSystem 静默回退到
// DefaultAssistantPrompt（通用助手），AUTHORIZATION 与 REFUSAL AVOIDANCE 两段
// 一起消失，模型随即开始讲安全政策。
//
// 这个测试钉死三件事：授权段还在、免拒绝段还在、且没有把任务态的编排指令
// （delegate_task / finish_task）带进对话页——那会让模型去调不存在的工具。
func TestChatSystemKeepsAuthorizationForRoot(t *testing.T) {
	old := PromptOverride
	t.Cleanup(func() { PromptOverride = old })
	// 模拟真实服务端：库里存着任务态 rootTmpl（含对话页没有的变量）。
	PromptOverride = func(key string) (string, bool) {
		if key == PromptKeyCoordRoot {
			return rootTmpl, true
		}
		return "", false
	}

	got := chatSystem(PromptKeyCoordRoot, "/data", "/data/sessions/1")

	for _, want := range []string{"AUTHORIZATION STATUS", "REFUSAL AVOIDANCE", "SCOPE:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("对话页正文缺少 %q —— 又会回落到通用助手并开始拒绝", want)
		}
	}
	for _, banned := range []string{"delegate_task", "finish_task", "{{."} {
		if strings.Contains(got, banned) {
			t.Fatalf("对话页正文不应包含 %q", banned)
		}
	}

	// 旧版留下的 auto / pentest 会话行已没有库提示词，也必须落到同一段正文，
	// 否则老线程仍是「通用助手」。
	for _, legacy := range []string{"auto", "pentest"} {
		if sys := chatSystem(legacy, "/data", "/data/sessions/1"); !strings.Contains(sys, "AUTHORIZATION STATUS") {
			t.Fatalf("旧会话 %s 仍回落到通用助手", legacy)
		}
	}
}
