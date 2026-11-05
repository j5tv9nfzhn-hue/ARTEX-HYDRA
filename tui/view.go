package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// 活动流与侧栏的渲染。
//
// 全部是纯函数式读取模型状态后拼字符串：渲染不改状态，
// 这样 Bubble Tea 的"只有 Update 能改状态"约定才真正成立。

// View 是 tea.Model 的渲染入口。
//
// 布局是「头部 + 主体 + 输入 + 状态栏」四段：
// 头部给任务与模型，主体分活动流与侧栏，底部是输入与提示。
// 侧栏在窄终端下自动隐藏——宁可少一栏，也不能让活动流被挤到读不了。
func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "正在初始化…"
	}
	if m.showHelp {
		return m.helpView()
	}

	bodyHeight := m.height - 4
	if bodyHeight < 3 {
		bodyHeight = 3
	}

	stream := m.streamView(bodyHeight)
	body := stream
	// 侧栏固定 32 列；终端太窄时直接省掉，保证活动流可读。
	if m.width >= 78 {
		body = lipgloss.JoinHorizontal(lipgloss.Top, stream, m.sideView(bodyHeight))
	}

	return strings.Join([]string{
		m.headerView(),
		body,
		m.inputView(),
		m.statusView(),
	}, "\n")
}

// headerView 渲染顶部状态栏。
//
// 宽度不够时逐项丢弃次要信息（子代理数、漏洞分档、模型名），
// 但保留品牌名与任务号——那是用户确认"我在看哪个任务"的唯一依据。
func (m *Model) headerView() string {
	brand := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("ARTEX-HYDRA")

	parts := []string{brand}
	if m.taskID != "" {
		state := m.taskState.Status
		if state == "" {
			state = "created"
		}
		parts = append(parts, lipgloss.NewStyle().Foreground(colMuted).Render("任务 #"+m.taskID))
		parts = append(parts, statusStyle(state).Render("● "+state))
		if c := m.taskState.Findings; c.Total() > 0 {
			parts = append(parts, severityCountsView(c))
		}
		if m.taskState.InFlight > 0 {
			parts = append(parts, lipgloss.NewStyle().Foreground(colInfo).
				Render(fmt.Sprintf("%d 个子代理在跑", m.taskState.InFlight)))
		}
	} else {
		parts = append(parts, lipgloss.NewStyle().Foreground(colMuted).Render("未开始"))
	}

	if model := m.backend.ModelLabel(m.ctx); model != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(colFaint).Render(model))
	}

	// 从右往左丢，直到装得下：先丢模型名（信息密度最低），再丢子代理数。
	for lipgloss.Width(strings.Join(parts, "  ")) > m.width && len(parts) > 1 {
		parts = parts[:len(parts)-1]
	}
	return styleHeader.Width(m.width).Render(strings.Join(parts, "  "))
}

// severityCountsView 渲染漏洞分档计数。
func severityCountsView(c SeverityCounts) string {
	items := []string{}
	if c.Critical > 0 {
		items = append(items, severityStyle("critical").Render(fmt.Sprintf("%d critical", c.Critical)))
	}
	if c.High > 0 {
		items = append(items, severityStyle("high").Render(fmt.Sprintf("%d high", c.High)))
	}
	if c.Medium > 0 {
		items = append(items, severityStyle("medium").Render(fmt.Sprintf("%d medium", c.Medium)))
	}
	if c.Low > 0 {
		items = append(items, severityStyle("low").Render(fmt.Sprintf("%d low", c.Low)))
	}
	return "漏洞 " + strings.Join(items, " · ")
}

// inputView 渲染底部输入区。
//
// 窄终端下不显示按键提示：提示挤在输入行后面会换行，把输入框撑成两行，
// 反而挤掉活动流的显示空间。按键提示在 ? 帮助页里始终有。
func (m *Model) inputView() string {
	label := stylePrompt.Render("› ")
	if m.mode == modeTarget {
		label = lipgloss.NewStyle().Bold(true).Foreground(colWarn).Render("目标 › ")
	}
	line := label + m.input.View()
	if m.width >= 96 {
		hint := lipgloss.NewStyle().Foreground(colFaint).
			Render("  Ctrl-N 新目标  Ctrl-F 漏洞  Ctrl-T 任务  Ctrl-R 刷新  ? 帮助  Ctrl-C 退出")
		line += hint
	}
	return styleInput.Width(m.width).Render(line)
}

// statusView 渲染状态栏。
func (m *Model) statusView() string {
	text := m.status
	if text == "" {
		if m.taskID == "" {
			text = "输入授权目标开始；或按 ? 查看命令"
		} else {
			text = "就绪。输入文字 = 给编排者下指令"
		}
	}
	var style lipgloss.Style
	switch m.statusKind {
	case statusOK:
		style = lipgloss.NewStyle().Foreground(colOK)
	case statusWarn:
		style = lipgloss.NewStyle().Foreground(colWarn)
	case statusErr:
		style = lipgloss.NewStyle().Foreground(colErr)
	default:
		style = lipgloss.NewStyle().Foreground(colMuted)
	}
	return styleStatusBar.Width(m.width).Render(style.Render(text))
}

// helpView 渲染帮助浮层。
//
// 首列宽度固定 18：按键名最长的 "PgUp / PgDn / ↑↓" 也就 16 列，
// 再宽就浪费，再窄会把 "/findings / /tasks / /stream" 挤成两行。
func (m *Model) helpView() string {
	rows := [][2]string{
		{"输入文字", "作为 instruction 投给根 agent（与 Web 控制台同语义）"},
		{"Ctrl-N", "切到目标模式，开一个新任务"},
		{"Ctrl-F / T / S", "切换侧栏：漏洞 / 任务 / 活动流"},
		{"Ctrl-R", "刷新漏洞与任务列表"},
		{"PgUp / PgDn / ↑↓", "滚动活动流"},
		{"/status", "刷新任务状态"},
		{"/findings", "切到漏洞面板并刷新"},
		{"/tasks", "切到任务面板并刷新"},
		{"/stream", "切回活动流"},
		{"/pause / /resume", "暂停 / 恢复任务"},
		{"/verbose", "切换 thinking 流显示"},
		{"/exit", "退出（会中止本进程内的任务）"},
		{"?", "关闭本页"},
		{"Ctrl-C", "退出（按两次确认）"},
	}
	var b strings.Builder
	b.WriteString(stylePanelTitle.Render("ARTEX-HYDRA · 命令与按键"))
	b.WriteString("\n\n")
	for _, r := range rows {
		b.WriteString(lipgloss.NewStyle().Foreground(colAccent).Width(18).Render(r[0]))
		b.WriteString(lipgloss.NewStyle().Foreground(colText).Render(r[1]))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(styleHelp.Render("配置（LLM / Agent / Skill / 预算）只在 Web 控制台做，与本界面共用同一个 artex.db。"))
	return stylePanel.Width(m.width - 2).Render(b.String())
}

// streamView 渲染主区（活动流）。
func (m *Model) streamView(height int) string {
	title := stylePanelTitle.Render("活动流")
	if m.finished {
		title = stylePanelTitle.Render("活动流 · 已收尾")
	}
	m.renderStream()
	return lipgloss.JoinVertical(lipgloss.Left, title, m.stream.View())
}

// sideView 渲染侧栏（漏洞 / 任务概览）。
func (m *Model) sideView(height int) string {
	var b strings.Builder
	switch m.activePane {
	case panelFindings:
		b.WriteString(stylePanelTitle.Render("漏洞"))
		b.WriteString("\n")
		b.WriteString(m.findingsPane())
	case panelTasks:
		b.WriteString(stylePanelTitle.Render("任务"))
		b.WriteString("\n")
		b.WriteString(m.tasksPane())
	default:
		b.WriteString(stylePanelTitle.Render("概览"))
		b.WriteString("\n")
		b.WriteString(m.overviewPane())
	}
	return stylePanel.Width(30).Height(height - 2).Render(b.String())
}

// overviewPane 渲染概览：任务状态、目标进度、漏洞分档。
func (m *Model) overviewPane() string {
	if m.taskID == "" {
		return styleHelp.Render("尚未开始任务。\n\n按 Ctrl-N 输入授权目标，\n或用 -t 参数直接开跑。")
	}
	var b strings.Builder
	state := m.taskState.Status
	if state == "" {
		state = "created"
	}
	b.WriteString(lipgloss.NewStyle().Foreground(colMuted).Render("状态"))
	b.WriteString("  ")
	b.WriteString(statusStyle(state).Render(state))
	b.WriteString("\n\n")

	if m.taskState.GoalsTotal > 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(colMuted).Render("目标"))
		b.WriteString(fmt.Sprintf("  %d/%d\n\n", m.taskState.GoalsMet, m.taskState.GoalsTotal))
	}
	if m.taskState.Tokens > 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(colMuted).Render("Token"))
		b.WriteString(fmt.Sprintf("  %d\n\n", m.taskState.Tokens))
	}

	b.WriteString(lipgloss.NewStyle().Foreground(colMuted).Render("漏洞"))
	b.WriteString("\n")
	c := m.taskState.Findings
	if c.Total() == 0 {
		b.WriteString(styleHelp.Render("  暂无"))
	} else {
		if c.Critical > 0 {
			b.WriteString("  " + severityStyle("critical").Render(fmt.Sprintf("critical  %d", c.Critical)) + "\n")
		}
		if c.High > 0 {
			b.WriteString("  " + severityStyle("high").Render(fmt.Sprintf("high      %d", c.High)) + "\n")
		}
		if c.Medium > 0 {
			b.WriteString("  " + severityStyle("medium").Render(fmt.Sprintf("medium    %d", c.Medium)) + "\n")
		}
		if c.Low > 0 {
			b.WriteString("  " + severityStyle("low").Render(fmt.Sprintf("low       %d", c.Low)) + "\n")
		}
	}
	return b.String()
}

// findingsPane 渲染漏洞列表。
func (m *Model) findingsPane() string {
	if len(m.findings) == 0 {
		return styleHelp.Render("本任务暂未记录到漏洞。")
	}
	var b strings.Builder
	for _, f := range m.findings {
		name := f.Name
		if strings.TrimSpace(name) == "" {
			name = f.VulnClass
		}
		sev := strings.ToUpper(normalizeSeverity(f.Severity))
		if sev == "" {
			sev = "?"
		}
		b.WriteString(severityStyle(f.Severity).Render("[" + sev + "]"))
		b.WriteString(" ")
		b.WriteString(lipgloss.NewStyle().Foreground(colText).Render(truncate(name, 20)))
		b.WriteString("\n")
	}
	return b.String()
}

// tasksPane 渲染任务列表。
func (m *Model) tasksPane() string {
	if len(m.taskList) == 0 {
		return styleHelp.Render("暂无任务。")
	}
	var b strings.Builder
	for _, t := range m.taskList {
		marker := "  "
		if t.Current {
			marker = "› "
		}
		b.WriteString(marker)
		b.WriteString(statusStyle(t.Status).Render("●"))
		b.WriteString(" ")
		b.WriteString(lipgloss.NewStyle().Foreground(colText).Render(truncate(t.Name, 18)))
		b.WriteString("\n")
	}
	return b.String()
}

// renderStream 把活动流重新渲染进视口。
//
// 保持视口贴底：用户在等结果时，视野应该始终跟着最新一条走。
// 用户手动往上滚之后就不再强制贴底——把他拽回底部比不贴底更烦人。
func (m *Model) renderStream() {
	lines := m.streamLines()
	atBottom := m.stream.AtBottom()
	m.stream.SetContent(strings.Join(lines, "\n"))
	if atBottom {
		m.stream.GotoBottom()
	}
}

// streamLines 生成活动流的每一行。
func (m *Model) streamLines() []string {
	lines := make([]string, 0, len(m.activities)+len(m.liveOrder)+1)
	if len(m.activities) == 0 && len(m.liveOrder) == 0 {
		return []string{styleHelp.Render("等待活动…")}
	}
	width := m.stream.Width
	for _, a := range m.activities {
		lines = append(lines, m.wrapActivityLine(m.activityLine(a), width)...)
	}
	// 实时帧跟在最后：它们是"正在发生"的增量，位置最靠底才符合直觉。
	for _, key := range m.liveOrder {
		a, ok := m.live[key]
		if !ok {
			continue
		}
		lines = append(lines, m.wrapActivityLine(m.liveLine(a), width)...)
	}
	return lines
}

// wrapActivityLine 对活动行做折行。
//
// 折行时把 "[worker] " 前缀只留在首行，续行缩进对齐正文——
// 每行都重复 "[worker]" 会让长文本读起来像日志而不像对话。
func (m *Model) wrapActivityLine(line string, width int) []string {
	if line == "" {
		return nil
	}
	if lipgloss.Width(line) <= width {
		return []string{line}
	}
	prefix := ""
	body := line
	if strings.HasPrefix(line, "[") {
		if end := strings.Index(line, "] "); end > 0 {
			prefix = line[:end+2]
			body = line[end+2:]
		}
	}
	indent := strings.Repeat(" ", lipgloss.Width(prefix))
	parts := wrapLine(body, width-lipgloss.Width(prefix))
	out := make([]string, 0, len(parts))
	for i, p := range parts {
		if i == 0 {
			out = append(out, prefix+p)
			continue
		}
		out = append(out, indent+p)
	}
	return out
}

// activityLine 渲染一条落库活动。
func (m *Model) activityLine(a Activity) string {
	worker := a.Worker
	if worker == "" {
		worker = "system"
	}
	label := lipgloss.NewStyle().Foreground(colFaint).Render("[" + worker + "]")
	switch a.Kind {
	case "thinking":
		if !m.verbose {
			return ""
		}
		return label + " " + lipgloss.NewStyle().Foreground(colFaint).Render(a.Summary)
	case "text":
		if strings.TrimSpace(a.Summary) == "" {
			return ""
		}
		return label + " " + lipgloss.NewStyle().Foreground(colText).Render(a.Summary)
	case "tool_use":
		return label + " " + lipgloss.NewStyle().Foreground(colInfo).Render("→ ") +
			lipgloss.NewStyle().Foreground(colText).Render(a.Summary)
	case "tool_result":
		mark := lipgloss.NewStyle().Foreground(colFaint).Render("←")
		if a.IsError {
			mark = lipgloss.NewStyle().Foreground(colErr).Render("✗")
		}
		return label + " " + mark + " " + lipgloss.NewStyle().Foreground(colMuted).Render(a.Summary)
	case "result":
		// finish_task / submit_report 会取消该 agent 的 run，capture 因而补一条
		//「（运行被中断…未完成）」。任务实际已按交卷收尾，这条只会误导。
		if strings.HasPrefix(strings.TrimSpace(a.Summary), "（运行被中断") {
			return ""
		}
		mark := lipgloss.NewStyle().Foreground(colOK).Render("✓")
		if a.IsError {
			mark = lipgloss.NewStyle().Foreground(colErr).Render("✗")
		}
		return label + " " + mark + " " + lipgloss.NewStyle().Foreground(colText).Render(a.Summary)
	case "round":
		return label + " " + lipgloss.NewStyle().Foreground(colFaint).Render(a.Summary)
	case "usage":
		return ""
	default:
		if strings.TrimSpace(a.Summary) == "" {
			return ""
		}
		return label + " " + lipgloss.NewStyle().Foreground(colText).Render(a.Summary)
	}
}

// liveLine 渲染一条实时增量帧。
func (m *Model) liveLine(a Activity) string {
	worker := a.Worker
	if worker == "" {
		worker = "system"
	}
	label := lipgloss.NewStyle().Foreground(colFaint).Render("[" + worker + "]")
	style := lipgloss.NewStyle().Foreground(colFaint)
	if a.Kind == "text" {
		style = lipgloss.NewStyle().Foreground(colText)
	}
	// 实时帧是累计全文，末尾标一个续写符号表示"还在写"。
	return label + " " + style.Render(a.Summary) +
		lipgloss.NewStyle().Foreground(colAccent).Render("▌")
}

// truncate 按显示宽度截断字符串，超出部分用省略号收尾。
//
// 用显示宽度而不是字节数：中文是双宽字符，按字节截会切出半个字。
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(s)
	if lipgloss.Width(s) <= width {
		return s
	}
	if len(runes) <= width {
		return s
	}
	out := string(runes[:width-1])
	return out + "…"
}

// wrapLine 按显示宽度对一行文本做软换行。
//
// 视口只会横向裁切（ansi.Cut），不会换行——不预先折行的话，
// 长 summary 会被直接切掉右半边，用户看不到结论。中文是双宽字符，
// 所以必须按显示宽度而非字节数折行。
func wrapLine(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	if lipgloss.Width(s) <= width {
		return []string{s}
	}
	var out []string
	var cur strings.Builder
	curWidth := 0
	// 按空格断词；中文没有空格，退化为逐字断行。
	tokens := strings.Split(s, " ")
	for _, tok := range tokens {
		tokWidth := lipgloss.Width(tok)
		if curWidth > 0 && curWidth+1+tokWidth > width {
			out = append(out, cur.String())
			cur.Reset()
			curWidth = 0
		}
		if tokWidth > width {
			// 单个词就超宽（多半是长 URL 或中文长句）：逐字切。
			for _, r := range tok {
				rw := lipgloss.Width(string(r))
				if curWidth+rw > width && curWidth > 0 {
					out = append(out, cur.String())
					cur.Reset()
					curWidth = 0
				}
				cur.WriteRune(r)
				curWidth += rw
			}
			continue
		}
		if curWidth > 0 {
			cur.WriteByte(' ')
			curWidth++
		}
		cur.WriteString(tok)
		curWidth += tokWidth
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	if len(out) == 0 {
		return []string{s}
	}
	return out
}
