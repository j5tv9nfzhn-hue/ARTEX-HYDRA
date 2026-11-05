package server

// TUI 后端：把 Server 的进程内能力暴露给 tui 包。
//
// 这一层替代了原先的 server/cli.go：CLI 与 TUI 共用同一批业务语义
// （建任务、订阅活动流、投递指令、读漏洞、任务控制），只有呈现方式不同。
// 把它们收敛到一处，是为了让"终端里看到的行为"和"Web 里看到的行为"
// 不可能分叉——同一份 artex.db、同一套 coord 语义、同一批退出码口径。

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/tui"
)

// TUISession 是 TUI 与 Server 之间的适配器，实现 tui.TUIBackend。
type TUISession struct {
	srv *Server

	// lastSig 是上一次见到的激活配置签名，用于检测 Web 控制台侧的变更。
	lastSig string

	// keepAlive 持有订阅的取消函数，会话结束时统一释放。
	keepAlive []func()
}

// NewTUISession 构造一个 TUI 会话。
//
// 装配前先做两项体检：没有可用的 LLM 配置就明确拒绝（把用户引到 Web 控制台），
// 非 coord 编排模式同样拒绝——TUI 的"给编排者下指令"依赖根 agent 这条链路。
func (s *Server) NewTUISession() (*TUISession, error) {
	if s == nil || s.m == nil || s.engine == nil {
		return nil, errors.New("TUI 未装配")
	}
	if _, ok := s.loadLLMConfig(); !ok {
		return nil, errors.New("尚未配置可用的 LLM：请在 Web 控制台「系统 → LLM」添加并启用一个配置（TUI 只从 artex.db 读取配置）")
	}
	if !agent.CoordEnabled() {
		return nil, errors.New("TUI 需要 coord 编排模式（ARTEX_SCHED=coord）；当前是 legacy 模式")
	}
	return &TUISession{srv: s, lastSig: s.tuiProfileSignature()}, nil
}

// CreateTask 按授权目标建一个任务并启动编排。
//
// 走 Manager.CreateTaskWithOptions 与 launchTask，与 Web「新建任务」同构：
// 不直接写库，保证种子目标、目标分解、并发准入、LLM 链解析没有第二套实现。
func (ts *TUISession) CreateTask(ctx context.Context, targets []string, instruction string, opts tui.TaskOptions) (string, error) {
	s := ts.srv
	targets = normalizeTUITargets(targets)
	if len(targets) == 0 {
		return "", errors.New("缺少目标：请指定授权目标，例如 artex tui -t https://example.com")
	}

	desc := strings.TrimSpace(opts.Name)
	if desc == "" {
		desc = "TUI · " + targets[0]
		if len(targets) > 1 {
			desc = fmt.Sprintf("TUI · %s 等 %d 个目标", targets[0], len(targets))
		}
	}
	goal := "目标：\n- " + strings.Join(targets, "\n- ")
	if ins := strings.TrimSpace(instruction); ins != "" {
		goal += "\n\n任务要求：\n" + ins
	}
	goal += "\n\n授权范围：仅限上述目标；开始前请确认已获得明确授权。"

	coverage := true
	t, err := s.m.CreateTaskWithOptions(desc, goal, db.TaskCreateOptions{
		Name:            strings.TrimSpace(opts.Name),
		TimeoutSeconds:  opts.TimeoutSeconds,
		TokenBudget:     s.m.DefaultTaskTokenBudget(),
		CoverageEnabled: &coverage,
	})
	if err != nil {
		return "", err
	}
	// 先订阅再启动：目标分解、根 agent 第一轮与首个工具调用都必须被看见，
	// 否则用户看到的是「已经跑了一会儿」的半截流。
	acts, unsubscribe := s.engine.Broadcaster().Subscribe(t.ID)
	live, unsubscribeLive := s.engine.Broadcaster().SubscribeLive(t.ID)
	s.launchTask(t, t.Description+" "+t.Goal, false)

	// 订阅的取消函数随会话生命周期走：TUI 退出时统一释放。
	// 这里仍各自建一份订阅：CreateTask 侧的订阅保证"启动瞬间不漏事件"，
	// TUI 随后自己再订一份（Subscribe 每次返回独立通道），两路并存互不干扰。
	ts.keepAlive = append(ts.keepAlive, unsubscribe, unsubscribeLive)
	_ = acts
	_ = live
	log.Printf("[tui] task #%s created (targets=%s)", t.ID, strings.Join(targets, ","))
	return t.ID, nil
}

// Subscribe 返回任务的活动流。
//
// 落库活动与实时增量合并成一条流：TUI 只消费一个 channel，
// 不必关心"哪些是最终的、哪些是正在写的"。
func (ts *TUISession) Subscribe(ctx context.Context, taskID string) (<-chan tui.Activity, func()) {
	s := ts.srv
	out := make(chan tui.Activity, 64)
	if taskID == "" {
		close(out)
		return out, func() {}
	}
	acts, unsubActs := s.engine.Broadcaster().Subscribe(taskID)
	live, unsubLive := s.engine.Broadcaster().SubscribeLive(taskID)

	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case a, ok := <-acts:
				if !ok {
					return
				}
				select {
				case out <- tui.Activity{
					Kind:    a.Kind,
					Worker:  a.Worker,
					Tool:    a.Tool,
					Summary: a.Summary,
					IsError: a.IsError,
				}:
				case <-ctx.Done():
					return
				}
			case f, ok := <-live:
				if !ok {
					return
				}
				// 只有 thinking / text 走实时通道；工具调用等以落库活动为准。
				if f.Kind != "thinking" && f.Kind != "text" {
					continue
				}
				select {
				case out <- tui.Activity{
					Kind:    f.Kind,
					Worker:  f.Worker,
					Summary: f.Text,
				}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	cancel := func() {
		unsubActs()
		unsubLive()
	}
	return out, cancel
}

// Send 把人类输入作为 instruction 投给根 agent。
//
// 用 instruction 而不是 information：人在终端里说话是在改变做法，不是补充背景。
// 与 server.chat 的 coord 分支保持同一语义，避免 Web/TUI 两套行为。
func (ts *TUISession) Send(ctx context.Context, taskID, text string) error {
	if !agent.CoordEnabled() {
		return errors.New("当前不是 coord 编排模式，TUI 需要 ARTEX_SCHED=coord")
	}
	ct, ok := coordGet(ts.srv.engine, taskID)
	if !ok || ct == nil || ct.run == nil || ct.run.Coord == nil || ct.run.RootID == "" {
		return errors.New("编排者已收尾，无法再接收指令")
	}
	_, err := ct.run.Coord.Send("human", ct.run.RootID, agent.MsgInstruction, text)
	return err
}

// TaskStatus 返回任务的当前状态。
func (ts *TUISession) TaskStatus(ctx context.Context, taskID string) (tui.TaskState, error) {
	s := ts.srv
	t, ok := s.m.Task(taskID)
	if !ok {
		return tui.TaskState{}, fmt.Errorf("task %s not found", taskID)
	}
	state := tui.TaskState{ID: t.ID, Status: s.resolvedTaskStatus(t)}
	if metric, err := s.m.PG().TaskListMetricsOne(taskIDInt64(t.ID)); err == nil && metric != nil {
		state.Findings = tui.SeverityCounts{
			Critical: metric.Findings.Critical,
			High:     metric.Findings.High,
			Medium:   metric.Findings.Medium,
			Low:      metric.Findings.Low,
		}
		state.Tokens = metric.Tokens.InputTokens + metric.Tokens.OutputTokens +
			metric.Tokens.CacheReadTokens + metric.Tokens.CacheWriteTokens
		state.InFlight = metric.RunningIntents
		state.GoalsTotal = metric.Goals.Total
		state.GoalsMet = metric.Goals.Met
	}
	if t.FirstRunAt > 0 {
		state.StartedAt = time.Unix(t.FirstRunAt, 0)
	}
	return state, nil
}

// Findings 返回任务已记录的漏洞摘要。
func (ts *TUISession) Findings(ctx context.Context, taskID string) ([]tui.Finding, error) {
	s := ts.srv
	if taskID == "" {
		return nil, nil
	}
	fs, _, err := s.m.pg.ListFindingsPage(db.FindingFilter{TaskID: taskID, Sort: "severity"}, 1, 200)
	if err != nil {
		return nil, err
	}
	out := make([]tui.Finding, 0, len(fs))
	for _, f := range fs {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			name = f.VulnClass
		}
		out = append(out, tui.Finding{
			ID:        f.ID,
			Severity:  strings.ToLower(strings.TrimSpace(f.Severity)),
			Name:      name,
			VulnClass: f.VulnClass,
			Status:    f.Status,
			Summary:   f.Summary,
		})
	}
	return out, nil
}

// Control 对任务执行控制动作。
func (ts *TUISession) Control(ctx context.Context, taskID, action string) error {
	s := ts.srv
	t, ok := s.m.Task(taskID)
	if !ok {
		return fmt.Errorf("task %s not found", taskID)
	}
	_, err := s.applyTaskControl(t, action)
	return err
}

// ModelLabel 返回当前生效的 LLM 配置标签。
func (ts *TUISession) ModelLabel(ctx context.Context) string {
	return ts.srv.tuiModelLabel()
}

// ActiveModelChanged 检测 Web 控制台侧是否改了激活配置。
//
// 配置只有 Web 一个来源（写入 artex.db），TUI 这边只读不写：
// 签名变了就重建 provider，在跑的回合继续用旧配置，下一个回合自动切换。
func (ts *TUISession) ActiveModelChanged(ctx context.Context) (bool, string) {
	sig := ts.srv.tuiProfileSignature()
	if sig == ts.lastSig {
		return false, ""
	}
	ts.lastSig = sig
	ts.srv.reapplyActiveProfile()
	return true, ts.srv.tuiModelLabel()
}

// ListTasks 返回任务列表摘要。
//
// 这是 tui.TUIBackend 的可选扩展：TUI 通过类型断言拿它，拿不到就显示空列表。
func (ts *TUISession) ListTasks(ctx context.Context) ([]tui.TaskSummary, error) {
	s := ts.srv
	list := s.m.List()
	metrics, _ := s.m.PG().TaskListMetricsAll()
	out := make([]tui.TaskSummary, 0, len(list))
	for _, t := range list {
		item := tui.TaskSummary{
			ID:     t.ID,
			Name:   taskDisplayName(t),
			Status: s.resolvedTaskStatus(t),
		}
		if m, ok := metrics[t.ExpID]; ok {
			item.Findings = m.Findings.Critical + m.Findings.High + m.Findings.Medium + m.Findings.Low
			item.UpdatedAt = time.Unix(m.LastActivity, 0)
		}
		out = append(out, item)
	}
	return out, nil
}

// Close 释放会话持有的订阅。
func (ts *TUISession) Close() {
	for _, cancel := range ts.keepAlive {
		cancel()
	}
	ts.keepAlive = nil
}

// tuiProfileSignature 汇总激活 LLM 配置的关键字段，供 TUI 检测配置变更。
func (s *Server) tuiProfileSignature() string {
	if s == nil || s.m == nil || s.m.pg == nil {
		return ""
	}
	p, err := s.m.pg.ActiveProfile()
	if err != nil || p == nil {
		return ""
	}
	return fmt.Sprintf("%d|%s|%s|%s|%t|%d|%d|%s|%s",
		p.ID, p.Model, p.BaseURL, p.APIKey, p.Streaming, p.MaxTokens, p.ContextWindowK, p.ThinkingType, p.ReasoningEffort)
}

// tuiModelLabel 返回「模型（配置名）」形式的可读标签。
func (s *Server) tuiModelLabel() string {
	if s == nil || s.m == nil || s.m.pg == nil {
		return ""
	}
	p, err := s.m.pg.ActiveProfile()
	if err != nil || p == nil {
		return ""
	}
	if name := strings.TrimSpace(p.Name); name != "" {
		return fmt.Sprintf("%s · %s", p.Model, name)
	}
	return p.Model
}

// normalizeTUITargets 把目标列表切分并去重。
func normalizeTUITargets(raw []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range raw {
		fields := strings.FieldsFunc(item, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
		})
		for _, f := range fields {
			f = strings.TrimSpace(f)
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// taskDisplayName 返回任务的展示名（未命名时回退描述）。
func taskDisplayName(t *Task) string {
	if t == nil {
		return ""
	}
	if name := strings.TrimSpace(t.Name); name != "" {
		return name
	}
	return strings.TrimSpace(t.Description)
}

// taskIDInt64 把任务 id 转成整数形式。
//
// 任务 id 在 server 层是字符串（tasks.id 的文本形式），而聚合查询按整数主键走；
// 转换失败返回 0，调用方据此跳过查询而不是报错——列表页不该因为一条脏 id 整页失败。
func taskIDInt64(id string) int64 {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
