package agent

import (
	"strings"
	"testing"
)

// 本文件守一件事：**task_constraints 必须出现在模型看得见的地方**。
//
// 这个缺陷的形态很隐蔽——set_constraints 写入成功、UI 列表能查到、接口返回 200，
// 只有提示词里没有。于是运营方以为自己设了边界，模型却按探索启发式一路拓面。
// 断言放在渲染出口（sysBodyFor）而不是 DB 侧，是因为落库从来没坏过，坏的是
// 「库 → 提示词」这最后一步。

func TestSysBodyInjectsConstraintsIntoBothRoles(t *testing.T) {
	const marker = "禁止触碰支付域"
	r := NewSessionRunner(RunnerConfig{
		Constraints: func() string {
			return "【操作约束（测试桩）】\n禁止的操作：\n- " + marker
		},
	})
	spec := AgentSpec{Task: "审计 example.com"}

	root := r.sysBodyFor(spec, nil, "root", true)
	if !strings.Contains(root, marker) {
		t.Fatalf("根提示词未注入操作约束（Scope 段落缺失）：\n%s", root)
	}
	child := r.sysBodyFor(spec, nil, "c1", false)
	if !strings.Contains(child, marker) {
		t.Fatalf("子提示词未注入操作约束——执行者是真正动手的那个，漏掉等于没设：\n%s", child)
	}
	// 必须落在"授权范围 / 操作约束"这个位置，而不是被塞进目标描述里。
	if !strings.Contains(child, "约束") {
		t.Fatalf("子提示词缺少约束小标题：\n%s", child)
	}
}

// 无约束不得留下空洞，也不得打崩。
func TestSysBodyWithoutConstraintsStaysClean(t *testing.T) {
	r := NewSessionRunner(RunnerConfig{}) // Constraints == nil
	body := r.sysBodyFor(AgentSpec{Task: "审计 example.com", AssetIDs: []int64{7}}, nil, "root", true)
	if !strings.Contains(body, "审计 example.com") {
		t.Fatalf("目标丢失：\n%s", body)
	}
	if !strings.Contains(body, "仅限资产 #7") {
		t.Fatalf("资产范围丢失：\n%s", body)
	}
	if strings.Contains(body, "【操作约束") {
		t.Fatalf("没有约束时不该出现约束段：\n%s", body)
	}
}

// 空回调（约束被清空）与 nil 回调必须等价。
func TestSysBodyEmptyConstraintsStaysClean(t *testing.T) {
	r := NewSessionRunner(RunnerConfig{Constraints: func() string { return "   " }})
	body := r.sysBodyFor(AgentSpec{Task: "x"}, nil, "root", true)
	if strings.Contains(body, "【操作约束") {
		t.Fatalf("空白约束不该渲染出空段：\n%s", body)
	}
}

func TestConstraintBlockNilStore(t *testing.T) {
	if got := ConstraintBlock(nil); got != "" {
		t.Fatalf("nil store 应返回空串，得到 %q", got)
	}
}

// 交付边界（只报告、不修复）必须**无条件**出现：它不能依赖运营方是否设了约束，
// 也不能依赖任务是否配了资产范围。
//
// 这条断言的存在理由：本仓库与上游 strix 在这一点上是相反的（上游要求报告里带
// 补丁与 PR 正文），而提示词里又逐字照搬了 strix 的其它段落。少了这条断言，
// 将来有人"对齐 strix"把 blockFindingOnly 删掉，编译与既有测试都会照常通过。
func TestFindingOnlyBlockIsUnconditional(t *testing.T) {
	r := NewSessionRunner(RunnerConfig{}) // 无约束、无资产范围
	for _, tc := range []struct {
		name   string
		isRoot bool
	}{{"root", true}, {"child", false}} {
		t.Run(tc.name, func(t *testing.T) {
			body := r.sysBodyFor(AgentSpec{Task: "审计 example.com"}, nil, "a1", tc.isRoot)
			for _, want := range []string{
				"只报告，不修复",
				"不写补丁",
				"不生成 PR",
				"不改目标的代码",
			} {
				if !strings.Contains(body, want) {
					t.Fatalf("%s 提示词缺少交付边界措辞 %q：\n%s", tc.name, want, body)
				}
			}
		})
	}
}

// 上游 strix 的 "Reporting-with-fix" 已按本地口径改为 Reporting-only。
// 保留这条断言，防止"对齐上游"时被改回去。
func TestRootDirectiveDropsFixStep(t *testing.T) {
	r := NewSessionRunner(RunnerConfig{})
	body := r.sysBodyFor(AgentSpec{Task: "g"}, nil, "root", true)
	if strings.Contains(body, "Reporting-with-fix") {
		t.Fatalf("仍保留上游的 Reporting-with-fix，与只报告口径冲突：\n%s", body)
	}
	if !strings.Contains(body, "Reporting-only") {
		t.Fatalf("根提示词应明确 Reporting-only：\n%s", body)
	}
}
