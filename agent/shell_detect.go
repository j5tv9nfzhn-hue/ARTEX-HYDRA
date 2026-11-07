package agent

// 探测本机可用的 Shell，供设置页决定「哪些选项该出现」。
//
// 硬性要求（用户 2026-10-23）：
//   - 不做 auto：下拉框只放明确选项；
//   - Git Bash **检测到才出现**，没装就不给这个选项（而不是选了之后报错）；
//   - 任何情况下不静默回落。
//
// 与 ResolveShell 的关系：这里对每个模式各解析一次，用「能不能真跑起来」当可用性
// 判据（见 shell_probe.go 的理由）；用户选了某个模式后，运行时仍会独立解析一遍，
// 两次结论不一致（例如探测后被人删了文件）就在执行时报 config 错。

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ShellOption 是一个可选项及其本机可用性。
type ShellOption struct {
	Mode      ShellMode `json:"mode"`
	Label     string    `json:"label"`
	Available bool      `json:"available"`
	Bin       string    `json:"bin,omitempty"`
	Version   string    `json:"version,omitempty"`
	Flavor    string    `json:"flavor,omitempty"`
	Note      string    `json:"note,omitempty"`
	Error     string    `json:"error,omitempty"`
	// Rank 是优先顺序（0 = 首选）。UI 用它排序并给首选打「推荐」标记，
	// 而不是由前端各写一套判断。见 PreferredShellOrder。
	Rank int `json:"rank"`
	// Recommended 表示本机可用 + 排在可选里是否第一可用的那个。
	// 与 PreferredShellOrder 的差异：这里只认**探测通过**的选项，
	// 没装 Git for Windows 的机器上 PowerShell 才是推荐项。
	Recommended bool `json:"recommended"`
}

// defaultResolveDeps 是生产环境的依赖注入：真实 PATH、真实文件系统、真实探测。
func defaultResolveDeps() ResolveDeps {
	return ResolveDeps{
		GOOS:     runtime.GOOS,
		Env:      os.Getenv,
		LookPath: exec.LookPath,
		Exists: func(p string) bool {
			st, err := os.Stat(p)
			return err == nil && !st.IsDir()
		},
		Probe: probeShellBinary,
		GitExecPath: func() (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "git", "--exec-path").Output()
			return strings.TrimSpace(string(out)), err
		},
	}
}

// DetectShellOptions 返回设置页该显示的选项集合。
//
// 顺序即推荐顺序（作者 2026-11-07 Issue #6 §4-C 的选型结论）：
//
//	Windows:  Git Bash → PowerShell（7 优先）→ CMD（不建议）
//	其它平台: bash
//
// 非 Windows 只返回 bash 一项（cmd / powershell 在该平台没有意义）。
//
// 排序按 PreferredShellOrder 生成，**不按"平台默认"生成**：默认值只决定新装机
// 落库的初始值，而这个列表是给用户看的建议顺序，两者口径不同刻意分开。
func DetectShellOptions() []ShellOption {
	deps := defaultResolveDeps()
	modes := orderedShellModes(runtime.GOOS)
	out := make([]ShellOption, 0, len(modes))
	for i, m := range modes {
		opt := ShellOption{Mode: m, Label: shellModeLabel(m), Rank: i}
		spec, err := ResolveShell(m, deps)
		switch {
		case err == nil:
			opt.Available = true
			opt.Bin = spec.Bin
			opt.Version = spec.Version
			opt.Flavor = spec.Flavor
			opt.Note = spec.Note
		case err != nil:
			opt.Error = err.Error()
		}
		if m == ShellModeBash && runtime.GOOS == "windows" {
			// Git Bash 是可选依赖：没装就不显示（装了的判定即「bash 能真跑」）。
			opt.Label = "Git Bash"
		}
		out = append(out, opt)
	}
	// 第一个「探测通过」的选项即推荐项。全都不可用时没有推荐项，UI 应直接
	// 显示各选项的 Error，让用户去修安装而不是猜。
	for i := range out {
		if out[i].Available {
			out[i].Recommended = true
			break
		}
	}
	return out
}

// orderedShellModes 按优先顺序给出该平台要探测的模式，并补齐"虽然排在前面
// 但不在首选列表里"的兜底项（Windows 的 cmd），保证设置页始终能看见全部三档。
func orderedShellModes(goos string) []ShellMode {
	if goos != "windows" {
		return []ShellMode{ShellModeBash}
	}
	order := PreferredShellOrder(goos)
	out := make([]ShellMode, 0, len(order)+1)
	seen := map[ShellMode]bool{}
	for _, m := range order {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	// cmd 永远排在最后：可用但明确不建议（语法差异最大）。
	for _, m := range AllShellModes() {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// ShellPreferenceByMode 在探测结果里找出某个模式的优先说明，供设置页回显。
func ShellPreferenceByMode(mode ShellMode, goos string) string {
	return ShellPreferenceNote(mode, goos)
}

// shellModeLabel 是 UI 文案。刻意写清「谁在背后跑」，避免用户以为 PowerShell
// 选项等于 Windows PowerShell 5.1。
func shellModeLabel(m ShellMode) string {
	switch m {
	case ShellModePowerShell:
		return "PowerShell"
	case ShellModeCmd:
		return "CMD（cmd.exe）"
	case ShellModeBash:
		if runtime.GOOS == "windows" {
			return "Git Bash"
		}
		return "Bash"
	}
	return string(m)
}

// ResolveShellWithOverride 用设置页给出的显式路径（可为空）解析一次。
func ResolveShellWithOverride(mode ShellMode, override string) (ShellSpec, error) {
	deps := defaultResolveDeps()
	deps.Override = strings.TrimSpace(override)
	return ResolveShell(mode, deps)
}

// PlatformGOOS 暴露 runtime.GOOS（server 侧需要生成默认值时用）。
func PlatformGOOS() string { return runtime.GOOS }

// ResolveDefaultShell 按平台默认值解析一次（首次启动写默认设置时用）。
func ResolveDefaultShell() (ShellSpec, error) {
	return ResolveShell(DefaultShellMode(runtime.GOOS), defaultResolveDeps())
}
