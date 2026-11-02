package cvss

// CVSS v3.1 基础分的回归测试。
//
// 期望值全部来自 FIRST 的 CVSS v3.1 规范与官方计算器：这里的每一个数字都是
// 「同一个向量在 Strix（Python cvss 库）里也必须得到的那个数」。移植错一个
// 权重，模型校准过的严重度就会在两个系统里对不上——所以这组用例不是装饰。

import (
	"math"
	"strings"
	"testing"
)

// metricsMap 从「键, 值, 键, 值…」构造原始指标 map。
//
// 刻意不用 map 字面量：多键字面量的对齐由 gofmt 按最长键计算，手写很容易差一格，
// 而 gofmt 门禁就在 CI 上——差一格就是一次红门禁。
func metricsMap(pairs ...string) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

// vectorCase 是一个已知向量及其期望结果。
type vectorCase struct {
	Breakdown Breakdown
	Score     float64
	Severity  string
	Band      string
	Vector    string
}

// knownVectors 是手工按规范核算过的一组向量。
//
// 涵盖：满影响、单项影响、低分、Scope 改变、PR 升档、以及「没有实证任何影响」。
// 其中四条与 Strix 自己的夹具同源（9.8 / 7.5 / 3.0 / 全 N 得 info），分数必须与 Python
// 的 cvss 库逐个对上——对不上就说明「模型校准过的严重度」会在两个系统里分叉。
var knownVectors = []vectorCase{
	{
		Breakdown: Breakdown{"N", "L", "N", "N", "U", "H", "H", "H"},
		Score:     9.8,
		Severity:  "critical",
		Band:      "critical",
		Vector:    "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",
	},
	{
		// 单项影响（Strix 的 _DEP_CONTEXT 夹具）：只有可用性受影响 → 7.5。
		Breakdown: Breakdown{"N", "L", "N", "N", "U", "N", "N", "H"},
		Score:     7.5,
		Severity:  "high",
		Band:      "high",
		Vector:    "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H",
	},
	{
		// 低权限 + 高复杂度 + 低影响（Strix 的 _CONTEXTUAL_BREAKDOWN 夹具）：3.0。
		Breakdown: Breakdown{"L", "H", "H", "N", "U", "L", "L", "N"},
		Score:     3.0,
		Severity:  "low",
		Band:      "low",
		Vector:    "CVSS:3.1/AV:L/AC:H/PR:H/UI:N/S:U/C:L/I:L/A:N",
	},
	{
		Breakdown: Breakdown{"N", "L", "N", "N", "U", "L", "N", "N"},
		Score:     5.3,
		Severity:  "medium",
		Band:      "medium",
		Vector:    "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:N/A:N",
	},
	{
		Breakdown: Breakdown{"N", "L", "L", "N", "U", "H", "H", "H"},
		Score:     8.8,
		Severity:  "high",
		Band:      "high",
		Vector:    "CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:H/A:H",
	},
	{
		Breakdown: Breakdown{"N", "L", "N", "R", "C", "H", "H", "H"},
		Score:     9.6,
		Severity:  "critical",
		Band:      "critical",
		Vector:    "CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:C/C:H/I:H/A:H",
	},
	{
		Breakdown: Breakdown{"N", "L", "N", "N", "C", "H", "H", "H"},
		Score:     10.0,
		Severity:  "critical",
		Band:      "critical",
		Vector:    "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H",
	},
	{
		// 没有实证任何影响：分数 0，标签 info（Strix 的 test_cvss_without_demonstrated_impact_is_informational）。
		Breakdown: Breakdown{"N", "L", "N", "N", "U", "N", "N", "N"},
		Score:     0.0,
		Severity:  "info",
		Band:      "low",
		Vector:    "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N",
	},
}

// TestKnownVectorScores 钉住每个已知向量的分数、标签与向量串。
func TestKnownVectorScores(t *testing.T) {
	for _, tc := range knownVectors {
		score, severity, vector, err := Calculate(tc.Breakdown)
		if err != nil {
			t.Fatalf("%s：计算失败：%v", tc.Vector, err)
		}
		if math.Abs(score-tc.Score) > 1e-9 {
			t.Fatalf("%s：分数应为 %.1f，实得 %.4f", tc.Vector, tc.Score, score)
		}
		if severity != tc.Severity {
			t.Fatalf("%s：严重度标签应为 %q，实得 %q", tc.Vector, tc.Severity, severity)
		}
		if vector != tc.Vector {
			t.Fatalf("向量串应为 %q，实得 %q", tc.Vector, vector)
		}
		if got := Band(score); got != tc.Band {
			t.Fatalf("%s：ARTEX 档位应为 %q，实得 %q", tc.Vector, tc.Band, got)
		}
	}
}

// TestCalculateRejectsIllegalMetrics 断言取值非法时算分失败而不是给一个假分。
//
// 假分比不评分更危险：模型会拿它去校准严重度。
func TestCalculateRejectsIllegalMetrics(t *testing.T) {
	bad := Breakdown{"X", "L", "N", "N", "U", "H", "H", "H"}
	if _, _, _, err := Calculate(bad); err == nil {
		t.Fatal("非法的 attack_vector 应当算分失败")
	} else if !strings.Contains(err.Error(), "Failed to calculate CVSS for validated vector") {
		t.Fatalf("报错应说明向量算不出来，实得：%v", err)
	}
	badScope := Breakdown{"N", "L", "N", "N", "X", "H", "H", "H"}
	if _, _, _, err := Calculate(badScope); err == nil {
		t.Fatal("非法的 scope 应当算分失败")
	}
}

// TestValidateErrorTextMatchesStrix 钉住校验报错文案。
//
// 文案是给模型看的：改了它，模型就得重新学一遍这些字串各自意味着什么。
func TestValidateErrorTextMatchesStrix(t *testing.T) {
	t.Run("空对象", func(t *testing.T) {
		errs := Validate(map[string]any{})
		if len(errs) != 1 || errs[0] != "cvss_breakdown: must be an object with the 8 CVSS metrics" {
			t.Fatalf("空对象的报错不对：%v", errs)
		}
	})
	t.Run("缺项与非法值", func(t *testing.T) {
		raw := metricsMap(
			"attack_vector", "X",
			"attack_complexity", "L",
			"privileges_required", "N",
			"user_interaction", "N",
			"scope", "U",
			"confidentiality", "H",
			"integrity", "H",
			// availability 缺失
		)
		errs := Validate(raw)
		if len(errs) != 2 {
			t.Fatalf("应报两条错（attack_vector 非法 + availability 缺失），实得 %d 条：%v", len(errs), errs)
		}
		if errs[0] != "Invalid attack_vector: X. Must be one of: ['N', 'A', 'L', 'P']" {
			t.Fatalf("第一条报错文案不对：%q", errs[0])
		}
		if errs[1] != "Invalid availability: None. Must be one of: ['N', 'L', 'H']" {
			t.Fatalf("第二条报错文案不对：%q", errs[1])
		}
	})
	t.Run("合法取值不报错", func(t *testing.T) {
		raw := metricsMap(
			"attack_vector", "N",
			"attack_complexity", "L",
			"privileges_required", "N",
			"user_interaction", "N",
			"scope", "U",
			"confidentiality", "H",
			"integrity", "H",
			"availability", "H",
		)
		if errs := Validate(raw); len(errs) != 0 {
			t.Fatalf("合法取值不该报错：%v", errs)
		}
	})
}

// TestCanonicalJSONKeyOrder 钉住落库 JSON 的键序。
func TestCanonicalJSONKeyOrder(t *testing.T) {
	b := Breakdown{"N", "L", "N", "N", "U", "H", "H", "H"}
	want := `{"attack_vector":"N","attack_complexity":"L","privileges_required":"N",` +
		`"user_interaction":"N","scope":"U","confidentiality":"H","integrity":"H","availability":"H"}`
	if got := b.CanonicalJSON(); got != want {
		t.Fatalf("规范化 JSON 不对：\n得到 %s\n期望 %s", got, want)
	}
}

// TestParseIgnoresIllegalValues 断言 Parse 只搬运不裁决。
func TestParseIgnoresIllegalValues(t *testing.T) {
	raw := map[string]any{"attack_vector": "X", "scope": 3}
	b := Parse(raw)
	if b.AttackVector != "X" {
		t.Fatalf("Parse 应原样搬运 attack_vector，实得 %q", b.AttackVector)
	}
	if b.Scope != "" {
		t.Fatalf("非字符串的 scope 应落成空串，实得 %q", b.Scope)
	}
}
