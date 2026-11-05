package tui

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// Result 是 TUI 退出后的结果。
//
// 退出码由调用方按 Status/Findings 决定（Strix 口径：有漏洞 → 2），
// 与 Web 控制台的任务状态口径一致。
type Result struct {
	// TaskID 是本次会话跑的任务 id；没开任务则为空。
	TaskID string
	// Status 是任务终态；未开任务或仍在跑则为空。
	Status string
	// Findings 是任务已记录的漏洞摘要。
	Findings []Finding
}

// HasFindings 报告本次会话是否记录到漏洞。
func (r Result) HasFindings() bool { return len(r.Findings) > 0 }

// Run 启动 TUI 并阻塞到用户退出。
//
// opts.Targets 非空表示直接开跑；为空则界面停在目标输入态。
// 目标为空是合法的：用户可能想先看看界面，或用 Ctrl-N 之后再开任务。
func Run(ctx context.Context, backend TUIBackend, opts Options) (Result, error) {
	if backend == nil {
		return Result{}, errors.New("TUI 未装配后端")
	}
	// TUI 接管终端：关闭 Bubble Tea 自带的信号处理，由调用方（cmd 层）统一收口。
	// 两处都注册 SIGINT 会互相抢，Ctrl-C 的行为将不可预测。
	p := tea.NewProgram(
		NewWithOptions(ctx, backend, opts),
		tea.WithAltScreen(),
		tea.WithContext(ctx),
		tea.WithoutSignalHandler(),
	)
	final, err := p.Run()
	if err != nil {
		if errors.Is(err, tea.ErrProgramKilled) || errors.Is(err, context.Canceled) {
			return Result{}, err
		}
		return Result{}, fmt.Errorf("TUI 退出：%w", err)
	}
	m, ok := final.(*Model)
	if !ok {
		return Result{}, errors.New("TUI 返回了未知的模型")
	}
	res := Result{TaskID: m.taskID}
	if m.taskID != "" {
		// 退出时以库里的真实状态为准，而不是界面内存里的快照：
		// 用户可能在任务跑着的时候就退出，此时 m.taskState 未必是最新的，
		// 而退出码要如实反映"这个任务最后是什么结果"。
		if st, err := backend.TaskStatus(ctx, m.taskID); err == nil {
			res.Status = st.Status
		} else {
			res.Status = m.taskState.Status
		}
		if fs, err := backend.Findings(ctx, m.taskID); err == nil {
			res.Findings = fs
		}
	}
	return res, nil
}

// Options 是 TUI 的启动参数。
type Options struct {
	// Targets 是启动即开跑的目标；为空则等用户在界面里输入。
	Targets []string
	// Instruction 是给根 agent 的初始要求（可空）。
	Instruction string
	// Name 是任务名；空则按目标自动生成。
	Name string
	// Timeout 是任务级超时秒数；0 = 不限。
	Timeout int
}
