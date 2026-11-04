package server

// 终端 HARNESS —— artex cli 的进程内实现。
//
// 设计口径（与 Web 侧 HARNESS 对话页切割）：
//   - 不绑定端口、不依赖浏览器：直接复用已装配的 Manager/Engine，在进程内跑一个
//     coord 任务（根 agent 编排、子 agent 按需派生）；
//   - 一个最终出口：任务由根 agent 的 finish_task 收尾，CLI 只负责把活动流翻译成
//     终端可读的行，并在结束时汇总发现；
//   - 人类输入走与 Web 完全相同的语义：把文字作为 instruction 投给根 agent，
//     不是另起一个对话 agent（那会形成两套互不相通的上下文）。
//
// 这里刻意不做 TUI：逐行输出 + 行式 REPL，任何终端、CI、管道都能用。

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/config"
	"github.com/Autumn-27/artex/db"
)

// CLIOptions 是终端 HARNESS 的输入。空字段使用调用方给的默认值。
type CLIOptions struct {
	// Targets 是授权目标（URL / 域名 / IP）。为空时交互模式会提示输入。
	Targets []string
	// Instruction 是给根 agent 的额外要求（可空）。
	Instruction string
	// Name 是任务名（可空，默认按目标生成）。
	Name string
	// TimeoutSeconds 是任务级超时；0 = 不限。
	TimeoutSeconds int
	// Interactive 为 true 时开启行式 REPL（输入文字即向根 agent 下指令）。
	Interactive bool
	// Verbose 为 true 时额外显示 thinking 流。
	Verbose bool
	// Color 为 true 时输出 ANSI 颜色。
	Color bool
	// In / Out 便于测试注入；空则用 stdin/stdout。
	In  io.Reader
	Out io.Writer
}

// CLIFinding 是结束摘要里的一条漏洞。
type CLIFinding struct {
	ID        int64
	Severity  string
	Name      string
	VulnClass string
	Status    string
	Summary   string
}

// CLIResult 是终端 HARNESS 的最终结果。退出码由调用方按 Status/Findings 决定。
type CLIResult struct {
	TaskID   string
	Status   string
	Findings []CLIFinding
}

var (
	errCLINoLLM    = errors.New("尚未配置可用的 LLM：请在 Web 控制台「系统 → LLM」添加并启用一个配置（终端 HARNESS 只从 artex.db 读取配置，不再读取环境变量）")
	errCLINoTarget = errors.New("缺少目标：请用 -t/--target 指定授权目标，例如 artex cli -t https://example.com")
	errCLINotCoord = errors.New("终端 HARNESS 需要 coord 编排模式（ARTEX_SCHED=coord）；当前是 legacy 模式，请去掉 ARTEX_SCHED=legacy")
)

// RunCLI 在进程内跑一个 coord 任务并把活动流写到终端。
//
// 阻塞到任务终态、用户退出或 ctx 取消。ctx 取消会中止 agent 回合；返回的
// CLIResult 仍带上任务 id 与最近状态，便于调用方留痕。
func (s *Server) RunCLI(ctx context.Context, opts CLIOptions) (CLIResult, error) {
	if s == nil || s.m == nil || s.engine == nil {
		return CLIResult{}, errors.New("终端 HARNESS 未装配")
	}
	in := opts.In
	if in == nil {
		in = os.Stdin
	}
	out := opts.Out
	if out == nil {
		out = os.Stdout
	}
	pr := &cliPrinter{w: out, color: opts.Color, verbose: opts.Verbose}

	// LLM 只认 Web 控制台写入的激活配置（loadLLMConfig 直读 artex.db）。
	// 这里不看 s.llmOn：那个标志可能来自 env 兜底，而终端 HARNESS 已禁用
	// 环境变量配置——没有 DB profile 就明确拒绝，把用户引到 Web 控制台。
	if _, ok := s.loadLLMConfig(); !ok {
		return CLIResult{}, errCLINoLLM
	}
	if !agent.CoordEnabled() {
		return CLIResult{}, errCLINotCoord
	}

	targets := normalizeCLITargets(opts.Targets)
	var scanner *bufio.Scanner
	if opts.Interactive {
		scanner = bufio.NewScanner(in)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	}

	// 目标与要求都可以从「环境变量/旗标」直达；缺省时才交互补问，
	// 保证绿色启动（ARTEX_TARGET=... artex cli）完全不打断。
	if len(targets) == 0 {
		if !opts.Interactive {
			return CLIResult{}, errCLINoTarget
		}
		pr.raw("目标（URL / 域名 / IP，多个用空格或逗号分隔）: ")
		if !scanner.Scan() {
			return CLIResult{}, errCLINoTarget
		}
		targets = normalizeCLITargets([]string{scanner.Text()})
		if len(targets) == 0 {
			return CLIResult{}, errCLINoTarget
		}
	}
	if opts.Interactive && strings.TrimSpace(opts.Instruction) == "" {
		pr.raw("补充要求（可直接回车跳过）: ")
		if scanner.Scan() {
			opts.Instruction = strings.TrimSpace(scanner.Text())
		}
	}

	t, err := s.cliCreateTask(targets, opts)
	if err != nil {
		return CLIResult{}, err
	}

	pr.line("")
	pr.line("%s  任务 #%s", pr.bold("ARTEX HARNESS"), t.ID)
	pr.line("目标  %s", strings.Join(targets, ", "))
	pr.line("模式  根 agent 编排（%s）", coordModeLabel())
	pr.line("配置  LLM 来自 Web 控制台（%s）· Web 侧修改后下一回合自动生效", s.cliActiveModelLabel())
	if opts.Interactive {
		pr.line("提示  直接输入文字 = 给编排者下指令；/help 查看命令；/exit 退出（会中止本进程内的任务）")
	} else {
		pr.line("模式  非交互：跑完自动汇总")
	}
	pr.line("")

	// 先订阅再启动：目标分解、根 agent 第一轮与首个工具调用都必须被看见，
	// 否则用户看到的是「已经跑了一会儿」的半截流。
	acts, unsubscribe := s.engine.Broadcaster().Subscribe(t.ID)
	defer unsubscribe()

	s.launchTask(t, t.Description+" "+t.Goal, false)

	var inputCh chan string
	if opts.Interactive {
		inputCh = make(chan string, 8)
		go cliReadLines(scanner, inputCh, pr)
	}

	lastStatus := ""
	lastSig := s.cliActiveProfileSignature()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			status := s.resolvedTaskStatus(t)
			pr.line("")
			pr.line("%s  已中断（状态 %s）", pr.yellow("!"), status)
			return CLIResult{TaskID: t.ID, Status: status, Findings: s.cliFindings(t)}, ctx.Err()

		case a, ok := <-acts:
			if !ok {
				acts = nil
				continue
			}
			pr.activity(a)

		case line, ok := <-inputCh:
			if !ok {
				inputCh = nil
				continue
			}
			if s.cliHandleLine(t, line, pr) {
				res := CLIResult{TaskID: t.ID, Status: s.resolvedTaskStatus(t), Findings: s.cliFindings(t)}
				pr.printSummary(res)
				return res, nil
			}

		case <-ticker.C:
			status := s.resolvedTaskStatus(t)
			if status != lastStatus {
				lastStatus = status
				pr.line("%s  状态 → %s", pr.dim("·"), status)
			}
			// Web 控制台配置热同步：激活配置的签名变了就重建 provider，
			// 在跑的回合继续用旧配置，下一个回合自动切换，无需重启。
			if sig := s.cliActiveProfileSignature(); sig != lastSig {
				lastSig = sig
				s.reapplyActiveProfile()
				if m := s.cliActiveModelLabel(); m != "" {
					pr.line("%s  检测到 Web 控制台配置变更：模型 → %s（下一回合生效）", pr.cyan("◆"), m)
				}
			}
			if isTerminalStatus(status) {
				// 终态先落库、收尾摘要后 emit：给活动流一个短暂的排空窗口，
				// 别让最后一条「任务收尾」留到进程退出之后。
				drainCLIActivities(acts, pr, 300*time.Millisecond)
				res := CLIResult{TaskID: t.ID, Status: status, Findings: s.cliFindings(t)}
				pr.printSummary(res)
				return res, nil
			}
		}
	}
}

// drainCLIActivities 在任务终态后继续读一小段活动，把收尾摘要排空。
func drainCLIActivities(acts <-chan db.Activity, pr *cliPrinter, wait time.Duration) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case a, ok := <-acts:
			if !ok {
				return
			}
			pr.activity(a)
		case <-timer.C:
			return
		}
	}
}

// cliReadLines 把行式输入送进事件循环。EOF 关闭通道，让主循环继续等任务收尾。
func cliReadLines(scanner *bufio.Scanner, ch chan<- string, pr *cliPrinter) {
	defer close(ch)
	for {
		pr.raw("you> ")
		if !scanner.Scan() {
			pr.line("")
			return
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		ch <- line
	}
}

// cliHandleLine 处理一行人类输入。返回 true 表示退出循环。
func (s *Server) cliHandleLine(t *Task, line string, pr *cliPrinter) bool {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "/help", "/h", "help", "?":
		pr.line("命令：")
		pr.line("  /status     任务状态与漏洞数")
		pr.line("  /findings   列出本任务已记录漏洞")
		pr.line("  /config     显示 Web 控制台配置入口")
		pr.line("  /pause      暂停任务并退出（可在控制台恢复）")
		pr.line("  /verbose    切换 thinking 流显示")
		pr.line("  /exit       退出（本进程内的任务会被中止）")
		pr.line("  其它任何文字 = 作为 instruction 发给根 agent")
		return false
	case "/status":
		pr.line("任务 #%s · 状态 %s · 漏洞 %d 条", t.ID, s.resolvedTaskStatus(t), len(s.cliFindings(t)))
		return false
	case "/config":
		pr.line("配置入口：Web 控制台 http://%s", config.DefaultListenAddr)
		pr.line("  LLM / Agent / Skill / 预算 / 通知均在 Web 控制台配置；")
		pr.line("  配置写入 artex.db，本会话下一回合自动生效，无需重启。")
		return false
	case "/findings":
		printCLIFindings(pr, s.cliFindings(t))
		return false
	case "/pause", "/stop":
		if _, err := s.applyTaskControl(t, "pause"); err != nil {
			pr.line("%s  暂停失败：%v", pr.red("!"), err)
			return false
		}
		pr.line("%s  任务已暂停。可退出后在控制台任务 #%s 恢复。", pr.green("✓"), t.ID)
		return true
	case "/verbose":
		pr.verbose = !pr.verbose
		pr.line("thinking 流显示：%v", pr.verbose)
		return false
	case "/exit", "/quit", "exit", "quit":
		pr.line("退出。任务 #%s 若仍在运行，会随本进程结束而中止；需要保留进度请用 /pause。", t.ID)
		return true
	}
	if strings.HasPrefix(line, "/") {
		pr.line("%s  未知命令 %s；/help 查看可用命令。", pr.yellow("!"), line)
		return false
	}
	if isTerminalStatus(s.resolvedTaskStatus(t)) {
		pr.line("%s  任务已收尾，无法再接收指令；请查看上面的最终汇总，或下次新建任务。", pr.yellow("!"))
		return false
	}
	if err := s.cliSendToRoot(t.ID, line); err != nil {
		pr.line("%s  发送失败：%v", pr.red("!"), err)
		return false
	}
	pr.line("%s  已发送给编排者。", pr.dim("·"))
	return false
}

// cliSendToRoot 把人类文字作为 instruction 投给根 agent。
//
// 用 instruction 而不是 information：人在终端里说话是在改变做法，不是补充背景。
// 与 server.chat 的 coord 分支保持同一语义，避免 Web/CLI 两套行为。
func (s *Server) cliSendToRoot(taskID, text string) error {
	if !agent.CoordEnabled() {
		return errors.New("当前不是 coord 编排模式，终端 HARNESS 需要 ARTEX_SCHED=coord")
	}
	ct, ok := coordGet(s.engine, taskID)
	if !ok || ct == nil || ct.run == nil || ct.run.Coord == nil || ct.run.RootID == "" {
		return errors.New("编排者已收尾，无法再接收指令")
	}
	_, err := ct.run.Coord.Send("human", ct.run.RootID, agent.MsgInstruction, text)
	return err
}

// cliCreateTask 建一个与 Web「新建任务」同构的任务。
//
// 不直接写库：走 Manager.CreateTaskWithOptions 与 launchTask，保证种子目标、
// 目标分解、并发准入、LLM 链解析与 Web 入口没有第二套实现。
func (s *Server) cliCreateTask(targets []string, opts CLIOptions) (*Task, error) {
	desc := strings.TrimSpace(opts.Name)
	if desc == "" {
		desc = "CLI · " + targets[0]
		if len(targets) > 1 {
			desc = fmt.Sprintf("CLI · %s 等 %d 个目标", targets[0], len(targets))
		}
	}
	goal := "目标：\n- " + strings.Join(targets, "\n- ")
	if ins := strings.TrimSpace(opts.Instruction); ins != "" {
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
		return nil, err
	}
	log.Printf("[cli] task #%s created (targets=%s)", t.ID, strings.Join(targets, ","))
	return t, nil
}

// cliFindings 读取本任务的漏洞摘要（最多 200 条，按严重度排序）。
func (s *Server) cliFindings(t *Task) []CLIFinding {
	if s == nil || s.m == nil || s.m.pg == nil || t == nil {
		return nil
	}
	fs, _, err := s.m.pg.ListFindingsPage(db.FindingFilter{TaskID: t.ID, Sort: "severity"}, 1, 200)
	if err != nil {
		log.Printf("[cli] task #%s list findings: %v", t.ID, err)
		return nil
	}
	out := make([]CLIFinding, 0, len(fs))
	for _, f := range fs {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			name = f.VulnClass
		}
		out = append(out, CLIFinding{
			ID: f.ID, Severity: strings.ToLower(strings.TrimSpace(f.Severity)),
			Name: name, VulnClass: f.VulnClass, Status: f.Status, Summary: f.Summary,
		})
	}
	// ListFindingsPage 已按 severity 排序；这里不再二次排序，保持查询口径。
	return out
}

// cliActiveProfileSignature 汇总 Web 控制台激活 LLM 配置的关键字段，供 CLI
// 每秒比对一次以检测「Web 侧改了配置」。签名只包含影响模型行为的字段；
// CLI 自身不提供任何修改配置的命令，变更只可能来自 Web UI 写库。
func (s *Server) cliActiveProfileSignature() string {
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

// cliActiveModelLabel 返回「模型（配置名）」形式的可读标签，用于横幅与变更提示。
func (s *Server) cliActiveModelLabel() string {
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

func coordModeLabel() string {
	if agent.CoordEnabled() {
		return "coord"
	}
	return "legacy"
}

func normalizeCLITargets(raw []string) []string {
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

func printCLIFindings(pr *cliPrinter, findings []CLIFinding) {
	if len(findings) == 0 {
		pr.line("（本任务暂未记录到漏洞）")
		return
	}
	for _, f := range findings {
		sev := strings.ToUpper(f.Severity)
		if sev == "" {
			sev = "?"
		}
		line := fmt.Sprintf("  [%s] %s", sev, f.Name)
		if f.Status != "" {
			line += "  (" + f.Status + ")"
		}
		pr.line("%s", line)
	}
}

// HasFindings 供 cmd 决定退出码（Strix 口径：有漏洞 → 2）。
func (r CLIResult) HasFindings() bool { return len(r.Findings) > 0 }

func (pr *cliPrinter) printSummary(res CLIResult) {
	pr.line("")
	pr.line("%s", pr.dim("──────────────── 任务结束 ────────────────"))
	pr.line("任务  #%s", res.TaskID)
	pr.line("状态  %s", res.Status)
	if len(res.Findings) == 0 {
		pr.line("漏洞  未记录到漏洞")
	} else {
		counts := map[string]int{}
		for _, f := range res.Findings {
			counts[f.Severity]++
		}
		parts := make([]string, 0, 5)
		known := 0
		for _, sev := range []string{"critical", "high", "medium", "low"} {
			if counts[sev] > 0 {
				known += counts[sev]
				parts = append(parts, fmt.Sprintf("%s %d", sev, counts[sev]))
			}
		}
		if rest := len(res.Findings) - known; rest > 0 {
			parts = append(parts, fmt.Sprintf("其它 %d", rest))
		}
		pr.line("漏洞  %d 条（%s）", len(res.Findings), strings.Join(parts, " / "))
		printCLIFindings(pr, res.Findings)
	}
	pr.line("")
	pr.line("完整转录、证据与报告在控制台任务 #%s 查看；本次未启动 Web 服务、未占用端口。", res.TaskID)
}

// cliPrinter 是并发安全的行输出器：活动流与用户命令来自不同 goroutine。
type cliPrinter struct {
	mu      sync.Mutex
	w       io.Writer
	color   bool
	verbose bool
}

func (p *cliPrinter) line(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.w, format+"\n", args...)
}

func (p *cliPrinter) raw(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.w, format, args...)
}

func (p *cliPrinter) activity(a db.Activity) {
	worker := a.Worker
	if worker == "" {
		worker = "system"
	}
	label := p.dim("[" + worker + "]")
	switch a.Kind {
	case "thinking":
		if !p.verbose {
			return
		}
		p.line("%s %s", label, p.dim(a.Summary))
	case "text":
		if strings.TrimSpace(a.Summary) == "" {
			return
		}
		p.line("%s %s", label, a.Summary)
	case "tool_use":
		p.line("%s %s %s", label, p.cyan("→"), a.Summary)
	case "tool_result":
		mark := p.dim("←")
		if a.IsError {
			mark = p.red("←")
		}
		p.line("%s %s %s", label, mark, a.Summary)
	case "result":
		// finish_task / submit_report 会取消该 agent 的 run，capture 因而补一条
		// 「（运行被中断…未完成）」。任务实际已按交卷收尾，这条在终端里只会误导；
		// Web 侧仍原样保留，CLI 不重复展示。
		if strings.HasPrefix(strings.TrimSpace(a.Summary), "（运行被中断") {
			return
		}
		mark := p.green("✓")
		if a.IsError {
			mark = p.red("✗")
		}
		p.line("%s %s %s", label, mark, a.Summary)
	case "round":
		p.line("%s %s", label, p.dim(a.Summary))
	case "usage":
		return
	default:
		if strings.TrimSpace(a.Summary) != "" {
			p.line("%s %s", label, a.Summary)
		}
	}
}

func (p *cliPrinter) paint(code, s string) string {
	if !p.color || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (p *cliPrinter) bold(s string) string   { return p.paint("1", s) }
func (p *cliPrinter) dim(s string) string    { return p.paint("2", s) }
func (p *cliPrinter) cyan(s string) string   { return p.paint("36", s) }
func (p *cliPrinter) green(s string) string  { return p.paint("32", s) }
func (p *cliPrinter) yellow(s string) string { return p.paint("33", s) }
func (p *cliPrinter) red(s string) string    { return p.paint("31", s) }
