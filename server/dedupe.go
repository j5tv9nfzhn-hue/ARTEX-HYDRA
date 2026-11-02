package server

// ── 判重（移植自 Strix 的 strix/report/dedupe.py）────────────────────────────
//
// 判重是「落库前」的一道闸门：一条候选发现是不是本任务已有发现的同一个漏洞，由模型
// 判。判为重复就不落库，模型会收到「疑似重复、别再报」的明确回执——这与 Strix 的
// 口径一致（那边是丢弃候选并回一条 duplicate 错误）。
//
// 三条不可让步的性质：
//  1. 总是判：没有「按严重度跳过」，也没有采样（Strix 那边同样没有）。判重是上报
//     链路上的一次额外模型调用，但漏判的代价（同一件事在报告里出现三遍）更高。
//  2. fail-open：模型不可用 / 超时 / 回包不是 JSON 一律放行。挡住一条真发现的
//     代价远大于多留一条重复条目。
//  3. 专用模型可隔离：配了 dedupe_profile_id 就用它（端点与凭据都是它自己的，绝不
//     继承主配置的），没配就回退到当前启用的配置——与 Strix 的 dedupe.model 回退
//     主模型同构。

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/dedupe"
	"github.com/Autumn-27/norma/llm"
)

// 判重的系统设置项。
const (
	// settingDedupeEnabled 关掉后不再判重（默认开）。
	settingDedupeEnabled = "dedupe_enabled"
	// settingDedupeProfileID 判重专用模型配置 id（0 = 用当前启用的配置）。
	settingDedupeProfileID = "dedupe_profile_id"
	// settingDedupeTimeout 单次判重的超时秒数。
	settingDedupeTimeout = "dedupe_timeout_seconds"
)

// dedupeExistingLimit 是参与比对的已有发现条数上限。
//
// 判重是一次调用带上全部已有发现（Strix 同口径，不分批）：分批会让「同一条漏洞被
// 拆到两批里各判一次不重复」。上限只是防载荷无限膨胀——一个任务几十条发现是常态。
const dedupeExistingLimit = 50

// dedupeTimeoutDefault 是单次判重的默认超时。
const dedupeTimeoutDefault = 60 * time.Second

// dedupeService 是 server 侧的判重实现。
//
// provider 由 Server 注入：引擎侧只有 Manager，拿不到 Server 的 provider 池，而
// 「用哪个模型判」是 server 的配置问题。
type dedupeService struct {
	pg       *db.DB
	provider func(profileID int64) (llm.Provider, bool)
}

// Check 判断候选发现是不是本任务已有发现的重复。
//
// 判重失败返回 error；agent 侧据此放行（fail-open），详见 agent/finding_cvss.go。
func (d *dedupeService) Check(ctx context.Context, taskID int64, candidate dedupe.Candidate) (dedupe.Result, error) {
	if d == nil || d.pg == nil {
		return dedupe.Result{}, errors.New("判重未配置")
	}
	if !d.enabled() {
		return dedupe.Result{IsDuplicate: false}, nil
	}
	existing, err := d.existing(taskID)
	if err != nil {
		return dedupe.Result{}, err
	}
	if len(existing) == 0 {
		// 本任务还没有别的发现，没有可比对象：判重不调模型（Strix 同口径）。
		return dedupe.Result{IsDuplicate: false, Confidence: 1.0}, nil
	}
	if d.provider == nil {
		return dedupe.Result{}, errors.New("判重模型未配置")
	}
	profileID, timeout := d.policy()
	prov, ok := d.provider(profileID)
	if !ok {
		return dedupe.Result{}, errors.New("没有可用的判重模型")
	}
	judge := &dedupe.Judge{Provider: prov, Timeout: timeout}
	return judge.Check(ctx, candidate, existing)
}

// dedupeEnabled 报告判重是否开启。默认开：判重是上报质量的一部分，不是可选装饰。
//
// 三个设置项的读取口径集中在这几个函数里：默认值与解析规则只有一处，
// 「判重时怎么读」与「设置接口回显什么」问的是同一段代码，不会各写一套后走偏。
func dedupeEnabled(pg *db.DB) bool {
	if pg == nil {
		return true
	}
	raw, ok, err := pg.GetSetting(settingDedupeEnabled)
	if err != nil || !ok {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "off", "0", "false", "no":
		return false
	}
	return true
}

// dedupeProfileID 读判重专用模型配置 id；0 = 用当前启用的配置（与 Strix 的
// dedupe.model 回退主模型同构）。
func dedupeProfileID(pg *db.DB) int64 {
	if pg == nil {
		return 0
	}
	raw, ok, err := pg.GetSetting(settingDedupeProfileID)
	if err != nil || !ok {
		return 0
	}
	n, convErr := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if convErr != nil || n <= 0 {
		return 0
	}
	return n
}

// dedupeTimeout 读单次判重超时。非法值回落到默认值而不是取 0：超时为 0 在
// dedupe.Judge 里表示「不限时」，一次卡住的判重会把上报永久挂住。
func dedupeTimeout(pg *db.DB) time.Duration {
	if pg == nil {
		return dedupeTimeoutDefault
	}
	raw, ok, err := pg.GetSetting(settingDedupeTimeout)
	if err != nil || !ok {
		return dedupeTimeoutDefault
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(raw))
	if convErr != nil || n <= 0 {
		return dedupeTimeoutDefault
	}
	return time.Duration(n) * time.Second
}

// dedupeTimeoutSecs 是 dedupeTimeout 的秒数形式，给设置接口回显用。
func dedupeTimeoutSecs(pg *db.DB) int { return int(dedupeTimeout(pg) / time.Second) }

// enabled 报告判重是否开启（口径见 dedupeEnabled）。
func (d *dedupeService) enabled() bool { return dedupeEnabled(d.pg) }

// policy 读判重口径：专用模型 id 与单次超时。
func (d *dedupeService) policy() (profileID int64, timeout time.Duration) {
	return dedupeProfileID(d.pg), dedupeTimeout(d.pg)
}

// existing 取本任务最近的已有发现，整理成判重载荷。
func (d *dedupeService) existing(taskID int64) ([]dedupe.Candidate, error) {
	if taskID <= 0 {
		return nil, nil
	}
	findings, _, err := d.pg.ListFindingsPage(db.FindingFilter{TaskID: strconv.FormatInt(taskID, 10)}, 1, dedupeExistingLimit)
	if err != nil {
		return nil, err
	}
	out := make([]dedupe.Candidate, 0, len(findings))
	for _, f := range findings {
		out = append(out, dedupe.Candidate{
			ID:                f.ID,
			Title:             dedupeFindingTitle(f),
			Description:       f.Summary,
			Impact:            dedupeImpact(f.Severity, f.CVSS),
			Target:            dedupeAssetIDs(f.AssetIDs),
			TechnicalAnalysis: f.Counterevidence,
			PoC:               f.Evidence,
		})
	}
	return out, nil
}

// dedupeFindingTitle 取发现的可读标题：名称 → 类型 → 未分类。
func dedupeFindingTitle(f *db.DBFinding) string {
	if name := strings.TrimSpace(f.Name); name != "" {
		return name
	}
	if class := strings.TrimSpace(f.VulnClass); class != "" {
		return class
	}
	return "未分类"
}

// dedupeImpact 拼出 impact 字段：严重度 + CVSS 分。
func dedupeImpact(severity, score string) string {
	parts := make([]string, 0, 2)
	if s := strings.TrimSpace(severity); s != "" {
		parts = append(parts, "severity="+s)
	}
	if s := strings.TrimSpace(score); s != "" {
		parts = append(parts, "cvss="+s)
	}
	return strings.Join(parts, " ")
}

// dedupeAssetIDs 拼出 target 字段：受影响资产 id 列表。
func dedupeAssetIDs(ids []int64) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ",")
}
