package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/boot"
	"github.com/Autumn-27/artex/config"
	"github.com/Autumn-27/artex/server"
)

// 终端 HARNESS：artex cli（用户入口是 artex，由 artex-launcher 转发到本命令）。
//
// 与 Web 控制台并列的第二种入口，不是它的替代品：
//   - 绿色单文件：同一份 artex 二进制，不装依赖、不占端口、不起浏览器；
//   - 配置一律来自 Web 控制台：LLM、Agent、预算等写入 artex.db，CLI 实时读取，
//     Web 侧修改后本会话下一回合自动生效；
//   - 交互式 REPL：终端里输入的文字会作为 instruction 投给根 agent（与 Web 对话同语义）。
//
// 两条硬约束：
//   - 只允许在安装目录内启动（防止 artex.db / credential.key 散落到别处）；
//   - 不读取任何环境变量快捷启动（ARTEX_TARGET 等已废弃，配置只有 Web 一个来源）。

func runCLICommand(args []string) int {
	// 安装目录守卫必须放在一切 I/O 之前：不建目录、不建库、不留半个文件。
	if err := ensureInstallRoot(); err != nil {
		fmt.Fprintf(os.Stderr, "artex: %v\n", err)
		return 2
	}

	fs := flag.NewFlagSet("cli", flag.ContinueOnError)
	fs.SetOutput(os.Stderr) // 用法错误走 stderr，stdout 只留给任务输出

	var targets cliTargets
	fs.Var(&targets, "t", "授权目标（URL / 域名 / IP）；可重复，多个也可用逗号分隔")
	fs.Var(&targets, "target", "同 -t")
	instruction := fs.String("i", "", "补充要求（本次任务给根 agent 的额外说明）")
	fs.StringVar(instruction, "instruction", "", "同 -i")
	name := fs.String("name", "", "任务名；空则按目标自动生成")
	timeout := fs.Int("timeout", 0, "任务级超时（秒）；0 = 不限")
	dataDir := fs.String("data", filepath.Join(config.BaseDir(), "data"), "数据目录（默认与 Web 控制台共用）")
	proxy := fs.String("proxy", "", "流量录制代理地址（默认空 = 不启动代理；需要留痕时设为 127.0.0.1:8788）")
	headless := fs.Bool("n", false, "非交互：跑完自动汇总并退出")
	fs.BoolVar(headless, "non-interactive", false, "同 -n")
	interactive := fs.Bool("interactive", false, "即使 stdin 不是终端也开启行式 REPL")
	verbose := fs.Bool("verbose", false, "显示 thinking 流与后端日志路径")
	colorMode := fs.String("color", "auto", "颜色：auto|always|never")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `artex —— 终端 HARNESS（根 agent 编排，不依赖 Web 控制台）

用法（必须在安装目录内运行）:
  artex
  artex cli -t https://example.com -i "重点测试登录与越权"

常用参数:
  -t, --target       授权目标，可重复（逗号/空格分隔）
  -i, --instruction  补充要求
  -n, --non-interactive  跑完自动汇总并退出
      --name         任务名（默认按目标生成）
      --timeout      任务级超时秒数（0=不限）
      --data         数据目录（默认与 Web 控制台共用）
      --proxy        流量录制代理地址（默认不启动）
      --verbose      显示 thinking 流
      --color        auto|always|never

配置:
  LLM、Agent、Skill、预算等一律在 Web 控制台配置（系统 → LLM），
  与 CLI 实时共用 artex.db；Web 侧修改后 CLI 下一回合自动生效。
  终端 HARNESS 不读取任何环境变量，也只在安装目录内允许启动。

交互命令:
  /status  /findings  /pause  /verbose  /exit
  直接输入文字 = 给根 agent 下指令（不会另起一套对话上下文）

退出码: 0 完成且无漏洞 / 2 完成任务但有漏洞 / 1 失败或参数错误
`)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *timeout < 0 {
		*timeout = 0
	}

	// 没有终端就别假装能交互：CI、nohup、管道一律按非交互跑（与 Strix 的口径一致）。
	terminal := stdinIsTerminal()
	*interactive = !*headless && (terminal || *interactive)

	logPath := setupCLILog(*dataDir, *verbose)
	if logPath != "" && *verbose {
		fmt.Fprintf(os.Stderr, "后端日志: %s\n", logPath)
	}

	// 与 Web 控制台同一套启动骨架：凭据主密钥、进程树收口、开库、装配 server。
	// Headless 只跳过监听器，不跳过任何数据初始化。
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancelAll := boot.ShutdownContext(sigCtx)

	app, err := boot.Prepare(ctx, boot.Options{
		Version:    version,
		DataDir:    *dataDir,
		ProxyAddr:  *proxy,
		SkillDir:   config.SkillDir(),
		ExeDir:     config.BaseDir(),
		ListenAddr: "",
		Headless:   true,
	})
	if err != nil {
		cancelAll(agent.AbortShutdown)
		fmt.Fprintf(os.Stderr, "artex: 启动失败：%v\n", err)
		if logPath != "" {
			fmt.Fprintf(os.Stderr, "详细日志: %s\n", logPath)
		}
		return 1
	}

	srv := app.Server()
	useColor := cliColorEnabled(*colorMode)
	res, runErr := srv.RunCLI(ctx, server.CLIOptions{
		Targets:        targets,
		Instruction:    *instruction,
		Name:           *name,
		TimeoutSeconds: *timeout,
		Interactive:    *interactive,
		Verbose:        *verbose,
		Color:          useColor,
		In:             os.Stdin,
		Out:            os.Stdout,
	})

	// 停机顺序与 Web 侧一致：先让在跑回合收尾，再关库。
	cancelAll(agent.AbortShutdown)
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	_ = app.Shutdown(shutdownCtx)
	cancelShutdown()

	if runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			fmt.Fprintln(os.Stderr, "artex: 已中断")
			return 1
		}
		fmt.Fprintf(os.Stderr, "artex: %v\n", runErr)
		return 1
	}
	switch {
	case res.Status == "failed" || res.Status == "timeout":
		return 1
	case res.HasFindings():
		return 2
	default:
		return 0
	}
}

// ensureInstallRoot 落实「只允许在安装目录内启动终端 HARNESS」的约束：
// 防止从任意目录启动时把 artex.db、credential.key 等散落文件建到别处，
// 也保证 CLI 与 Web 控制台读写的是同一份数据。版本化安装下 BaseDir
// 已被入口转发进程指回安装根，这里统一与它比较。
func ensureInstallRoot() error {
	base := strings.TrimSpace(config.BaseDir())
	if base == "." {
		// `go run` 的临时二进制：BaseDir 回退 CWD，属于开发场景，不拦。
		return nil
	}
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		return nil
	}
	if sameDir(cwdAbs, baseAbs) {
		return nil
	}
	return fmt.Errorf("终端 HARNESS 只能在安装目录内启动：请先 cd 到 %s 再运行 artex（当前目录 %s）", baseAbs, cwdAbs)
}

// sameDir 优先用文件系统身份判断（别名/大小写/软链都算同一目录），
// stat 失败时退回字符串比较（Windows 忽略大小写）。
func sameDir(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	if errA == nil && errB == nil {
		return os.SameFile(fa, fb)
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// setupCLILog 把后端日志写进 data/cli.log，避免 engine 的启动/调度日志
// 与终端对话内容互相插行。stdout 始终只留任务输出。
func setupCLILog(dataDir string, verbose bool) string {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dataDir, "cli.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return ""
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags)
	return path
}

// stdinIsTerminal 判断 stdin 是否接在终端上。管道/重定向时返回 false。
func stdinIsTerminal() bool {
	st, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// stdoutIsTerminal 用于颜色判断：颜色写在 stdout 上，与 stdin 是否可交互无关。
func stdoutIsTerminal() bool {
	st, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

// cliColorEnabled 默认只在真正的终端上上色；NO_COLOR 永远优先关闭。
// Windows 默认不上色：老控制台不一定开了 VT 处理，转义序列会变成乱码；
// 需要时用 --color always 显式打开。
func cliColorEnabled(mode string) bool {
	if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "never":
		return false
	case "always":
		return true
	case "auto":
		if runtime.GOOS == "windows" {
			return false
		}
		return stdoutIsTerminal()
	default:
		return false
	}
}

// cliTargets 是可重复的 -t/--target。
type cliTargets []string

func (t *cliTargets) String() string { return strings.Join(*t, ",") }

func (t *cliTargets) Set(v string) error {
	*t = append(*t, v)
	return nil
}
