package agent

// 反证闭环的回归测试（移植自 Strix 的 _validate_analysis_fields）。
//
// 这套断言的意义不在「字段非空」这种表面校验，而在于它把「上报前必须主动构造
// 反证」这件事从提示词里的软要求变成了代码里的硬闸门——模型可以忘记，代码不会。

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// validAnalysis 是全部字段都合格的样本，各用例在它基础上挖空某一项。
func validAnalysis() findingAnalysisFields {
	return findingAnalysisFields{
		Counterevidence:          "该路径未见输入校验、WAF 或鉴权检查；已分别以已登录/未登录测过",
		Confidence:               "high",
		SeverityChangeConditions: "确认该路由暴露在未认证公网流量下则升为 critical",
	}
}

func TestValidateFindingAnalysisAcceptsCompleteClosure(t *testing.T) {
	if errs := validateFindingAnalysis(validAnalysis()); len(errs) != 0 {
		t.Fatalf("合格样本不该报错：%v", errs)
	}
	// medium 带上 rationale 也应通过：这是最常见的真实形态。
	a := validAnalysis()
	a.Confidence = "medium"
	a.ConfidenceRationale = "仅有静态链路，没能起服务复现"
	if errs := validateFindingAnalysis(a); len(errs) != 0 {
		t.Fatalf("medium + rationale 应通过：%v", errs)
	}
}

func TestValidateFindingAnalysisRejectsEmptyCounterevidence(t *testing.T) {
	// 空白也算空：模型常用一串空格或「无」来敷衍通过非空检查。
	for _, bad := range []string{"", "   ", "\n\t", "无", "none"} {
		// 「无」「none」不是空白，本函数不该拦——它们属于内容质量问题，
		// 由 schema 描述里的样例约束，不是代码能可靠判定的。
		a := validAnalysis()
		a.Counterevidence = bad
		errs := validateFindingAnalysis(a)
		if strings.TrimSpace(bad) == "" {
			if len(errs) == 0 {
				t.Fatalf("counterevidence=%q 应被拒", bad)
			}
			if !strings.Contains(errs[0], "counterevidence") {
				t.Fatalf("报错应点名 counterevidence，实得：%s", errs[0])
			}
			if !strings.Contains(errs[0], "查过") {
				t.Fatalf("报错应给出合格样例（可行动），实得：%s", errs[0])
			}
			continue
		}
		if len(errs) != 0 {
			t.Fatalf("counterevidence=%q 内容质量差但非空，代码层不该拦：%v", bad, errs)
		}
	}
}

func TestValidateFindingAnalysisRejectsEmptySeverityChangeConditions(t *testing.T) {
	for _, bad := range []string{"", "  ", "\t"} {
		a := validAnalysis()
		a.SeverityChangeConditions = bad
		errs := validateFindingAnalysis(a)
		if len(errs) == 0 {
			t.Fatalf("severity_change_conditions=%q 应被拒", bad)
		}
		if !strings.Contains(errs[0], "severity_change_conditions") {
			t.Fatalf("报错应点名字段，实得：%s", errs[0])
		}
	}
}

func TestValidateFindingAnalysisRejectsBadConfidence(t *testing.T) {
	for _, bad := range []string{"", "HIGH", "High", "certain", "medium-high", "observed"} {
		a := validAnalysis()
		a.Confidence = bad
		errs := validateFindingAnalysis(a)
		if len(errs) == 0 {
			t.Fatalf("confidence=%q 应被拒", bad)
		}
		// 报错必须把合法取值列出来，否则模型只能猜。
		if !strings.Contains(errs[0], "high") {
			t.Fatalf("报错应列出合法取值，实得：%s", errs[0])
		}
	}
	// record_fact 的 confidence 取值（observed/inferred）不能混进来。
	a := validAnalysis()
	a.Confidence = "observed"
	if errs := validateFindingAnalysis(a); len(errs) == 0 {
		t.Fatal("record_fact 的 observed 不是 report_finding 的合法置信度")
	}
}

func TestValidateFindingAnalysisRequiresRationaleBelowHigh(t *testing.T) {
	for _, conf := range []string{"medium", "low"} {
		a := validAnalysis()
		a.Confidence = conf
		errs := validateFindingAnalysis(a)
		if len(errs) == 0 {
			t.Fatalf("confidence=%s 缺 rationale 应被拒", conf)
		}
		if !strings.Contains(errs[0], "confidence_rationale") {
			t.Fatalf("报错应点名 confidence_rationale，实得：%s", errs[0])
		}
		// 补上就通过。
		a.ConfidenceRationale = "该路由能否从未认证流量触达未确认"
		if errs := validateFindingAnalysis(a); len(errs) != 0 {
			t.Fatalf("confidence=%s 补了 rationale 应通过，实得 %v", conf, errs)
		}
	}
	// high 不需要 rationale。
	a := validAnalysis()
	a.Confidence, a.ConfidenceRationale = "high", ""
	if errs := validateFindingAnalysis(a); len(errs) != 0 {
		t.Fatalf("high 不该要求 rationale：%v", errs)
	}
}

// TestReportFindingRejectsMissingAnalysis 验证闸门真的装在了工具入口上，
// 而不只是有个孤立的函数。
func TestReportFindingRejectsMissingAnalysis(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTask("analysis gate", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	ts := NewToolSet(nil, "")
	ts.ts, ts.taskID = d.Exploration(task.ExplorationID), task.ID
	ts.SetFindingRecorder(nil)

	// 缺全部四项：应被拒，且报错把四项缺口一次说清（模型一轮就能补全）。
	res, err := ts.addFinding().Call(t.Context(), json.RawMessage(
		`{"vulnclass":"TEST","severity":"low","summary":"no analysis"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := res.Flatten()
	for _, want := range []string{
		"counterevidence", "severity_change_conditions", "confidence",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("报错应点名 %s，实得：%s", want, got)
		}
	}
	if ts.writes.Findings != 0 {
		t.Fatal("被拒的上报不该计数为一次成功写入")
	}
}

// TestReportFindingPersistsAnalysis 验证四字段真的落库、能读回来——
// 只校验不落库的话，复核与报告阶段看不到，闸门等于白装（存进去读不出来的字段
// 比没有这个字段更糟：它会让人以为这里已经把关了）。
func TestReportFindingPersistsAnalysis(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTask("analysis persist", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	ts := NewToolSet(nil, "")
	ts.ts, ts.taskID = d.Exploration(task.ExplorationID), task.ID
	ts.worker = "fixture"

	const (
		wantCE   = "后端使用参数化查询，注入点实际被 PDO 预处理吞掉；已确认该路径走 prepare()"
		wantConf = "medium"
		wantRat  = "仅静态链路，未能在目标上完整复现"
		wantSCC  = "若能在真实目标上 dump 出库名则升为 critical"
	)
	res, err := ts.addFinding().Call(t.Context(), json.RawMessage(`{
		"vulnclass":"SQLI","severity":"high","summary":"persisted analysis fixture",
		"evidence":"id=1' AND 1=1-- returns full table",
		"counterevidence":"`+wantCE+`",
		"confidence":"`+wantConf+`",
		"confidence_rationale":"`+wantRat+`",
		"severity_change_conditions":"`+wantSCC+`"
	}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitN(res.Flatten(), "\n", 2)
	if len(lines) != 2 {
		t.Fatalf("上报失败：%s", res.Flatten())
	}
	var out db.RecordedFinding
	if err := json.Unmarshal([]byte(lines[1]), &out); err != nil {
		t.Fatal(err)
	}
	f, err := d.GetFinding(out.FindingID)
	if err != nil || f == nil {
		t.Fatalf("读回落库记录失败：%+v %v", f, err)
	}
	// 落库 → 读回，四字段逐一核对。
	if f.Counterevidence != wantCE {
		t.Errorf("counterevidence 未落库：%q", f.Counterevidence)
	}
	if f.Confidence != wantConf {
		t.Errorf("confidence 未落库：%q", f.Confidence)
	}
	if f.ConfidenceRationale != wantRat {
		t.Errorf("confidence_rationale 未落库：%q", f.ConfidenceRationale)
	}
	if f.SeverityChangeConditions != wantSCC {
		t.Errorf("severity_change_conditions 未落库：%q", f.SeverityChangeConditions)
	}
	// 列表查询走的是同一套 findingSelectCols，也必须带上，否则详情页有、列表页没有。
	page, _, err := d.ListFindingsPage(db.FindingFilter{TaskID: strconv.FormatInt(task.ID, 10)}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range page {
		if it.ID == out.FindingID {
			found = true
			if it.Confidence != wantConf || it.Counterevidence != wantCE {
				t.Errorf("列表查询丢掉了反证字段：confidence=%q counterevidence=%q", it.Confidence, it.Counterevidence)
			}
		}
	}
	if !found {
		t.Fatal("新漏洞没出现在列表查询里")
	}
}
