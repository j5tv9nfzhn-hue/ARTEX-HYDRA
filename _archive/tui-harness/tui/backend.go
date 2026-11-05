package tui

import (
	"context"
	"time"
)

// TUIBackend 是 TUI 与 ARTEX 后端之间的唯一接缝。
//
// 抽这一层的理由与 OpenCode 的「TUI 是服务端 API 的客户端」是同一个道理：
// 界面只描述"我需要什么"，不描述"怎么拿到"。好处有三：
//   - TUI 可以脱离 SQLite、agent 与网络单测（用假后端喂事件即可）；
//   - 将来若要让 TUI 连远端服务，只需换一份 Backend 实现，渲染层不动；
//   - 后端不因"要迁就界面"而长出第二套语义（同一任务只有一条创建路径）。
type TUIBackend interface {
	// CreateTask 按授权目标建一个任务并启动编排，返回任务 id。
	CreateTask(ctx context.Context, targets []string, instruction string, opts TaskOptions) (string, error)

	// Subscribe 订阅任务的活动流。返回的 channel 关闭即表示流结束；
	// 调用方必须调用返回的 cancel 释放订阅。
	Subscribe(ctx context.Context, taskID string) (<-chan Activity, func())

	// Send 把人类输入作为 instruction 投给根 agent。
	Send(ctx context.Context, taskID string, text string) error

	// TaskStatus 返回任务的当前状态（用于状态栏与终态判定）。
	TaskStatus(ctx context.Context, taskID string) (TaskState, error)

	// Findings 返回任务已记录的漏洞摘要，按严重度排序。
	Findings(ctx context.Context, taskID string) ([]Finding, error)

	// Control 对任务执行控制动作（pause / resume）。
	Control(ctx context.Context, taskID string, action string) error

	// ModelLabel 返回当前生效的 LLM 配置标签，用于状态栏展示。
	ModelLabel(ctx context.Context) string

	// ActiveModelChanged 返回自上次调用以来激活配置是否发生变化。
	// TUI 用它提示"Web 控制台改了配置，下一回合生效"——与 Web 侧共用同一份 artex.db，
	// 配置只有 Web 一个来源，界面这边只读不写。
	ActiveModelChanged(ctx context.Context) (changed bool, label string)
}

// TaskOptions 是建任务时的可选参数。
type TaskOptions struct {
	// Name 是任务名；空则按目标自动生成。
	Name string
	// TimeoutSeconds 是任务级超时；0 = 不限。
	TimeoutSeconds int
}

// TaskState 是任务在界面层需要知道的最小状态集。
type TaskState struct {
	// ID 是任务 id。
	ID string
	// Status 是生命周期状态：created/queued/running/paused/done/failed/timeout。
	Status string
	// Findings 是按严重度分档的漏洞计数。
	Findings SeverityCounts
	// Tokens 是任务级 token 消耗合计。
	Tokens int
	// InFlight 是当前运行中的子代理数。
	InFlight int
	// GoalsTotal / GoalsMet 是目标进度。
	GoalsTotal int
	GoalsMet   int
	// StartedAt 是任务首次开跑时间；0 = 尚未开跑。
	StartedAt time.Time
}

// SeverityCounts 是按严重度分档的漏洞计数。
type SeverityCounts struct {
	Critical int
	High     int
	Medium   int
	Low      int
}

// Total 返回四档合计。
func (s SeverityCounts) Total() int {
	return s.Critical + s.High + s.Medium + s.Low
}

// Activity 是活动流中的一条事件。
//
// 字段与 db.Activity 对齐但刻意不直接复用：界面层不该被数据库结构绑住。
// Kind 取值与 Web 侧一致：thinking/text/tool_use/tool_result/result/round/usage。
type Activity struct {
	// Kind 是事件类型。
	Kind string
	// Worker 是会话归属（root / work#N / chat / system）。
	Worker string
	// Tool 是工具名（tool_use / tool_result 才有）。
	Tool string
	// Summary 是可读摘要。
	Summary string
	// IsError 标记该事件是否表示出错。
	IsError bool
}

// Finding 是界面展示用的一条漏洞摘要。
type Finding struct {
	// ID 是漏洞行 id。
	ID int64
	// Severity 是 critical/high/medium/low。
	Severity string
	// Name 是漏洞名（空则回退 VulnClass）。
	Name string
	// VulnClass 是漏洞类别。
	VulnClass string
	// Status 是处置状态（pending/resolved/false_positive/ignored）。
	Status string
	// Summary 是漏洞摘要。
	Summary string
}
