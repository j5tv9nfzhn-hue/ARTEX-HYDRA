package agent

import (
	"strings"

	"github.com/Autumn-27/artex/db"
)

// ConstraintBlock renders this task's operation constraints (task_constraints) as a
// high-priority block for the agent system prompt. allow/deny are grouped; empty
// string when there are no constraints (or ts is nil).
//
// ── 为什么必须进提示词 ──────────────────────────────────────────────────────
//
// 约束存在库里（task_constraints）而模型看不见，等于没设：运营方在界面上写下
// 「禁止对支付域发起任何请求」「只允许 443 端口」，模型照样会去派生意图。这个
// 函数就是那段唯一的渲染出口——调用方是 SessionRunner.sysBodyFor（经
// RunnerConfig.Constraints 注入），根与子都会带上。
//
// ── 与上游 strix 的**刻意分歧**（必读）──────────────────────────────────────
//
// 本仓库的交付形态是「只报告、不修复」，与上游 strix 相反。上游
// strix/agents/prompts/system_prompt.jinja 明确要求报告与修复合为一步：
//
//	"Derive the code fix as PART OF reporting ... `fix_before`/`fix_after`
//	 and `fix_pr_body`"
//	"If you also apply and verify the patch in the repo (edit the file,
//	 re-test that the vulnerability is gone), do it in the same agent/turn"
//	"Reporting and fixing are ONE step, not two ... the report is not
//	 complete without it"
//
// 即上游要求**产出补丁、填 PR 正文、并在仓库里直接改代码**。本仓库不采用：
//
//	autonomous agent 改他人代码库的失败模式（改错分支、留下半成品补丁、把未验证的
//	"修复"当成已修复）远大于它的收益，而且审计上很难区分"验证动作"与"改动动作"。
//
// 因此这里与 prompts_coord.go 的 blockFindingOnly 一起，把交付物收敛为**漏洞事实**。
//
// 注意：不要因为"对齐 strix"把这段删掉。DEFAULT（未设约束时）不渲染本段，
// 但 blockFindingOnly 是**无条件**渲染的，两条路径都要保持同一口径。
func ConstraintBlock(ts *db.ExplorationStore) string {
	if ts == nil {
		return ""
	}
	rows, err := ts.ListConstraints()
	if err != nil || len(rows) == 0 {
		return ""
	}
	var allow, deny []string
	for _, c := range rows {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		if c.Kind == "allow" {
			allow = append(allow, "- "+text)
		} else {
			deny = append(deny, "- "+text)
		}
	}
	if len(allow) == 0 && len(deny) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【操作约束（最高优先级，凌驾于下方一切探索/拓面启发式；每生成一条意图、每执行一个动作前都必须先自检是否违反，违反即不得进行）】：")
	if len(allow) > 0 {
		b.WriteString("\n允许的操作：\n")
		b.WriteString(strings.Join(allow, "\n"))
	}
	if len(deny) > 0 {
		b.WriteString("\n禁止的操作：\n")
		b.WriteString(strings.Join(deny, "\n"))
	}
	b.WriteString("\n（本段同时叠加一条**不可被 allow 覆盖的底线**：只找漏洞、不修漏洞——不写补丁、不给修复建议、不改目标代码/配置/仓库、不生成 PR。运营方的 allow 只能放大「允许验证什么」，永远不能放大「允许改动什么」。）")
	b.WriteString("\n（发现约束之外的新目标/新端口/新主机，不等于获得授权：除非它落在上述允许范围内，否则记为 out-of-scope 事实并跳过，不得为其派生意图或执行动作。）")
	return b.String()
}
