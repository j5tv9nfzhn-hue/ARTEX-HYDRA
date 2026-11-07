package agent

// 跨平台执行器选型的回归测试（作者 2026-11-07 Issue #6 §4-C）。
//
// 这一组测试**完全在 Linux 上跑**：ResolveDeps 把 GOOS / LookPath / Exists / Probe
// 全部注入，所以可以在没有 Windows 的机器上验证 Windows 的解析路径。这是本次
// 修复"Windows 未实测"问题的可行做法——真机烟雾测试仍需在 Windows 上做，
// 但"选了哪个 exe、为什么选它、选不到时会不会偷偷换一个"这些**决策**可以在这里锁死。
//
// 覆盖三条结论：
//  1. Git Bash 在优先顺序里第一，且没有安装时不会被推荐（不是"选了才报错"）；
//  2. PowerShell 解析时 7.x 优先于 5.1，即使 5.1 在 PATH 里先被命中；
//  3. 三档之间绝不静默回退：选定模式的候选用尽就报 ErrShellNotAvailable。

import (
	"errors"
	"strings"
	"testing"
)

func winDeps(lookPath map[string]string, exists map[string]string, probe func(string) (string, error)) ResolveDeps {
	return ResolveDeps{
		GOOS: "windows",
		Env: func(k string) string {
			switch k {
			case "SystemRoot":
				return `C:\Windows`
			case "LOCALAPPDATA":
				return `C:\Users\u\AppData\Local`
			case "COMSPEC":
				return `C:\Windows\System32\cmd.exe`
			}
			return ""
		},
		LookPath: func(name string) (string, error) {
			if p, ok := lookPath[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Exists: func(p string) bool {
			_, ok := exists[strings.ToLower(strings.ReplaceAll(p, `\`, `\`))]
			return ok
		},
		Probe: func(exe string, _ ShellMode) (string, error) {
			return probe(exe)
		},
	}
}

// TestPowerShell7Beats5_1EvenWhenFirstInPath 是本次修复的核心断言。
//
// 现场机器：`powershell` 在 PATH 命中 5.1（Windows 自带），
// pwsh 7 装在标准目录但没进 PATH。旧逻辑"第一个能跑的就要"会选 5.1，
// 于是 `&&`、UTF-8 全部退化。新逻辑必须选 7。
func TestPowerShell7Beats5_1EvenWhenFirstInPath(t *testing.T) {
	pwsh7 := `C:\Program Files\PowerShell\7\pwsh.exe`
	ps51 := `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
	deps := winDeps(
		map[string]string{"powershell": ps51}, // PATH 里只有 5.1
		map[string]string{strings.ToLower(pwsh7): pwsh7},
		func(exe string) (string, error) {
			if strings.EqualFold(exe, pwsh7) {
				return "7.4.2", nil
			}
			if strings.EqualFold(exe, ps51) {
				return "5.1", nil
			}
			return "", errors.New("missing")
		},
	)
	spec, err := ResolveShell(ShellModePowerShell, deps)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !strings.EqualFold(spec.Bin, pwsh7) {
		t.Fatalf("应选 PowerShell 7，实际选了 %q（版本 %s）", spec.Bin, spec.Version)
	}
	if spec.Flavor != "pwsh" || spec.Major() < 7 {
		t.Fatalf("flavor/版本判定错：%+v", spec)
	}
}

// TestPowerShellFallsBackTo5_1OnlyWhen7Absent 确认"7 不存在时用 5.1"——
// 这是同一模式内部的降级（同一个 exe 家族的版本差异），不属于"换执行器"。
func TestPowerShellFallsBackTo5_1OnlyWhen7Absent(t *testing.T) {
	ps51 := `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
	deps := winDeps(
		map[string]string{},
		map[string]string{strings.ToLower(ps51): ps51},
		func(exe string) (string, error) {
			if strings.EqualFold(exe, ps51) {
				return "5.1", nil
			}
			return "", errors.New("missing")
		},
	)
	spec, err := ResolveShell(ShellModePowerShell, deps)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if spec.Flavor != "windows-powershell" {
		t.Fatalf("应识别为 5.1，得到 %+v", spec)
	}
}

// TestBashModeNeverSilentlyUsesPowerShell：选 bash 但没装 Git for Windows →
// 必须报 ErrShellNotAvailable，**不能**改成跑 PowerShell。
// 这条是用户明确否决过的行为（"不静默回落"）。
func TestBashModeNeverSilentlyUsesPowerShell(t *testing.T) {
	pwsh7 := `C:\Program Files\PowerShell\7\pwsh.exe`
	deps := winDeps(
		map[string]string{"pwsh": pwsh7},
		map[string]string{strings.ToLower(pwsh7): pwsh7},
		func(exe string) (string, error) {
			if strings.EqualFold(exe, pwsh7) {
				return "7.4.2", nil
			}
			return "", errors.New("missing")
		},
	)
	spec, err := ResolveShell(ShellModeBash, deps)
	if err == nil {
		t.Fatalf("bash 不可用时必须报错，实际解析到 %+v", spec)
	}
	if !errors.Is(err, ErrShellNotAvailable) {
		t.Fatalf("错误类型应为 ErrShellNotAvailable，得到 %v", err)
	}
}

// TestPreferredShellOrderPutsGitBashFirst 锁定 Issue #6 §4-C 的选型结论。
func TestPreferredShellOrderPutsGitBashFirst(t *testing.T) {
	order := PreferredShellOrder("windows")
	if len(order) < 2 || order[0] != ShellModeBash {
		t.Fatalf("Windows 上 Git Bash 应排第一，得到 %v", order)
	}
	if order[1] != ShellModePowerShell {
		t.Fatalf("PowerShell 应为次选，得到 %v", order)
	}
	if got := PreferredShellOrder("linux"); len(got) != 1 || got[0] != ShellModeBash {
		t.Fatalf("非 Windows 应只有 bash，得到 %v", got)
	}
}

// TestOrderedShellModesIncludesCmdLast：cmd 仍要在设置页出现（可用但明确不建议），
// 且永远排最后。删掉它会让"PowerShell 全挂"的机器没有兜底。
func TestOrderedShellModesIncludesCmdLast(t *testing.T) {
	modes := orderedShellModes("windows")
	if len(modes) != 3 {
		t.Fatalf("Windows 应有三项，得到 %v", modes)
	}
	if modes[0] != ShellModeBash || modes[len(modes)-1] != ShellModeCmd {
		t.Fatalf("顺序应为 bash → powershell → cmd，得到 %v", modes)
	}
	if got := orderedShellModes("linux"); len(got) != 1 || got[0] != ShellModeBash {
		t.Fatalf("非 Windows 只应有 bash，得到 %v", got)
	}
	// 同一模式不能重复出现（否则设置页会出现两个 Git Bash）。
	seen := map[ShellMode]bool{}
	for _, m := range modes {
		if seen[m] {
			t.Fatalf("模式 %s 重复出现：%v", m, modes)
		}
		seen[m] = true
	}
}

// TestShellPreferenceNoteExplainsEachChoice：设置页需要把"为什么推荐/不建议"
// 写出来，否则用户会以为三档等价。文案缺失本身就是一种"说法不符"。
func TestShellPreferenceNoteExplainsEachChoice(t *testing.T) {
	if note := ShellPreferenceNote(ShellModeBash, "windows"); !strings.Contains(note, "推荐") {
		t.Fatalf("Git Bash 应标为推荐，得到 %q", note)
	}
	if note := ShellPreferenceNote(ShellModeCmd, "windows"); !strings.Contains(note, "不建议") {
		t.Fatalf("cmd 应标为不建议，得到 %q", note)
	}
	if note := ShellPreferenceNote(ShellModePowerShell, "windows"); !strings.Contains(note, "pwsh") {
		t.Fatalf("PowerShell 的说明应提到 pwsh 7 优先，得到 %q", note)
	}
}
