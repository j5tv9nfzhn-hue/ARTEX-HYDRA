package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// 消息类型：Bubble Tea 的异步结果都以消息回到 Update。
//
// 全部走消息而不是直接改模型，是 Bubble Tea 的核心约定：
// 只有 Update 能改状态，渲染才是纯函数，界面才不会因为并发回调而错乱。

// activityMsg 是一条落库活动。
type activityMsg struct {
	activity Activity
}

// liveMsg 是一条实时增量帧。
type liveMsg struct {
	activity Activity
}

// statusMsg 是一次任务状态刷新结果。
type statusMsg struct {
	state TaskState
	err   error
}

// findingsMsg 是一次漏洞列表刷新结果。
type findingsMsg struct {
	findings []Finding
}

// tasksMsg 是一次任务列表刷新结果。
type tasksMsg struct {
	tasks []TaskSummary
}

// errMsg 是一次异步失败。
type errMsg struct {
	err error
}

// taskCreatedMsg 表示任务已建成。
type taskCreatedMsg struct {
	id string
}

// statusTickMsg 是状态轮询的一次心跳。
type statusTickMsg struct{}

// statusPollInterval 是任务状态的轮询间隔。
//
// 1 秒：比 Web 的 3 秒更快（终端是"盯着看"的场景），但也不至于把 SQLite
// 打成热点——单任务状态只读一次聚合查询。
const statusPollInterval = time.Second

// createTaskCmd 建任务并启动编排，成功后开始订阅活动流。
//
// 启动参数（-i / --name / --timeout）只作用于首个任务：它们是"本次任务"的
// 附加说明，用户随后用 Ctrl-N 开的第二个任务如果再带上一次的要求，
// 会变成"悄悄替用户下指令"。消费标记在这里置位——createTaskCmd 只会
// 在 Update 里被调用，单线程，不存在竞态。
func (m *Model) createTaskCmd(targets []string) tea.Cmd {
	opts, instr := TaskOptions{}, ""
	if !m.optsConsumed {
		m.optsConsumed = true
		opts = TaskOptions{Name: m.opts.Name, TimeoutSeconds: m.opts.Timeout}
		instr = m.opts.Instruction
	}
	return func() tea.Msg {
		if m.backend == nil {
			return errMsg{err: fmt.Errorf("TUI 未装配后端")}
		}
		id, err := m.backend.CreateTask(m.ctx, targets, instr, opts)
		if err != nil {
			return errMsg{err: err}
		}
		return taskCreatedMsg{id: id}
	}
}

// subscribeCmd 从活动流里取下一条事件。
//
// 这是 Bubble Tea 的标准「拉」式订阅：每次取到一条就重新挂一次，
// 于是 Update 里只需要处理 activityMsg，不用管理 goroutine 与 channel 的关闭顺序。
// 订阅本身由后端持有，模型只管取。
func (m *Model) subscribeCmd() tea.Cmd {
	if m.taskID == "" || m.backend == nil {
		return nil
	}
	ch, cancel := m.backend.Subscribe(m.ctx, m.taskID)
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		defer cancel()
		a, ok := <-ch
		if !ok {
			return nil
		}
		return activityMsg{activity: a}
	}
}

// sendCmd 把一条指令投给根 agent。
func (m *Model) sendCmd(taskID, text string) tea.Cmd {
	return func() tea.Msg {
		if err := m.backend.Send(m.ctx, taskID, text); err != nil {
			return errMsg{err: err}
		}
		return statusMsg{}
	}
}

// controlCmd 执行任务控制动作。
func (m *Model) controlCmd(taskID, action string) tea.Cmd {
	return func() tea.Msg {
		if err := m.backend.Control(m.ctx, taskID, action); err != nil {
			return errMsg{err: err}
		}
		return statusMsg{}
	}
}

// refreshStatusCmd 拉一次任务状态。
func (m *Model) refreshStatusCmd() tea.Cmd {
	return func() tea.Msg {
		state, err := m.backend.TaskStatus(m.ctx, m.taskID)
		if err != nil {
			return statusMsg{err: err}
		}
		return statusMsg{state: state}
	}
}

// pollStatusCmd 周期性刷新任务状态。
//
// 任务终态后停止轮询：终态不会再变，继续轮询只是白烧 SQLite。
func (m *Model) pollStatusCmd() tea.Cmd {
	return tea.Tick(statusPollInterval, func(t time.Time) tea.Msg {
		return statusTickMsg{}
	})
}

// refreshFindingsCmd 拉一次漏洞列表。
func (m *Model) refreshFindingsCmd() tea.Cmd {
	return func() tea.Msg {
		fs, err := m.backend.Findings(m.ctx, m.taskID)
		if err != nil {
			return errMsg{err: err}
		}
		return findingsMsg{findings: fs}
	}
}

// refreshTasksCmd 拉一次任务列表。
func (m *Model) refreshTasksCmd() tea.Cmd {
	return func() tea.Msg {
		tasks, err := m.listTasks()
		if err != nil {
			return errMsg{err: err}
		}
		return tasksMsg{tasks: tasks}
	}
}

// listTasks 汇总任务列表。
func (m *Model) listTasks() ([]TaskSummary, error) {
	// 任务列表是后端聚合口径，不在 TUIBackend 的最小接口里；
	// 这里通过可选扩展拿到，拿不到就退回空列表。
	type lister interface {
		ListTasks(ctx context.Context) ([]TaskSummary, error)
	}
	l, ok := m.backend.(lister)
	if !ok {
		return nil, nil
	}
	return l.ListTasks(m.ctx)
}
