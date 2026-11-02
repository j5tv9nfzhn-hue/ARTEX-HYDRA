package agent

// report_finding 的 CVSS 校准与判重接入。
//
// 两件事都移植自 Strix 的 reporting 工具：
//   - cvss_breakdown：八项 CVSS v3.1 基础指标齐全合法后由系统算分并定级，
//     严重度以向量为准（见 agent/tools.go 的 addFinding）。
//   - 判重：落库前问一次「这条是不是已经报过了」，判为重复就不落库。
//
// 判重要一次模型调用，而模型、provider 池、故障转移都在 server 侧；agent 只负责
// 把候选交出去、拿结论、据此拦住这次上报。实现由宿主注入（见 FindingDedupe）。

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/dedupe"
)

// FindingDedupe 判断一条候选发现是不是本任务已有发现的重复。
//
// 实现方负责取「已有发现」集合（宿主才有库连接）并按设置决定用哪个模型。返回的
// Result 若 IsDuplicate 为真，agent 侧不落库，并把结论原样回给模型。
//
// 判重失败（模型不可用、超时、回包不是 JSON）时返回 error，agent 侧按「未重复」
// 放行——把一条真发现挡在门外的代价，远大于多留一条重复条目。
type FindingDedupe interface {
	Check(ctx context.Context, taskID int64, candidate dedupe.Candidate) (dedupe.Result, error)
}

// SetFindingDedupe 注入判重实现。nil = 不判重（上报直接落库）。
func (t *ToolSet) SetFindingDedupe(d FindingDedupe) { t.findingDedupe = d }

// cvssMetricSchema 造一项 CVSS 指标的参数 schema（取值枚举 + 说明）。
//
// 刻意用函数而不是内联字面量：八个指标的内联 map 字面量会被 gofmt 按最长键对齐，
// 手写很容易差一格，而 gofmt 门禁就在 CI 上。
func cvssMetricSchema(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}

// findingImpactText 拼出判重载荷里的 impact 字段：严重度 + CVSS 分。
func findingImpactText(severity, score string) string {
	parts := make([]string, 0, 2)
	if s := strings.TrimSpace(severity); s != "" {
		parts = append(parts, "severity="+s)
	}
	if s := strings.TrimSpace(score); s != "" {
		parts = append(parts, "cvss="+s)
	}
	return strings.Join(parts, " ")
}

// assetIDText 拼出判重载荷里的 target 字段：受影响资产 id 列表。
func assetIDText(raw []json.RawMessage) string {
	ids := pidList(raw)
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ",")
}

// dedupeDuplicateMessage 造判重命中时回给模型的文案。
//
// 照搬 Strix 的口径（"do not re-report the same vulnerability"）：模型看到的是
// 「别再报，把证据并进已有那条」，而不是一句没有下文的「重复」。
func dedupeDuplicateMessage(result dedupe.Result) string {
	return fmt.Sprintf(
		"疑似与已有漏洞 #%s 重复（判重置信度 %.2f）：%s。不要重复上报同一个漏洞；"+
			"如果只是补充证据或补充复现，请把它并入已有那条。",
		result.DuplicateID, result.Confidence, strings.TrimSpace(result.Reason))
}
