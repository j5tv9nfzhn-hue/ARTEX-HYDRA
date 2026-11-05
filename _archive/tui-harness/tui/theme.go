// Package tui 是 ARTEX-HYDRA 的全屏终端界面。
//
// 分层口径（与 OpenCode 对齐）：TUI 只负责"呈现与交互"，不持有业务逻辑。
// 后端能力（建任务、订阅活动流、投递指令、读漏洞）由 server.TUISession 暴露，
// TUI 通过 TUIBackend 接口与它对话——这样界面可以脱离数据库与 agent 单测，
// 换一套后端也不必重写渲染层。
//
// 三条硬约束：
//   - 不占端口、不起浏览器：与 Web 控制台共用同一个 artex.db，但不监听 HTTP；
//   - 只有一个最终出口：任务由根 agent 的 finish_task 收尾，TUI 只呈现与汇总；
//   - 人类输入与 Web 同语义：文字作为 instruction 投给根 agent，不另起对话上下文。
package tui

import "github.com/charmbracelet/lipgloss"

// 调色板：与 Web 控制台的 severity 色板保持同一套语义
// （critical 最重、low 最轻），终端里也一眼能对上。
var (
	colAccent  = lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#7C6CF0"}
	colMuted   = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"}
	colText    = lipgloss.AdaptiveColor{Light: "#111827", Dark: "#E5E7EB"}
	colFaint   = lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#6B7280"}
	colOK      = lipgloss.AdaptiveColor{Light: "#047857", Dark: "#34D399"}
	colWarn    = lipgloss.AdaptiveColor{Light: "#B45309", Dark: "#FBBF24"}
	colErr     = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
	colCrit    = lipgloss.AdaptiveColor{Light: "#991B1B", Dark: "#EF4444"}
	colHigh    = lipgloss.AdaptiveColor{Light: "#C2410C", Dark: "#FB923C"}
	colMedium  = lipgloss.AdaptiveColor{Light: "#A16207", Dark: "#FACC15"}
	colLow     = lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#4ADE80"}
	colInfo    = lipgloss.AdaptiveColor{Light: "#0E7490", Dark: "#22D3EE"}
	colSelBg   = lipgloss.AdaptiveColor{Light: "#E8E8FB", Dark: "#2A2A44"}
	colPanelBg = lipgloss.AdaptiveColor{Light: "#FAFAFA", Dark: "#171717"}
)

// 布局样式。
//
// 头部/底栏用「上/下边框 + 填充」而不是整屏 border：全屏 border 会把可用
// 高度吃掉两行，长任务里活动流少两行是实打实的损失。
var (
	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(colText).
			Background(colPanelBg).
			Padding(0, 1)

	styleFooter = lipgloss.NewStyle().
			Foreground(colMuted).
			Background(colPanelBg).
			Padding(0, 1)

	styleStatusBar = lipgloss.NewStyle().
			Foreground(colFaint).
			Padding(0, 1)

	stylePanel = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colFaint).
			Padding(0, 1)

	stylePanelTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colAccent)

	stylePrompt = lipgloss.NewStyle().
			Bold(true).
			Foreground(colAccent)

	styleInput = lipgloss.NewStyle().
			Foreground(colText)

	styleSelected = lipgloss.NewStyle().
			Foreground(colText).
			Background(colSelBg).
			Bold(true)

	styleBadge = lipgloss.NewStyle().
			Foreground(colPanelBg).
			Background(colAccent).
			Bold(true).
			Padding(0, 1)

	styleHelp = lipgloss.NewStyle().Foreground(colMuted)
)

// severityStyle 返回某严重度的展示样式。白名单外的严重度按 low 处理，
// 不给它单独的色——避免出现"看不懂的第三种红"。
func severityStyle(sev string) lipgloss.Style {
	switch normalizeSeverity(sev) {
	case "critical":
		return lipgloss.NewStyle().Foreground(colCrit).Bold(true)
	case "high":
		return lipgloss.NewStyle().Foreground(colHigh).Bold(true)
	case "medium":
		return lipgloss.NewStyle().Foreground(colMedium)
	case "low":
		return lipgloss.NewStyle().Foreground(colLow)
	default:
		return lipgloss.NewStyle().Foreground(colFaint)
	}
}

// statusStyle 返回任务状态的展示样式。
func statusStyle(status string) lipgloss.Style {
	switch status {
	case "running":
		return lipgloss.NewStyle().Foreground(colOK).Bold(true)
	case "done":
		return lipgloss.NewStyle().Foreground(colOK)
	case "failed", "timeout":
		return lipgloss.NewStyle().Foreground(colErr).Bold(true)
	case "paused":
		return lipgloss.NewStyle().Foreground(colWarn)
	case "queued":
		return lipgloss.NewStyle().Foreground(colInfo)
	default:
		return lipgloss.NewStyle().Foreground(colMuted)
	}
}

// normalizeSeverity 把严重度归一到 critical/high/medium/low 四档。
func normalizeSeverity(sev string) string {
	switch sev {
	case "critical", "high", "medium", "low":
		return sev
	default:
		return ""
	}
}
