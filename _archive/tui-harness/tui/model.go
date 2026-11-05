package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// 面板标识。TUI 用「活动流 + 侧栏」两栏布局：
// 活动流占主区（agent 在干什么），侧栏放漏洞与任务概览（结果是什么）。
type panel int

const (
	panelStream panel = iota
	panelFindings
	panelTasks
)

// 模式：TUI 有两种互斥的输入语义。
//
// 输入框在最下方，它的含义由 mode 决定：
//   - modeInput：文字是给根 agent 的指令（与 Web 控制台同语义）；
//   - modeTarget：文字是授权目标，用来开一个新任务。
//
// 分成两态而不是让用户记斜杠命令，是因为"开新任务"和"给编排者下指令"
// 是两种截然不同的意图，混在一个输入框里必然误操作。
type mode int

const (
	modeInput mode = iota
	modeTarget
)

// statusKind 决定状态栏提示的配色。
type statusKind int

const (
	statusInfo statusKind = iota
	statusOK
	statusWarn
	statusErr
)

// Model 是 TUI 的根模型，实现 tea.Model。
type Model struct {
	backend TUIBackend
	ctx     context.Context

	// 布局尺寸，由 tea.WindowSizeMsg 驱动。
	width  int
	height int

	// 运行态。
	taskID         string
	taskState      TaskState
	mode           mode
	pendingTargets []string
	opts           Options
	// optsConsumed 标记启动参数（-i / --name / --timeout）已被首个任务消费。
	// 它们是"本次任务"的参数，Ctrl-N 新建的任务不该继承。
	optsConsumed bool
	finished     bool
	quit         bool

	// 活动流：落库活动与实时增量分开存，渲染时合并。
	// 实时帧是"截至目前的累计全文"，与落库活动会重复，
	// 所以同一 worker 的同类实时内容只保留最后一条，落库活动到达时清掉。
	activities []Activity
	live       map[string]Activity
	liveOrder  []string

	// 侧栏数据。
	findings   []Finding
	taskList   []TaskSummary
	activePane panel

	// 输入组件。
	input   textinput.Model
	spinner spinner.Model
	stream  viewport.Model

	// 状态栏与提示。
	status       string
	statusKind   statusKind
	verbose      bool
	showHelp     bool
	ctrlCPressed bool
}

// TaskSummary 是任务列表里的一行摘要。
type TaskSummary struct {
	// ID 是任务 id。
	ID string
	// Name 是任务名。
	Name string
	// Status 是生命周期状态。
	Status string
	// Findings 是漏洞数合计。
	Findings int
	// UpdatedAt 是最后活动时间。
	UpdatedAt time.Time
	// Current 标记是否是当前会话的任务。
	Current bool
}

// New 构造一个 TUI 模型。
//
// targets 非空表示"直接开跑"：启动即建任务并进入活动流；
// 为空则停在目标输入态，等用户输入授权目标。
func New(ctx context.Context, backend TUIBackend, targets []string) *Model {
	return NewWithOptions(ctx, backend, Options{Targets: targets})
}

// NewWithOptions 构造一个带完整启动参数的 TUI 模型。
func NewWithOptions(ctx context.Context, backend TUIBackend, opts Options) *Model {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "输入指令回车发送；Ctrl-N 开新任务；? 看帮助"
	in.CharLimit = 0
	in.Width = 0

	m := &Model{
		backend:        backend,
		ctx:            ctx,
		mode:           modeInput,
		input:          in,
		spinner:        spinner.New(spinner.WithSpinner(spinner.Dot)),
		live:           map[string]Activity{},
		pendingTargets: opts.Targets,
		opts:           opts,
		stream:         viewport.New(0, 0),
	}
	m.input.Focus()
	if len(opts.Targets) > 0 {
		m.status = "正在创建任务…"
		m.statusKind = statusInfo
	} else {
		m.status = "输入授权目标开始；或按 ? 查看命令"
		m.statusKind = statusInfo
	}
	return m
}

// Init 是 tea.Model 的初始化。
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick}
	if len(m.pendingTargets) > 0 {
		cmds = append(cmds, m.createTaskCmd(m.pendingTargets))
	}
	return tea.Batch(cmds...)
}

// Update 是 tea.Model 的事件循环。
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case taskCreatedMsg:
		m.taskID = msg.id
		m.setStatus("任务 #"+msg.id+" 已启动", statusOK)
		return m, tea.Batch(m.subscribeCmd(), m.pollStatusCmd(), m.refreshFindingsCmd(), m.refreshTasksCmd())

	case activityMsg:
		m.appendActivity(msg.activity)
		return m, m.subscribeCmd()

	case liveMsg:
		m.applyLive(msg.activity)
		return m, nil

	case statusMsg:
		m.applyStatus(msg)
		return m, nil

	case statusTickMsg:
		if m.finished {
			return m, nil
		}
		// 状态与漏洞一起刷：漏洞是任务跑出来的产物，
		// 只在按 Ctrl-F 时才刷新会让侧栏一直显示"暂无"，那等于没有这个面板。
		return m, tea.Batch(m.refreshStatusCmd(), m.refreshFindingsCmd(), m.pollStatusCmd())

	case findingsMsg:
		m.findings = msg.findings
		return m, nil

	case tasksMsg:
		m.taskList = msg.tasks
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case errMsg:
		m.setStatus(msg.err.Error(), statusErr)
		return m, nil
	}
	return m, nil
}

// handleKey 处理键盘输入。
func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl-C 两段式：第一次提示，第二次才真退。
	// 全屏 TUI 里误按 Ctrl-C 就丢掉一个在跑的任务，代价比 CLI 高得多。
	if msg.Type == tea.KeyCtrlC {
		if !m.ctrlCPressed {
			m.ctrlCPressed = true
			m.setStatus("再按一次 Ctrl-C 退出（任务会被中止）", statusWarn)
			return m, nil
		}
		m.quit = true
		return m, tea.Quit
	}
	m.ctrlCPressed = false

	if m.showHelp {
		m.showHelp = false
		return m, nil
	}

	// 帮助与面板切换在输入框之外，按键不落到 textinput。
	//
	// ? 只在输入框为空时打开帮助：否则用户永远没法在指令里写问号，
	// 而"这个接口有没有越权?"是最自然的下指令方式。
	switch msg.String() {
	case "?":
		if strings.TrimSpace(m.input.Value()) == "" {
			m.showHelp = true
			return m, nil
		}
	case "ctrl+r":
		return m, tea.Batch(m.refreshFindingsCmd(), m.refreshTasksCmd())
	case "ctrl+f":
		m.activePane = panelFindings
		return m, m.refreshFindingsCmd()
	case "ctrl+t":
		m.activePane = panelTasks
		return m, m.refreshTasksCmd()
	case "ctrl+s":
		m.activePane = panelStream
		return m, nil
	case "ctrl+n":
		m.mode = modeTarget
		m.input.SetValue("")
		m.input.Placeholder = "输入授权目标（URL / 域名 / IP），多个用空格分隔"
		m.setStatus("新任务模式：输入目标后回车", statusInfo)
		return m, nil
	case "pgup", "pgdown", "up", "down":
		// 滚动活动流：这些键不进输入框（单行输入里上下键本就没有语义）。
		return m, m.scrollStream(msg.String())
	}

	// 目标模式下，Enter 建任务而不是发指令。
	if m.mode == modeTarget && msg.Type == tea.KeyEnter {
		targets := splitTargets(m.input.Value())
		if len(targets) == 0 {
			m.setStatus("请输入至少一个授权目标", statusWarn)
			return m, nil
		}
		m.input.Reset()
		m.input.Placeholder = "输入指令回车发送；Ctrl-N 开新任务；? 看帮助"
		m.mode = modeInput
		m.setStatus("正在创建任务…", statusInfo)
		return m, m.createTaskCmd(targets)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if msg.Type == tea.KeyEnter {
		return m, m.submitInput()
	}
	return m, cmd
}

// submitInput 处理回车：把输入框里的文字投给根 agent。
func (m *Model) submitInput() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	m.input.Reset()
	if strings.HasPrefix(text, "/") {
		return m.runCommand(text)
	}
	if m.taskID == "" {
		m.setStatus("还没有任务：请按 Ctrl-N 输入目标", statusWarn)
		return nil
	}
	m.setStatus("已发送给编排者…", statusInfo)
	return m.sendCmd(m.taskID, text)
}

// runCommand 处理斜杠命令。
//
// 命令集刻意保持小：只放"界面上做不到、只有命令能做"的动作
// （切面板、暂停、退出），其余都做成了按键。
func (m *Model) runCommand(text string) tea.Cmd {
	switch strings.ToLower(text) {
	case "/help", "/?":
		m.showHelp = true
		return nil
	case "/status":
		return m.refreshStatusCmd()
	case "/findings":
		m.activePane = panelFindings
		return m.refreshFindingsCmd()
	case "/tasks":
		m.activePane = panelTasks
		return m.refreshTasksCmd()
	case "/stream":
		m.activePane = panelStream
		return nil
	case "/pause":
		if m.taskID == "" {
			return nil
		}
		m.setStatus("已请求暂停…", statusInfo)
		return m.controlCmd(m.taskID, "pause")
	case "/resume":
		if m.taskID == "" {
			return nil
		}
		m.setStatus("已请求恢复…", statusInfo)
		return m.controlCmd(m.taskID, "resume")
	case "/verbose":
		m.verbose = !m.verbose
		m.setStatus(fmt.Sprintf("thinking 流显示：%v", m.verbose), statusInfo)
		return nil
	case "/exit", "/quit":
		m.quit = true
		return tea.Quit
	default:
		m.setStatus("未知命令："+text+"（/help 查看命令）", statusWarn)
		return nil
	}
}

// appendActivity 追加一条落库活动，并清掉同 worker 的实时占位。
func (m *Model) appendActivity(a Activity) {
	m.activities = append(m.activities, a)
	// 实时帧是累计全文，落库活动到达后由落库内容接管，避免同一段文字显示两遍。
	delete(m.live, liveKey(a))
	m.rebuildLiveOrder()
}

// applyLive 处理一条实时增量帧。
func (m *Model) applyLive(a Activity) {
	if a.Kind != "thinking" && a.Kind != "text" {
		return
	}
	key := liveKey(a)
	if _, ok := m.live[key]; !ok {
		m.liveOrder = append(m.liveOrder, key)
	}
	m.live[key] = a
}

// liveKey 是实时帧的聚合键：同一 worker 的同一类只保留最后一条。
func liveKey(a Activity) string {
	return a.Worker + "\x00" + a.Kind
}

// rebuildLiveOrder 清理 liveOrder 里已被删除的键。
func (m *Model) rebuildLiveOrder() {
	kept := m.liveOrder[:0]
	for _, k := range m.liveOrder {
		if _, ok := m.live[k]; ok {
			kept = append(kept, k)
		}
	}
	m.liveOrder = kept
}

// setStatus 设置状态栏提示。
func (m *Model) setStatus(text string, kind statusKind) {
	m.status = text
	m.statusKind = kind
}

// applyStatus 应用一次状态刷新结果。
func (m *Model) applyStatus(msg statusMsg) {
	if msg.err != nil {
		m.setStatus(msg.err.Error(), statusErr)
		return
	}
	if msg.state.ID == "" {
		return
	}
	m.taskState = msg.state
	// 终态后不再轮询：状态不会再变，继续查只是白烧 SQLite。
	if isTerminalStatus(msg.state.Status) {
		if !m.finished {
			m.finished = true
			m.setStatus("任务已收尾："+msg.state.Status+"（Ctrl-C 退出）", statusOK)
		}
		return
	}
}

// splitTargets 把用户输入拆成目标列表。
//
// 与 CLI 的切分规则保持一致（逗号/分号/空白都算分隔符）：
// 两处入口对"什么算一个目标"的理解必须相同，否则用户会得到两种结果。
func splitTargets(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// isTerminalStatus 判断任务是否进入终态。
func isTerminalStatus(status string) bool {
	switch status {
	case "done", "failed", "timeout":
		return true
	}
	return false
}

// resize 依据终端尺寸重排视口。
func (m *Model) resize() {
	bodyHeight := m.height - 4
	if bodyHeight < 3 {
		bodyHeight = 3
	}
	sideWidth := 0
	if m.width >= 78 {
		sideWidth = 34
	}
	m.stream.Width = m.width - sideWidth - 2
	m.stream.Height = bodyHeight
	if m.stream.Width < 10 {
		m.stream.Width = 10
	}
	m.renderStream()
}

// scrollStream 滚动活动流。
func (m *Model) scrollStream(key string) tea.Cmd {
	switch key {
	case "up":
		m.stream.LineUp(1)
	case "down":
		m.stream.LineDown(1)
	case "pgup":
		m.stream.ViewUp()
	case "pgdown":
		m.stream.ViewDown()
	}
	return nil
}
