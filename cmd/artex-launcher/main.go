// Command artex-launcher 是发行包里的用户入口 artex / artex.exe。
//
// 它只做一件事：把命令转发给同目录的 artex-rea 内核。
//
//	artex              → artex-rea            （Web 控制台，默认行为）
//	artex web ...      → artex-rea ...        （同上，剥掉 web 子命令）
//	artex mcp ...      → artex-rea mcp ...    （外部 MCP 接入）
//	artex -addr ...    → artex-rea -addr ...  （其余参数原样转发）
//
// 内核负责真正的启动逻辑（含安装目录守卫与自更新转发），这个壳故意保持
// 极小、零依赖：它必须先于任何数据库/网络初始化运行，出错时给出一句人话。
//
// 终端 TUI 已归档到 _archive/tui-harness/，本壳不再有 tui 子命令路由。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	os.Exit(run())
}

func run() int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "artex: 无法定位自身：%v\n", err)
		return 1
	}
	dir := filepath.Dir(exe)
	kernel := filepath.Join(dir, "artex-rea"+ext())
	if _, err := os.Stat(kernel); err != nil {
		fmt.Fprintf(os.Stderr, "artex: 找不到内核程序 %s\n", kernel)
		fmt.Fprintln(os.Stderr, "artex: 请保持 artex 与 artex-rea 在同一目录（发行包默认如此）")
		return 1
	}

	args := os.Args[1:]
	switch {
	case len(args) == 0:
		// 直接运行 artex = 启动 Web 控制台。内核不带子命令时的默认行为就是起服务，
		// 这里不需要补任何参数。
	case args[0] == "tui":
		// 终端 TUI 已归档。静默按默认行为起 Web 控制台会让用户以为 tui 还能用，
		// 所以明确告知归档位置，再剥掉这个子命令继续走默认路径。
		fmt.Fprintln(os.Stderr, "artex: 终端 TUI 已归档到 _archive/tui-harness/，暂不参与构建；本次启动 Web 控制台。")
		args = args[1:]
	case args[0] == "web":
		// artex web = Web 控制台；内核不带子命令时默认起服务，剥掉 web 即可。
		args = args[1:]
	case args[0] == "help" || args[0] == "-h" || args[0] == "--help":
		usage()
		return 0
	}

	cmd := exec.Command(kernel, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "artex: 启动失败：%v\n", err)
		return 1
	}
	return 0
}

func asExitError(err error, target **exec.ExitError) bool {
	ee, ok := err.(*exec.ExitError)
	if ok {
		*target = ee
	}
	return ok
}

func ext() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func usage() {
	fmt.Print(`artex —— ARTEX-HYDRA 用户入口

  artex            启动 Web 控制台（默认行为）
  artex web ...    同上，可带参数
  artex mcp ...    外部 MCP 接入

配置（LLM / Agent / Skill / 预算）全部在 Web 控制台完成。
终端 TUI 已归档到 _archive/tui-harness/，暂不参与构建。
`)
}
