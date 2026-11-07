package agent

// Shell 执行器解析（任务 26）。
//
// 背景：SDK 的 Bash 工具在 Windows 上由 normo 的 shellCmd() 硬编码成
// `powershell -NoProfile -NonInteractive -Command`，而且只有 worker 的 Bash 被
// 本仓库包装过（agent/worker.go 的 WrapCrossPlatformBash）；chat / planner /
// mainagent 走的是 SDKDefaultTools()，拿到的是未包装的原始 Bash。于是同一台机器上
// 「谁能跑对命令」取决于你站在哪个 agent 里——这正是线上 Bash 失败集中在某几类
// agent 上的原因。
//
// 本文件只做一件事：把「用哪个可执行文件跑命令」变成一个显式、可校验、可落库的
// 决定，而不是由 runtime.GOOS 隐式决定。
//
// 设计约束（用户 2026-10-23 定）：
//   - 没有 auto：只有 powershell / cmd / bash 三个明确选项；
//   - Windows 默认 powershell，其它平台默认 bash；
//   - 解析必须真跑一次探针（Store/MSIX 的 pwsh.exe 别名可被 where 命中却 spawn
//     失败，Codex 为此有多个 issue：#18937 / #30742 / #336）；
//   - 解析失败 → 报错，绝不回落到别的执行器。

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// ShellMode 是用户在设置里选择的执行器。
type ShellMode string

const (
	// ShellModePowerShell = Windows PowerShell（优先 pwsh 7，其次 powershell.exe 5.1）。
	ShellModePowerShell ShellMode = "powershell"
	// ShellModeCmd = cmd.exe。
	ShellModeCmd ShellMode = "cmd"
	// ShellModeBash = POSIX 壳。Windows 上解析到 Git for Windows 的 bash.exe，
	// 其它平台解析到 /bin/bash。
	ShellModeBash ShellMode = "bash"
)

// AllShellModes 返回全部合法模式（UI 下拉框的取值来源）。
func AllShellModes() []ShellMode {
	return []ShellMode{ShellModePowerShell, ShellModeCmd, ShellModeBash}
}

// ParseShellMode 归一字符串。空串与未知值都返回 ok=false——调用方据此决定是
// 用平台默认值还是报错，绝不做「猜一个」。
func ParseShellMode(s string) (ShellMode, bool) {
	switch ShellMode(strings.ToLower(strings.TrimSpace(s))) {
	case ShellModePowerShell:
		return ShellModePowerShell, true
	case ShellModeCmd:
		return ShellModeCmd, true
	case ShellModeBash:
		return ShellModeBash, true
	}
	return "", false
}

// DefaultShellMode 是「首次启动写进 settings 的初始值」。
//
// 为什么 Windows 是 powershell 而不是 cmd：命令链（&& / 管道）与目标环境更接近
// 模型习惯的表达，且 Codex 与启用 PowerShell 工具的 Claude Code 都以此为默认。
// 为什么不是 bash：Windows 上 bash 依赖 Git for Windows，属于可选依赖，不能当默认。
//
// 注意：这仍然是「用户没设置过时写进 settings 的值」。真正的**优先顺序**见
// PreferredShellOrder —— 设置页首次打开时会按它给出建议（见 DetectShellOptions
// 的 recommended 字段）。改这里的默认值等于改所有新装机的落库值，属于产品决策，
// 本次修复不动它。
func DefaultShellMode(goos string) ShellMode {
	if goos == "windows" {
		return ShellModePowerShell
	}
	return ShellModeBash
}

// 跨平台执行器的**优先顺序**（作者 Issue #6 §4-C 的选型结论）：
//
//	Windows:  Git Bash  →  PowerShell 7 (pwsh)  →  Windows PowerShell 5.1
//	其它平台: bash
//
// 为什么 Git Bash 排第一：模型生成的命令绝大多数是 POSIX 写法（`grep -rn`、
// `2>/dev/null`、`&&`、`$(...)`）。在 PowerShell 下这些要么直接报错，要么静默
// 变成别的东西（`2>/dev/null` 会被当成重定向到文件 `null`）。而 Git Bash 是
// 真正的 bash，命令不需改写。
//
// 为什么不是"自动静默回退"：这三档之间**不是等价替换**——一条在 Git Bash 里
// 正确的命令在 PowerShell 里可能完全不同（路径分隔符、引号、重定向）。所以
// 顺序只在**设置页给出建议**时生效；运行期只按用户选定的模式解析，解析不到
// 就报错（见 ResolveShell 的 ErrShellNotAvailable）。这也是文件头「解析失败 →
// 报错，绝不回落到别的执行器」那条约束的落点。
//
// 5.1 排最后且不建议：不支持 `&&`、默认非 UTF-8、ScriptBlock 日志在某些策略下
// 直接拒绝执行；Issue 里明确写了「PS 5.1 与 CMD 不建议」。
func PreferredShellOrder(goos string) []ShellMode {
	if goos == "windows" {
		return []ShellMode{ShellModeBash, ShellModePowerShell}
	}
	return []ShellMode{ShellModeBash}
}

// ShellPreferenceNote 返回该模式在优先顺序里的定位说明，供设置页把
// 「建议 / 可用 / 不建议」三种态度写清楚，而不是让用户凭喜好选。
func ShellPreferenceNote(mode ShellMode, goos string) string {
	switch {
	case goos != "windows":
		if mode == ShellModeBash {
			return "推荐：本机默认执行器"
		}
		return "不建议：该模式只在 Windows 上可用"
	case mode == ShellModeBash:
		return "推荐：Git for Windows 的 bash，模型生成的 POSIX 命令可直接执行（需已安装 Git for Windows）"
	case mode == ShellModePowerShell:
		return "次选：没有 Git Bash 时的最佳选择；优先 PowerShell 7（pwsh），5.1 对 && 与 UTF-8 支持较差"
	default:
		return "不建议：cmd 的语法与模型产出的命令差异最大，仅作兜底"
	}
}

// ShellSpec 是一次解析的结果。落库的是 Mode / Bin / Version 三项。
type ShellSpec struct {
	Mode    ShellMode
	Bin     string // 绝对路径
	Version string // "7.4.2" / "5.1" / "5.3.15" / ""（cmd 无版本概念）
	// Flavor 区分同一 Mode 下的不同实现：pwsh / windows-powershell / cmd / bash。
	// 契约文案按它生成——pwsh 7 支持 &&，5.1 不支持，这两者不能共用一段提示。
	Flavor string
	// Note 是解析过程中的非致命提示（例如「命中的是 Store/MSIX 别名」），
	// 用于设置页回显，不影响执行。
	Note string
}

// Zero 报告这是一份空 spec（未解析）。
func (s ShellSpec) Zero() bool { return s.Bin == "" }

// Major 返回主版本号（解析不出来时返回 0）。
func (s ShellSpec) Major() int {
	if s.Version == "" {
		return 0
	}
	end := strings.IndexAny(s.Version, ".")
	if end < 0 {
		end = len(s.Version)
	}
	n := 0
	for _, r := range s.Version[:end] {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// describe 是给日志/设置页看的一行。
func (s ShellSpec) describe() string {
	if s.Zero() {
		return "未解析"
	}
	ver := s.Version
	if ver == "" {
		ver = "-"
	}
	return fmt.Sprintf("%s %s（%s）", s.Mode, ver, s.Bin)
}

var (
	// ErrShellNotAvailable 表示所选执行器在本机不可用。调用方必须把它原样
	// 呈现给用户，不能改用它者。
	ErrShellNotAvailable = errors.New("所选 Shell 在本机不可用")
	// ErrShellModeUnsupported 表示该模式与平台不匹配（例如 Linux 上选 cmd）。
	ErrShellModeUnsupported = errors.New("该 Shell 模式在当前平台不可用")
)

// ResolveDeps 把解析所需的外部能力注入进来，便于在 Linux 上测试 Windows 路径。
type ResolveDeps struct {
	GOOS     string
	Env      func(string) string
	LookPath func(string) (string, error)
	Exists   func(string) bool
	// Probe 真跑一次极简命令，返回版本号；失败即视为不可用。
	Probe func(exe string, mode ShellMode) (version string, err error)
	// GitExecPath 返回 `git --exec-path` 的输出，用来反推 Git 安装根目录
	// （本机 bash 不在 PATH，只有 E:\Git\bin\bash.exe，靠这一步才能找到）。
	GitExecPath func() (string, error)
	// Override 是设置页里填的显式路径（shell_bin），优先级高于 ARTEX_SHELL_BIN。
	Override string
}

// ResolveShell 按用户选定的模式解析出可执行文件。顺序与取舍：
//
//	powershell: pwsh（PATH → 标准安装目录）→ powershell.exe
//	cmd:        %COMSPEC% → System32\cmd.exe
//	bash:       PATH → git 反推 → 标准安装目录
//
// 每一级都必须 Probe 通过才接受。ARTEX_SHELL_BIN 在 candidate 里优先级最高，
// 且它失败时直接报错（显式覆盖失败不该被悄悄跳过）。
func ResolveShell(mode ShellMode, deps ResolveDeps) (ShellSpec, error) {
	if deps.GOOS == "" {
		deps.GOOS = runtime.GOOS
	}
	if deps.Env == nil {
		deps.Env = func(string) string { return "" }
	}
	if deps.Exists == nil {
		deps.Exists = func(string) bool { return false }
	}
	if deps.LookPath == nil {
		deps.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	}
	if deps.Probe == nil {
		deps.Probe = probeShellBinary
	}

	switch mode {
	case ShellModePowerShell, ShellModeCmd:
		if deps.GOOS != "windows" {
			return ShellSpec{}, fmt.Errorf("%w：%s 只能在 Windows 上使用", ErrShellModeUnsupported, mode)
		}
	case ShellModeBash:
		// 全平台可用
	default:
		return ShellSpec{}, fmt.Errorf("%w：未知模式 %q", ErrShellModeUnsupported, mode)
	}

	override := strings.TrimSpace(deps.Override)
	if override == "" {
		override = strings.TrimSpace(deps.Env("ARTEX_SHELL_BIN"))
	}
	candidates := shellCandidates(mode, deps)
	if override != "" {
		// 显式覆盖失败即报错：用户既然指定了路径，就不该被我们换成别的 exe。
		if _, err := deps.Probe(override, mode); err != nil {
			return ShellSpec{}, fmt.Errorf("%w：ARTEX_SHELL_BIN=%s 无法执行（%v）", ErrShellNotAvailable, override, err)
		}
		spec, err := buildSpec(override, mode, deps)
		if err != nil {
			return ShellSpec{}, err
		}
		spec.Note = "来自显式路径设置"
		return spec, nil
	}

	// 真实安装路径优先：Store/MSIX 的 pwsh 别名（...\Microsoft\WindowsApps\pwsh.exe）
	// 在非交互 spawn 下会报 1312，能不用就不用。
	sort.SliceStable(candidates, func(i, j int) bool {
		return !isStoreAlias(candidates[i]) && isStoreAlias(candidates[j])
	})

	// 探测全部候选后再决定，而不是"第一个能跑的就要"。
	//
	// 为什么必须探测完：candidates 的顺序只能表达"路径优先级"，表达不了**版本**。
	// 现场很常见的一种机器是：PATH 里 `powershell` 命中 5.1（Windows 自带、永远在），
	// 而 pwsh 7 装在 C:\Program Files\PowerShell\7 却没进 PATH。按"先到先得"就直接
	// 选了 5.1，于是 `&&`、`2>$null` 之外的一切 POSIX 写法全废——而 7 明明可用。
	//
	// 现在的规则（与作者 Issue #6 §4-C 一致）：
	//   - 先按"非 Store 别名优先"排；
	//   - 同档内 PowerShell 按**主版本号从高到低**，7.x 一律优先于 5.1；
	//   - 版本探测失败不影响可用性判断（能跑命令就够），按 0 参与排序。
	var resolved []ShellSpec
	var firstErr error
	for _, cand := range candidates {
		if cand == "" {
			continue
		}
		if _, err := deps.Probe(cand, mode); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", cand, err)
			}
			continue
		}
		spec, err := buildSpec(cand, mode, deps)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if isStoreAlias(cand) {
			spec.Note = "命中的是 Store/MSIX 别名，若命令无法启动请改用 MSI 安装版的 PowerShell"
		}
		resolved = append(resolved, spec)
	}
	if len(resolved) > 0 {
		// 稳定排序：只对 PowerShell 按版本降序，其它模式保持路径优先级不动
		// （cmd / bash 没有"新版本更好"这回事，路径顺序才是正确判据）。
		if mode == ShellModePowerShell {
			sort.SliceStable(resolved, func(i, j int) bool {
				return resolved[i].Major() > resolved[j].Major()
			})
		}
		return resolved[0], nil
	}
	if firstErr == nil {
		firstErr = errors.New("没有找到候选可执行文件")
	}
	return ShellSpec{}, fmt.Errorf("%w：%s 模式在本机找不到可用的可执行文件（%v）", ErrShellNotAvailable, mode, firstErr)
}

// shellCandidates 返回该模式在本机可能的位置，按优先级排列。
func shellCandidates(mode ShellMode, deps ResolveDeps) []string {
	var out []string
	addLook := func(name string) {
		if p, err := deps.LookPath(name); err == nil && strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	addExists := func(p string) {
		if strings.TrimSpace(p) != "" && deps.Exists(p) {
			out = append(out, p)
		}
	}
	switch mode {
	case ShellModePowerShell:
		// pwsh 优先：默认 UTF-8、支持 && / ||，比 5.1 少一半坑。
		addLook("pwsh")
		addExists(joinShellPath(`C:\Program Files\PowerShell\7`, "pwsh.exe"))
		addExists(joinShellPath(`C:\Program Files\PowerShell\7-preview`, "pwsh.exe"))
		addLook("powershell")
		sysRoot := deps.Env("SystemRoot")
		if sysRoot == "" {
			sysRoot = `C:\Windows`
		}
		addExists(joinShellPath(sysRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"))
		// Store 别名放最后：能用，但 spawn 风险最高。
		if la := deps.Env("LOCALAPPDATA"); la != "" {
			addExists(joinShellPath(la, "Microsoft", "WindowsApps", "pwsh.exe"))
		}
	case ShellModeCmd:
		if comspec := deps.Env("COMSPEC"); comspec != "" {
			addExists(comspec)
		}
		sysRoot := deps.Env("SystemRoot")
		if sysRoot == "" {
			sysRoot = `C:\Windows`
		}
		addExists(joinShellPath(sysRoot, "System32", "cmd.exe"))
		addLook("cmd")
	case ShellModeBash:
		if deps.GOOS == "windows" {
			addLook("bash")
			// git 在 PATH 里但 bash 不在，这是最常见的现场形态（本机就是）：
			// E:\Git\cmd\git.exe 在 PATH，E:\Git\bin\bash.exe 不在。
			if deps.GitExecPath != nil {
				if execPath, err := deps.GitExecPath(); err == nil {
					if root := gitRootFromExecPath(execPath); root != "" {
						addExists(joinShellPath(root, "bin", "bash.exe"))
					}
				}
			}
			addExists(joinShellPath(`C:\Program Files\Git`, "bin", "bash.exe"))
			addExists(joinShellPath(`C:\Program Files (x86)\Git`, "bin", "bash.exe"))
		} else {
			addExists("/bin/bash")
			addExists("/usr/bin/bash")
			addExists("/bin/sh")
			addLook("bash")
		}
	}
	return out
}

// gitRootFromExecPath 从 `git --exec-path` 的输出反推 Git 安装根目录。
//
// 典型输出（Git for Windows）：
//
//	E:/Git/mingw64/libexec/git-core     → E:\Git
//	E:\Git\mingw64\libexec\git-core     → E:\Git
//
// 只要末尾是 <root>/(mingw64|mingw32)/libexec/git-core 就往上退三级。
func gitRootFromExecPath(execPath string) string {
	p := strings.TrimSpace(strings.ReplaceAll(execPath, `\`, "/"))
	p = strings.TrimRight(p, "/")
	for _, mid := range []string{"/mingw64/libexec/git-core", "/mingw32/libexec/git-core"} {
		if strings.HasSuffix(strings.ToLower(p), mid) {
			root := p[:len(p)-len(mid)]
			return strings.ReplaceAll(root, "/", `\`)
		}
	}
	return ""
}

// isStoreAlias 识别 WindowsApps 下的 MSIX 应用执行别名。
func isStoreAlias(p string) bool {
	return strings.Contains(strings.ToLower(strings.ReplaceAll(p, `\`, "/")), "/microsoft/windowsapps/")
}

// buildSpec 判定 Flavor。版本探测失败不影响可用性（能跑命令就够），
// 只影响契约里能不能写「支持 &&」。
func buildSpec(bin string, mode ShellMode, deps ResolveDeps) (ShellSpec, error) {
	version, err := deps.Probe(bin, mode)
	spec := ShellSpec{Mode: mode, Bin: bin, Version: version}
	switch mode {
	case ShellModeCmd:
		spec.Flavor = "cmd"
	case ShellModePowerShell:
		if isPwshName(bin) {
			spec.Flavor = "pwsh"
		} else {
			spec.Flavor = "windows-powershell"
		}
	case ShellModeBash:
		spec.Flavor = "bash"
	}
	if err != nil {
		return ShellSpec{}, fmt.Errorf("%w：%s 无法执行（%v）", ErrShellNotAvailable, bin, err)
	}
	return spec, nil
}

// isPwshName 用文件名判断是 PowerShell 7 还是 5.1。
// 不看路径含 7 的目录名——用户可能把 pwsh 装在任意位置。
//
// 先把反斜杠归一成正斜杠再取 Base：filepath.Base 在 Linux 上不把 `\` 当分隔符，
// 而这里处理的可能是「在 Linux 上推断的 Windows 路径」（测试、离线分析都走这条）。
func isPwshName(bin string) bool {
	norm := strings.ReplaceAll(bin, `\`, "/")
	base := strings.ToLower(norm[strings.LastIndex(norm, "/")+1:])
	return base == "pwsh.exe" || base == "pwsh"
}

// joinShellPath 拼接路径。若起点是 Windows 形态（含反斜杠）就用反斜杠拼，
// 否则用 filepath.Join——避免在 Linux 上把 `E:\Git` 拼成 `E:\Git/bin`。
func joinShellPath(base string, elems ...string) string {
	if strings.Contains(base, `\`) {
		out := strings.TrimRight(base, `\/`)
		for _, e := range elems {
			out += `\` + e
		}
		return out
	}
	return filepath.Join(append([]string{base}, elems...)...)
}
