package agent

import (
	"strings"
	"testing"
)

// 本文件在 P4 拆掉旧骨架后重新裁剪。
//
// 保留的是**与角色无关**的两类断言：
//  1. renderSystem 的三级回落（无覆盖→内置默认；有覆盖→渲染；模板报错→回落）；
//  2. langDirective 自身的语义（点名显示语言、保留原文、禁止镜像目标语言）。
//
// 换掉的是**角色清单**：原来的用例断言 worker/planner/mainagent 三条系统提示都
// 带语言尾，这三个角色已随 agent/{worker,planner,mainagent}.go 下线。现在由
// coord 的根/子 agent 接手"用户看得见"的位置，断言随之迁移到它们身上——
// 这不是把测试放松，而是让它在新的角色集合上继续守同一条不变式。
func TestRenderSystemOverrideAndFallback(t *testing.T) {
	t.Cleanup(func() { PromptOverride = nil })

	// no override → built-in default
	PromptOverride = nil
	if got := renderSystem("coord_root", "DEFAULT", CoordVars{}); got != "DEFAULT" {
		t.Fatalf("no override should give default, got %q", got)
	}

	// override → rendered with vars
	PromptOverride = func(k string) (string, bool) {
		if k == PromptKeyCoordRoot {
			return "目标:{{.Goal}} 范围:{{.Scope}}", true
		}
		return "", false
	}
	if got := renderSystem(PromptKeyCoordRoot, "DEFAULT", CoordVars{Goal: "拿下X", Scope: "*.x.com"}); got != "目标:拿下X 范围:*.x.com" {
		t.Fatalf("override render: %q", got)
	}

	// override referencing a non-catalog var → execution error → fallback to default
	PromptOverride = func(k string) (string, bool) { return "{{.NotInCatalog}}", true }
	if got := renderSystem(PromptKeyCoordRoot, "DEFAULT", CoordVars{Goal: "x"}); got != "DEFAULT" {
		t.Fatalf("bad var should fall back to default, got %q", got)
	}

	// 真实路径：DB 覆盖的正文被采用，且代码自有的语言尾仍追加在最后。
	PromptOverride = func(k string) (string, bool) { return "ROOT-BODY", true }
	got := RootSystemPrompt(CoordVars{Goal: "g", DataDir: "/data"})
	if !strings.HasPrefix(got, "ROOT-BODY") {
		t.Fatalf("RootSystemPrompt body not honored: %q", got)
	}
	// DataDir 必须是**真的渲染进正文**的：运行时把 cwd 设成任务目录（runner_coord
	// 的 WorkDir），提示词里不提这个目录，模型就会把脚本和战利品写到别处，
	// 用户在任务目录里找不到产物。
	//
	// 注意必须在**无覆盖**下验：上面那段用的是 DB 覆盖正文 "ROOT-BODY"，
	// 它根本不含 {{.DataDir}}，在那里断言目录会被渲染出来是自相矛盾的。
	PromptOverride = nil
	withDir := RootSystemPrompt(CoordVars{Goal: "g", DataDir: "/data"})
	if !strings.Contains(withDir, "/data") {
		t.Fatalf("RootSystemPrompt 没有渲染工作目录: %q", withDir)
	}
	if bare := RootSystemPrompt(CoordVars{Goal: "g"}); strings.Contains(bare, "工作目录") {
		t.Fatalf("DataDir 为空时不应渲染工作目录行: %q", bare)
	}
	if !strings.Contains(got, "简体中文") {
		t.Fatalf("RootSystemPrompt missing code-owned lang tail: %q", got)
	}
}

// langMarker is the unambiguous substring that proves langDirective selected a
// given display language. These are the native-script tokens embedded in the
// (Chinese) directive, so finding one means the output-language mandate names that
// language. Used by the directive and role-append tests below.
var langMarker = map[string]string{
	"zh": "简体中文",
}

// TestLangDirectiveLocalized pins the output-language tail: the directive names
// the display language, preserves raw technical strings, forbids mirroring the
// target/material language, and names the planner situation summary. Because
// Chinese is the only supported display language, the "never emit Chinese to the
// user" clause must be absent — Chinese output is the goal.
func TestLangDirectiveLocalized(t *testing.T) {
	for _, code := range SupportedOutputLangs {
		dir := langDirective(code)
		if !strings.Contains(dir, langMarker[code]) {
			t.Fatalf("%s: langDirective must name the display language (%q), got %q", code, langMarker[code], dir)
		}
		if !strings.Contains(dir, "payload") || !strings.Contains(dir, "原样逐字保留") {
			t.Fatalf("%s: langDirective must keep commands/payloads verbatim, got %q", code, dir)
		}
		if !strings.Contains(dir, "不要镜像或照抄目标") {
			t.Fatalf("%s: langDirective must forbid mirroring the target/material language, got %q", code, dir)
		}
		if !strings.Contains(dir, "态势") {
			t.Fatalf("%s: langDirective must name the planner situation summary as user-facing, got %q", code, dir)
		}
		// The anti-Chinese-leak clause applies to every non-Chinese display
		// language (planner situation-summary drift). When the display language IS
		// Chinese, Chinese output is correct, so the clause must be absent.
		hasNoZhClause := strings.Contains(dir, "也绝不能把中文输出给用户")
		if code == "zh" && hasNoZhClause {
			t.Fatalf("zh: directive must NOT forbid Chinese output when the display language is Chinese, got %q", dir)
		}
		if code != "zh" && !hasNoZhClause {
			t.Fatalf("%s: langDirective must forbid leaking Chinese to the user, got %q", code, dir)
		}
	}
}

// TestLangDirectiveAppendedToUserFacingRoles pins the localization tail wiring:
// every user-facing role's system prompt must carry the code-owned
// output-language directive, and a DB-edited body must NOT be able to drop it.
//
// 【P4】角色集合换成 coord 的根/子两个 agent —— 它们现在是仅有的两个会把文本
// 直接呈现到用户面前的角色（根的进度叙述、子的 finish_task 简报）。
func TestLangDirectiveAppendedToUserFacingRoles(t *testing.T) {
	t.Cleanup(func() { PromptOverride = nil; OutputLanguage = nil })

	// Even with a DB body that is pure non-directive text, the code-owned tail is
	// still appended — a custom body can never translate away the mandate.
	PromptOverride = func(string) (string, bool) { return "BODY-ONLY", true }

	for _, code := range SupportedOutputLangs {
		OutputLanguage = func() string { return code }
		marker := langMarker[code]
		cases := map[string]string{
			"coord_root":  RootSystemPrompt(CoordVars{Goal: "g", DataDir: "/data"}),
			"coord_child": ChildSystemPrompt(CoordVars{Goal: "g", DataDir: "/data"}),
			"chat":        chatSystem("chat", "/data", "/data"),
			"goals":       goalsSystem("/data", true),
		}
		for role, sys := range cases {
			if !strings.HasPrefix(sys, "BODY-ONLY") {
				t.Fatalf("%s/%s: DB body not honored: %q", code, role, sys)
			}
			if !strings.Contains(sys, marker) {
				t.Fatalf("%s/%s: missing output-language tail (%q): %q", code, role, marker, sys)
			}
			// The directive is the tail — it must come AFTER the body (recency).
			if strings.Index(sys, marker) <= strings.Index(sys, "BODY-ONLY") {
				t.Fatalf("%s/%s: langDirective must be appended after the body: %q", code, role, sys)
			}
		}
	}
}
