package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// fakeBackend 是测试用的假后端。
//
// TUI 与后端之间只有 TUIBackend 这一个接缝，所以界面逻辑可以完全脱离
// SQLite、agent 与网络来测：喂活动、收指令、断言渲染。
type fakeBackend struct {
	created    [][]string
	sent       []string
	activities chan Activity
	createdID  string
	status     TaskState
	findings   []Finding
	model      string

	lastInstruction string
	lastOpts        TaskOptions
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		activities: make(chan Activity, 16),
		createdID:  "42",
		status:     TaskState{ID: "42", Status: "running"},
	}
}

func (f *fakeBackend) CreateTask(ctx context.Context, targets []string, instruction string, opts TaskOptions) (string, error) {
	f.created = append(f.created, targets)
	f.lastInstruction = instruction
	f.lastOpts = opts
	return f.createdID, nil
}

func (f *fakeBackend) Subscribe(ctx context.Context, taskID string) (<-chan Activity, func()) {
	return f.activities, func() {}
}

func (f *fakeBackend) Send(ctx context.Context, taskID, text string) error {
	f.sent = append(f.sent, text)
	return nil
}

func (f *fakeBackend) TaskStatus(ctx context.Context, taskID string) (TaskState, error) {
	return f.status, nil
}

func (f *fakeBackend) Findings(ctx context.Context, taskID string) ([]Finding, error) {
	return f.findings, nil
}

func (f *fakeBackend) Control(ctx context.Context, taskID, action string) error {
	return nil
}

func (f *fakeBackend) ModelLabel(ctx context.Context) string { return f.model }

func (f *fakeBackend) ActiveModelChanged(ctx context.Context) (bool, string) {
	return false, ""
}

// newTestModel 造一个已布局完成的模型（跳过 Bubble Tea 的真实终端）。
func newTestModel(t *testing.T, backend TUIBackend) *Model {
	t.Helper()
	m := NewWithOptions(context.Background(), backend, Options{})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m
}

func TestSplitTargets(t *testing.T) {
	got := splitTargets("https://a.example, b.example;10.0.0.1\n  b.example")
	want := []string{"https://a.example", "b.example", "10.0.0.1"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestSeverityCountsTotal(t *testing.T) {
	c := SeverityCounts{Critical: 1, High: 2, Medium: 3, Low: 4}
	if c.Total() != 10 {
		t.Fatalf("got %d, want 10", c.Total())
	}
}

func TestViewRendersEmptyState(t *testing.T) {
	m := newTestModel(t, newFakeBackend())
	out := m.View()
	if !strings.Contains(out, "ARTEX-HYDRA") {
		t.Fatalf("缺少品牌名：%q", out)
	}
	if !strings.Contains(out, "活动流") {
		t.Fatalf("缺少活动流面板：%q", out)
	}
	if !strings.Contains(out, "等待活动") {
		t.Fatalf("空状态应提示等待活动：%q", out)
	}
}

func TestActivityLineRendersKinds(t *testing.T) {
	m := newTestModel(t, newFakeBackend())
	cases := []struct {
		kind    string
		summary string
		want    string
	}{
		{"tool_use", "nmap -sV target", "→"},
		{"tool_result", "open 443", "←"},
		{"text", "正在分析", "正在分析"},
	}
	for _, c := range cases {
		line := m.activityLine(Activity{Worker: "root", Kind: c.kind, Summary: c.summary})
		if !strings.Contains(line, c.want) {
			t.Fatalf("%s 未渲染 %q：%q", c.kind, c.want, line)
		}
	}
}

func TestActivityLineHidesThinkingUnlessVerbose(t *testing.T) {
	m := newTestModel(t, newFakeBackend())
	line := m.activityLine(Activity{Worker: "root", Kind: "thinking", Summary: "内部推理"})
	if line != "" {
		t.Fatalf("默认不应渲染 thinking：%q", line)
	}
	m.verbose = true
	line = m.activityLine(Activity{Worker: "root", Kind: "thinking", Summary: "内部推理"})
	if !strings.Contains(line, "内部推理") {
		t.Fatalf("verbose 应渲染 thinking：%q", line)
	}
}

func TestActivityLineDropsAbortNoise(t *testing.T) {
	m := newTestModel(t, newFakeBackend())
	line := m.activityLine(Activity{Worker: "root", Kind: "result", Summary: "（运行被中断：取消方未附加具名原因）"})
	if line != "" {
		t.Fatalf("交卷产生的 abort 结果不应渲染：%q", line)
	}
}

func TestLiveFrameReplacesSameWorkerKind(t *testing.T) {
	m := newTestModel(t, newFakeBackend())
	m.applyLive(Activity{Worker: "root", Kind: "text", Summary: "第一段"})
	m.applyLive(Activity{Worker: "root", Kind: "text", Summary: "第一段第二句"})
	if len(m.liveOrder) != 1 {
		t.Fatalf("同 worker 同类实时帧应只保留一条：%v", m.liveOrder)
	}
	if got := m.live["root\x00text"].Summary; got != "第一段第二句" {
		t.Fatalf("实时帧应取最新全文：%q", got)
	}
}

func TestAppendActivityClearsLiveSlot(t *testing.T) {
	m := newTestModel(t, newFakeBackend())
	m.applyLive(Activity{Worker: "root", Kind: "text", Summary: "实时内容"})
	m.appendActivity(Activity{Worker: "root", Kind: "text", Summary: "落库内容"})
	if len(m.live) != 0 {
		t.Fatalf("落库活动到达后实时占位应清空：%v", m.live)
	}
	if len(m.liveOrder) != 0 {
		t.Fatalf("liveOrder 应同步清理：%v", m.liveOrder)
	}
}

func TestEnterSendsInstructionToRootAgent(t *testing.T) {
	backend := newFakeBackend()
	m := newTestModel(t, backend)
	m.taskID = "42"
	m.input.SetValue("重点测试登录与越权")

	cmd := m.submitInput()
	if cmd == nil {
		t.Fatal("应有命令产出")
	}
	// tea.Cmd 是惰性的：不执行就什么都不会发生。
	cmd()
	if len(backend.sent) != 1 || backend.sent[0] != "重点测试登录与越权" {
		t.Fatalf("指令未投给编排者：%v", backend.sent)
	}
	if m.input.Value() != "" {
		t.Fatalf("发送后输入框应清空：%q", m.input.Value())
	}
}

func TestSlashCommandDoesNotReachRootAgent(t *testing.T) {
	backend := newFakeBackend()
	m := newTestModel(t, backend)
	m.taskID = "42"
	m.input.SetValue("/status")

	m.submitInput()
	if len(backend.sent) != 0 {
		t.Fatalf("斜杠命令不应投给编排者：%v", backend.sent)
	}
}

func TestTargetModeEnterCreatesTask(t *testing.T) {
	backend := newFakeBackend()
	m := newTestModel(t, backend)
	m.mode = modeTarget
	m.input.SetValue("https://example.com")

	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("应产出建任务命令")
	}
	if m.mode != modeInput {
		t.Fatalf("建任务后应回到指令模式：%v", m.mode)
	}
	_ = updated
	// 执行命令才会真正调后端。
	msg := cmd()
	if _, ok := msg.(taskCreatedMsg); !ok {
		t.Fatalf("应得到 taskCreatedMsg，得到 %T", msg)
	}
	if len(backend.created) != 1 || backend.created[0][0] != "https://example.com" {
		t.Fatalf("目标未传给后端：%v", backend.created)
	}
}

func TestCtrlCRequiresSecondPress(t *testing.T) {
	m := newTestModel(t, newFakeBackend())
	m.taskID = "42"

	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatal("第一次 Ctrl-C 不应退出")
	}
	if m.quit {
		t.Fatal("第一次 Ctrl-C 不应标记退出")
	}
	if !strings.Contains(m.status, "再按一次") {
		t.Fatalf("应提示再按一次：%q", m.status)
	}

	_, cmd = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("第二次 Ctrl-C 应退出")
	}
	if !m.quit {
		t.Fatal("第二次 Ctrl-C 应标记退出")
	}
}

func TestHelpOverlayToggles(t *testing.T) {
	m := newTestModel(t, newFakeBackend())
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if !m.showHelp {
		t.Fatal("? 应打开帮助")
	}
	out := m.View()
	if !strings.Contains(out, "命令与按键") {
		t.Fatalf("帮助页内容缺失：%q", out)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.showHelp {
		t.Fatal("任意键应关闭帮助")
	}
}

func TestStartupOptionsApplyOnlyToFirstTask(t *testing.T) {
	backend := newFakeBackend()
	m := NewWithOptions(context.Background(), backend, Options{
		Instruction: "重点测试登录",
		Name:        "首个任务",
		Timeout:     600,
	})
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	// 首个任务带上启动参数。
	m.createTaskCmd([]string{"https://a.example"})()
	if backend.lastInstruction != "重点测试登录" {
		t.Fatalf("首个任务应带 -i：%q", backend.lastInstruction)
	}
	if backend.lastOpts.Name != "首个任务" || backend.lastOpts.TimeoutSeconds != 600 {
		t.Fatalf("首个任务应带 --name/--timeout：%+v", backend.lastOpts)
	}

	// 第二个任务（模拟 Ctrl-N）不应继承上次的要求。
	m.createTaskCmd([]string{"https://b.example"})()
	if backend.lastInstruction != "" {
		t.Fatalf("后续任务不应继承 -i：%q", backend.lastInstruction)
	}
	if backend.lastOpts.Name != "" || backend.lastOpts.TimeoutSeconds != 0 {
		t.Fatalf("后续任务不应继承 --name/--timeout：%+v", backend.lastOpts)
	}
}

func TestQuestionMarkIsTypableWhenInputNotEmpty(t *testing.T) {
	m := newTestModel(t, newFakeBackend())
	m.taskID = "42"
	// 先输入一段文字，再打问号：这是"这个接口有没有越权?"这种自然问法。
	for _, r := range "这个接口安全" {
		m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if m.showHelp {
		t.Fatal("输入框有内容时 ? 不应打开帮助")
	}
	if !strings.Contains(m.input.Value(), "?") {
		t.Fatalf("? 应作为文字进入输入框：%q", m.input.Value())
	}
}

func TestPaneSwitchKeys(t *testing.T) {
	m := newTestModel(t, newFakeBackend())
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlF})
	if m.activePane != panelFindings {
		t.Fatalf("Ctrl-F 应切到漏洞面板：%v", m.activePane)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlT})
	if m.activePane != panelTasks {
		t.Fatalf("Ctrl-T 应切到任务面板：%v", m.activePane)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	if m.activePane != panelStream {
		t.Fatalf("Ctrl-S 应切回活动流：%v", m.activePane)
	}
}

func TestFindingsPaneShowsSeverity(t *testing.T) {
	backend := newFakeBackend()
	backend.findings = []Finding{
		{ID: 1, Severity: "critical", Name: "SQL 注入"},
		{ID: 2, Severity: "low", Name: "缺失安全头"},
	}
	m := newTestModel(t, backend)
	m.taskID = "42"
	m.activePane = panelFindings
	// 漏洞数据由后端推送：命令产出消息，消息经 Update 才写进模型——走真实路径。
	m.Update(m.refreshFindingsCmd()())
	out := m.View()
	if !strings.Contains(out, "SQL 注入") {
		t.Fatalf("漏洞面板未显示漏洞名：%q", out)
	}
	if !strings.Contains(out, "CRITICAL") {
		t.Fatalf("漏洞面板未显示严重度：%q", out)
	}
}

func TestTruncateKeepsWidth(t *testing.T) {
	got := truncate("一个非常非常长的漏洞名称", 6)
	if len([]rune(got)) > 6 {
		t.Fatalf("截断后仍超长：%q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("截断应带省略号：%q", got)
	}
}

func TestWrapLineBreaksLongText(t *testing.T) {
	parts := wrapLine("这是一段很长的中文摘要内容需要折行显示才不会溢出视口宽度", 20)
	if len(parts) < 2 {
		t.Fatalf("长文本应被折行：%v", parts)
	}
	for _, p := range parts {
		if lipgloss.Width(p) > 20 {
			t.Fatalf("折行后仍超宽（%d）：%q", lipgloss.Width(p), p)
		}
	}
}

func TestWrapLineKeepsShortTextIntact(t *testing.T) {
	parts := wrapLine("短句", 40)
	if len(parts) != 1 || parts[0] != "短句" {
		t.Fatalf("短文本不应被折行：%v", parts)
	}
}

func TestWrapActivityLineIndentsContinuation(t *testing.T) {
	m := newTestModel(t, newFakeBackend())
	line := m.activityLine(Activity{
		Worker:  "root",
		Kind:    "text",
		Summary: "这是一段很长的中文摘要内容需要折行显示才不会溢出视口宽度",
	})
	parts := m.wrapActivityLine(line, 30)
	if len(parts) < 2 {
		t.Fatalf("长活动行应被折行：%v", parts)
	}
	if !strings.HasPrefix(parts[0], "[root]") {
		t.Fatalf("首行应保留 worker 前缀：%q", parts[0])
	}
	for _, p := range parts[1:] {
		if strings.Contains(p, "[root]") {
			t.Fatalf("续行不应重复 worker 前缀：%q", p)
		}
	}
}

func TestNarrowTerminalHidesSidebar(t *testing.T) {
	m := NewWithOptions(context.Background(), newFakeBackend(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 20})
	out := m.View()
	// 窄终端下不该出现侧栏边框。
	if strings.Contains(out, "╭") {
		t.Fatalf("窄终端不应渲染侧栏面板：%q", out)
	}
}

func TestNarrowTerminalHeaderFitsWidth(t *testing.T) {
	backend := newFakeBackend()
	backend.model = "gpt-4o-mini · tui-test"
	m := NewWithOptions(context.Background(), backend, Options{})
	m.taskID = "42"
	m.taskState = TaskState{ID: "42", Status: "running", InFlight: 3}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 20})
	header := m.headerView()
	if lipgloss.Width(header) > 40 {
		t.Fatalf("窄屏头部超宽（%d）：%q", lipgloss.Width(header), header)
	}
	// 品牌名与任务号必须保留：那是用户确认"在看哪个任务"的依据。
	if !strings.Contains(header, "ARTEX-HYDRA") || !strings.Contains(header, "任务 #42") {
		t.Fatalf("窄屏头部应保留品牌与任务号：%q", header)
	}
}

func TestNarrowTerminalInputIsSingleLine(t *testing.T) {
	m := NewWithOptions(context.Background(), newFakeBackend(), Options{})
	m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	line := m.inputView()
	if strings.Count(line, "\n") != 0 {
		t.Fatalf("窄屏输入区应保持单行：%q", line)
	}
}
