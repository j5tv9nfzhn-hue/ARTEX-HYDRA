// Command artex-launcher 是发行包里的用户入口 artex / artex.exe。
//
// 它只做一件事：把命令转发给同目录的 artex-rea 内核。
//
//	artex              → artex-rea cli        （终端 HARNESS，默认行为）
//	artex cli ...      → artex-rea cli ...    （同上，带参数）
//	artex web ...      → artex-rea ...        （Web 控制台，剥掉 web 子命令）
//	artex mcp ...      → artex-rea mcp ...    （外部 MCP 接入）
//	artex -addr ...    → artex-rea -addr ...  （其余参数原样转发）
//
// 内核负责真正的启动逻辑（含安装目录守卫与自更新转发），这个壳故意保持
// 极小、零依赖：它必须先于任何数据库/网络初始化运行，出错时给出一句人话。
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
		// 直接运行 artex = 启动终端 HARNESS。
		args = []string{"cli"}
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

  artex            启动终端 HARNESS（交互式，配置来自 Web 控制台）
  artex cli ...    同上，可带参数（artex cli -h 查看全部参数）
  artex web ...    启动 Web 控制台（LLM / Agent / Skill / 预算都在这里配置）
  artex mcp ...    外部 MCP 接入

注意：终端 HARNESS 只允许在安装目录内运行；从其他目录启动会被拒绝。
`)
}
