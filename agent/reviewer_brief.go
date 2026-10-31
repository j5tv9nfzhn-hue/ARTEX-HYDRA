package agent

// Reviewer 判决摘要的**读取侧**声明。
//
// Reviewer 本体（reviewer.go）已随旧调度链下线，但注入面的读取分支还挂在
// tools.go：它按「这个意图有没有判决」决定一条 fact 是注入正文，还是只留一个
// 计数。这里只补回读取面引用的四个声明，不恢复 Reviewer，也不动 tools.go 的
// 分支结构。
//
// 钩子默认 nil：没人接线时注入面按「无判决」走（正文照常注入，
// facts_unreviewed 与 reviewer_verdicts 都不出现）—— 这正是 Reviewer 下线后
// 的实际形态，不是新引入的行为。等新的判决来源（子 agent 的 Brief / verdict）
// 接上来时在这里注册即可，读取侧一行不用改。

// ReviewerVerdict 是 Reviewer 对一次 worker run 的终局判决。
type ReviewerVerdict string

// ReviewerAttribution 是失败归因。它决定 Planner 下一步该换什么，而不是换不换。
type ReviewerAttribution string

// ReviewerBrief 是给 Planner 注入面用的判决摘要（比 ReviewerResult 更窄）。
type ReviewerBrief struct {
	Verdict     ReviewerVerdict     `json:"verdict"`
	Attribution ReviewerAttribution `json:"attribution"`
	Summary     string              `json:"summary"`
	Confidence  float64             `json:"confidence"`
	Facts       []string            `json:"facts,omitempty"`
	Clues       []string            `json:"clues,omitempty"`
	Negative    []string            `json:"negative,omitempty"`
	// Evidence 是可供 fetch_evidence 用的快照 id。Planner 只有看见它才谈得上取证。
	Evidence []int64 `json:"evidence_ref,omitempty"`
}

// ReviewerVerdictsForIntents 由 server 侧接线：给定一批意图 id，返回其中已有判决的
// 那些。键是意图 id（全局唯一自增），所以这个钩子天然按任务隔离，不会串台。
//
// 注入面用它做两件事（见 tools.go）：
//  1. 有判决的意图 → 只注入判决摘要，不注入原始 fact 正文；
//  2. 没判决的意图 → 其 fact 被折叠成一个计数 facts_unreviewed，正文不进 Planner。
//
// 这就是「Worker 不能把未清洗数据直接递给 Planner」在读取侧的落点。
var ReviewerVerdictsForIntents func(ids []int64) map[int64]ReviewerBrief
