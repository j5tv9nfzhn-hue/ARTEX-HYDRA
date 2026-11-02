// Package cvss 实现 CVSS v3.1 基础指标的校验与基础分计算。
//
// 移植自 Strix：strix/tools/reporting/tool.py 的 _CVSS_VALID、_validate_cvss_breakdown
// 与 _calculate_cvss。Strix 那边用 Python 的 cvss 库算分，这里按 FIRST 的 CVSS v3.1
// 规范手写基础分公式——同一个向量在两边必须得到同一个分，否则「模型校准过的严重度」
// 会在两个系统里对不上，跨系统比对的结论也就失去意义。
//
// 依赖类发现（advisory_cvss / contextual_cvss）没有移植：ARTEX 的发现模型里没有
// 「软件包 + CVE」这种对象，那套评级规则在这里没有落点。硬造一个只会得到一个没人
// 填的字段。
package cvss

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Breakdown 是 CVSS v3.1 的八项基础指标。
//
// 字段顺序即规范顺序（AV/AC/PR/UI/S/C/I/A），也是落库 JSON 的键序：解析与渲染都
// 依赖它稳定，别随手调换。
type Breakdown struct {
	AttackVector       string `json:"attack_vector"`
	AttackComplexity   string `json:"attack_complexity"`
	PrivilegesRequired string `json:"privileges_required"`
	UserInteraction    string `json:"user_interaction"`
	Scope              string `json:"scope"`
	Confidentiality    string `json:"confidentiality"`
	Integrity          string `json:"integrity"`
	Availability       string `json:"availability"`
}

// metricSpec 是一项指标的合法取值表。
type metricSpec struct {
	Name  string
	Valid []string
}

// metrics 是八项指标的取值表，移植自 Strix 的 _CVSS_VALID。
//
// 顺序不可改：Strix 用 Python 字典保序遍历，报错按这个顺序逐条列出；这里照抄同一个
// 顺序，模型看到的报错序列才与 Strix 一致。
var metrics = []metricSpec{
	{Name: "attack_vector", Valid: []string{"N", "A", "L", "P"}},
	{Name: "attack_complexity", Valid: []string{"L", "H"}},
	{Name: "privileges_required", Valid: []string{"N", "L", "H"}},
	{Name: "user_interaction", Valid: []string{"N", "R"}},
	{Name: "scope", Valid: []string{"U", "C"}},
	{Name: "confidentiality", Valid: []string{"N", "L", "H"}},
	{Name: "integrity", Valid: []string{"N", "L", "H"}},
	{Name: "availability", Valid: []string{"N", "L", "H"}},
}

// 各项指标的权重（CVSS v3.1 规范 4.2 表）。
var (
	weightAV          = map[string]float64{"N": 0.85, "A": 0.62, "L": 0.55, "P": 0.2}
	weightAC          = map[string]float64{"L": 0.77, "H": 0.44}
	weightPRUnchanged = map[string]float64{"N": 0.85, "L": 0.62, "H": 0.27}
	weightPRChanged   = map[string]float64{"N": 0.85, "L": 0.68, "H": 0.50}
	weightUI          = map[string]float64{"N": 0.85, "R": 0.62}
	weightImpact      = map[string]float64{"H": 0.56, "L": 0.22, "N": 0}
)

// errVectorUnparsable 是指标取值非法时的报错前缀，移植自 Strix 的
// "Failed to calculate CVSS for validated vector: {vector}"。
const errVectorUnparsable = "Failed to calculate CVSS for validated vector: "

// Validate 校验八项指标齐全且取值合法，返回错误文案。
//
// 文案逐字对齐 Strix 的 _validate_cvss_breakdown：模型在 Strix 上见过这句话，这里
// 再见到同一句，就不用重新学一遍「Invalid attack_vector: X」到底要改什么。
func Validate(raw map[string]any) []string {
	if len(raw) == 0 {
		return []string{"cvss_breakdown: must be an object with the 8 CVSS metrics"}
	}
	var errs []string
	for _, m := range metrics {
		value, present := raw[m.Name]
		if present {
			if s, ok := value.(string); ok && contains(m.Valid, s) {
				continue
			}
		}
		errs = append(errs, fmt.Sprintf("Invalid %s: %s. Must be one of: %s",
			m.Name, pyValue(value, present), pyList(m.Valid)))
	}
	return errs
}

// Parse 从原始 map 取出八项指标。取值不合法也不报错——那是 Validate 的事；这里
// 只负责搬运，避免两处各写一套取值逻辑。
func Parse(raw map[string]any) Breakdown {
	get := func(name string) string {
		s, _ := raw[name].(string)
		return s
	}
	return Breakdown{
		AttackVector:       get("attack_vector"),
		AttackComplexity:   get("attack_complexity"),
		PrivilegesRequired: get("privileges_required"),
		UserInteraction:    get("user_interaction"),
		Scope:              get("scope"),
		Confidentiality:    get("confidentiality"),
		Integrity:          get("integrity"),
		Availability:       get("availability"),
	}
}

// Vector 拼出 CVSS:3.1 向量字符串，移植自 Strix 的 _calculate_cvss。
func (b Breakdown) Vector() string {
	return "CVSS:3.1/AV:" + b.AttackVector +
		"/AC:" + b.AttackComplexity +
		"/PR:" + b.PrivilegesRequired +
		"/UI:" + b.UserInteraction +
		"/S:" + b.Scope +
		"/C:" + b.Confidentiality +
		"/I:" + b.Integrity +
		"/A:" + b.Availability
}

// CanonicalJSON 输出八指标的标准 JSON（键序固定为规范顺序）。
//
// 落库用这个而不是模型原样给的字符串：同一个向量在库里必须是同一串字节，否则
// 「按向量分组统计」这类查询得先做一次 JSON 规范化才敢用。
func (b Breakdown) CanonicalJSON() string {
	raw, err := json.Marshal(b)
	if err != nil {
		return ""
	}
	return string(raw)
}

// Calculate 算基础分、严重度标签与向量，移植自 Strix 的 _calculate_cvss。
//
// severity 用 Strix 的口径：CVSS 的 none 档写成 "info"。ARTEX 自己的档位见 Band。
// 唯一的失败情形是八项指标取值非法——那说明调用方漏了 Validate。
func Calculate(b Breakdown) (score float64, severity string, vector string, err error) {
	vector = b.Vector()
	base, err := baseScore(b)
	if err != nil {
		return 0, "", vector, err
	}
	return base, SeverityLabel(base), vector, nil
}

// Band 把基础分映射成 ARTEX 的严重度档位（critical / high / medium / low）。
//
// ARTEX 没有 info 档：0.0 分（没有实证任何影响）落到 low，但分数本身仍是 0.0——
// 「没证明有影响」和「证明影响很小」在数据上分得开，靠的是分数而不是档位。
func Band(score float64) string {
	switch {
	case score >= 9.0:
		return "critical"
	case score >= 7.0:
		return "high"
	case score >= 4.0:
		return "medium"
	default:
		return "low"
	}
}

// SeverityOf 把基础分映射成 CVSS v3.1 的原始档位（none / low / medium / high / critical）。
func SeverityOf(score float64) string {
	switch {
	case score <= 0:
		return "none"
	case score < 4.0:
		return "low"
	case score < 7.0:
		return "medium"
	case score < 9.0:
		return "high"
	default:
		return "critical"
	}
}

// SeverityLabel 是最终对外的严重度标签：none 档写成 info（Strix 同口径）。
func SeverityLabel(score float64) string {
	if s := SeverityOf(score); s != "none" {
		return s
	}
	return "info"
}

// baseScore 按 CVSS v3.1 规范算基础分。
//
//	ISCBase = 1 - (1-C) × (1-I) × (1-A)
//	Scope 未变： Impact = 6.42 × ISCBase
//	Scope 改变： Impact = 7.52 × (ISCBase - 0.029) - 3.25 × (ISCBase - 0.02)^15
//	Impact ≤ 0 → 基础分 = 0（三项影响全是 N：没有实证任何影响）
//	Exploitability = 8.22 × AV × AC × PR × UI
//	基础分 = Roundup(min((Impact + Exploitability) × [Scope 改变时 1.08], 10))
func baseScore(b Breakdown) (float64, error) {
	av, okAV := weightAV[b.AttackVector]
	ac, okAC := weightAC[b.AttackComplexity]
	ui, okUI := weightUI[b.UserInteraction]
	conf, okC := weightImpact[b.Confidentiality]
	integ, okI := weightImpact[b.Integrity]
	avail, okA := weightImpact[b.Availability]
	if !okAV || !okAC || !okUI || !okC || !okI || !okA {
		return 0, fmt.Errorf("%s%s", errVectorUnparsable, b.Vector())
	}
	var pr float64
	var okPR bool
	switch b.Scope {
	case "U":
		pr, okPR = weightPRUnchanged[b.PrivilegesRequired]
	case "C":
		pr, okPR = weightPRChanged[b.PrivilegesRequired]
	default:
		return 0, fmt.Errorf("%s%s", errVectorUnparsable, b.Vector())
	}
	if !okPR {
		return 0, fmt.Errorf("%s%s", errVectorUnparsable, b.Vector())
	}

	iscBase := 1 - (1-conf)*(1-integ)*(1-avail)
	exploitability := 8.22 * av * ac * pr * ui
	var impact float64
	if b.Scope == "U" {
		impact = 6.42 * iscBase
	} else {
		impact = 7.52*(iscBase-0.029) - 3.25*math.Pow(iscBase-0.02, 15)
	}
	// 规范 7.1 的第一步：Impact 子分不大于 0 时基础分恒为 0。
	// 少了这一条，Exploitability 会单独把分数顶上去——「三项影响全是 N、没证明任何影响」
	// 的发现会拿到 3.9 分，而这正是 Strix 的
	// test_cvss_without_demonstrated_impact_is_informational 专门钉住的场景。
	if impact <= 0 {
		return 0, nil
	}
	if b.Scope == "U" {
		return roundUp(math.Min(impact+exploitability, 10)), nil
	}
	return roundUp(math.Min(1.08*(impact+exploitability), 10)), nil
}

// roundUp 是 CVSS v3.1 规范里的 Roundup：取「不小于输入的最小一位小数」。
//
// 规范附录 A 的参考实现：
//
//	int_input = round_to_nearest_integer(input * 100000)
//	if int_input % 10000 == 0 { return int_input / 100000.0 }
//	else { return (floor(int_input / 10000) + 1) / 10.0 }
func roundUp(x float64) float64 {
	scaled := math.Round(x * 100000)
	if math.Mod(scaled, 10000) == 0 {
		return scaled / 100000
	}
	return (math.Floor(scaled/10000) + 1) / 10
}

// contains 报告 s 是否在 list 里。
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// pyValue 按 Python 的 repr 风格渲染一个原始值，用于复刻 Strix 的报错文案。
//
// Strix 那边的模板是 f"Invalid {name}: {breakdown.get(name)}"，插值走的是 str()：
// 字符串不带引号，缺失的键渲染成 None，数字与布尔各按 Python 的写法。
func pyValue(value any, present bool) string {
	if !present {
		return "None"
	}
	switch v := value.(type) {
	case string:
		return v
	case bool:
		if v {
			return "True"
		}
		return "False"
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	case int:
		return strconv.Itoa(v)
	case nil:
		return "None"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// pyList 按 Python 列表的 repr 风格渲染合法取值表（['N', 'A', 'L', 'P']）。
func pyList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, "'"+v+"'")
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
