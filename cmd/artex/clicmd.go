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

// 终端 HARNESS：artex cli。
//
// 与 Web 控制台并列的第二种入口，不是它的替代品：
//   - 绿色单文件：同一份 artex 二进制，不装依赖、不占端口、不起浏览器；
//   - 环境变量快捷启动：ARTEX_TARGET / ARTEX_INSTRUCTION / ARTEX_NONINTERACTIVE
//     直接给出一次任务，CI 与脚本不需要任何交互；
//   - 交互式 REPL：终端里输入的文字会作为 instruction 投给根 agent（与 Web 对话同语义）。
//
// 数据、LLM 配置、技能目录与 Web 控制台完全共用：默认用同一个 data 目录与 config.json，
// 所以「先配置一次、两种入口都能跑」成立。

const cliEnvTarget = "ARTEX_TARGET"
const cliEnvInstruction = "ARTEX_INSTRUCTION"
const cliEnvName = "ARTEX_TASK_NAME"
const cliEnvNonInteractive = "ARTEX_NONINTERACTIVE"

// cliTargets 是可重复的 -t/--target。
type cliTargets []string

func (t *cliTargets) String() string { return strings.Join(*t, ",") }

func (t *cliTargets) Set(v string) error {
	*t = append(*t, v)
	return nil
}

func runCLICommand(args []string) int {
	fs := flag.NewFlagSet("cli", flag.ContinueOnError)
	fs.SetOutput(os.Stderr) // 用法错误走 stderr，stdout 只留给任务输出

	var targets cliTargets
	fs.Var(&targets, "t", "授权目标（URL / 域名 / IP）；可重复，多个也可用逗号分隔")
	fs.Var(&targets, "target", "同 -t")
	instruction := fs.String("i", "", "补充要求（也可用 ARTEX_INSTRUCTION）")
	fs.StringVar(instruction, "instruction", "", "同 -i")
	name := fs.String("name", "", "任务名（也可用 ARTEX_TASK_NAME）；空则按目标自动生成")
	timeout := fs.Int("timeout", 0, "任务级超时（秒）；0 = 不限")
	dataDir := fs.String("data", filepath.Join(config.BaseDir(), "data"), "数据目录（默认与 Web 控制台共用）")
	proxy := fs.String("proxy", "", "流量录制代理地址（默认空 = 不启动代理；需要留痕时设为 127.0.0.1:8788）")
	headless := fs.Bool("n", false, "非交互：跑完自动汇总并退出（也可用 ARTEX_NONINTERACTIVE=1）")
	fs.BoolVar(headless, "non-interactive", false, "同 -n")
	fs.BoolVar(headless, "headless", false, "同 -n")
	interactive := fs.Bool("interactive", false, "即使 stdin 不是终端也开启行式 REPL")
	verbose := fs.Bool("verbose", false, "显示 thinking 流与后端日志路径")
	colorMode := fs.String("color", "auto", "颜色：auto|always|never")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `artex cli —— 终端 HARNESS（根 agent 编排，不依赖 Web 控制台）

用法:
  artex cli -t https://example.com -i "重点测试登录与越权"
  ARTEX_TARGET=https://example.com artex cli
  ARTEX_TARGET=https://example.com ARTEX_NONINTERACTIVE=1 artex cli

常用参数:
  -t, --target       授权目标，可重复（也可用 ARTEX_TARGET，逗号/空格分隔）
  -i, --instruction  补充要求（也可用 ARTEX_INSTRUCTION）
  -n, --non-interactive  跑完自动汇总并退出
      --name         任务名（默认按目标生成）
      --timeout      任务级超时秒数（0=不限）
      --data         数据目录（默认与 Web 控制台共用）
      --proxy        流量录制代理地址（默认不启动）
      --verbose      显示 thinking 流
      --color        auto|always|never

环境变量:
  ARTEX_TARGET / ARTEX_INSTRUCTION / ARTEX_TASK_NAME / ARTEX_NONINTERACTIVE
  LLM（二选一，与 Web 控制台共用持久化配置）:
    ANTHROPIC_API_KEY 或 OPENAI_API_KEY + ARTEX_LLM_MODEL
    可选 ARTEX_LLM_PROVIDER / ARTEX_LLM_BASE_URL / ARTEX_LLM_PROXY

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

	if len(targets) == 0 {
		targets = append(targets, strings.Split(os.Getenv(cliEnvTarget), ",")...)
	}
	if strings.TrimSpace(*instruction) == "" {
		*instruction = strings.TrimSpace(os.Getenv(cliEnvInstruction))
	}
	if strings.TrimSpace(*name) == "" {
		*name = strings.TrimSpace(os.Getenv(cliEnvName))
	}
	if !*headless && isTruthy(os.Getenv(cliEnvNonInteractive)) {
		*headless = true
	}
	if *timeout < 0 {
		*timeout = 0
	}

	// 没有终端就别假装能交互：CI、nohup、管道一律按非交互跑（与 Strix 的口径一致）。
	terminal := stdinIsTerminal()
	if *headless {
		*interactive = false
	} else if !terminal && !*interactive {
		*interactive = false
	} else if terminal {
		*interactive = true
	}

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
		fmt.Fprintf(os.Stderr, "artex cli: 启动失败：%v\n", err)
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
			fmt.Fprintln(os.Stderr, "artex cli: 已中断")
			return 1
		}
		fmt.Fprintf(os.Stderr, "artex cli: %v\n", runErr)
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
// 需要时用 --color always 或 ARTEX_CLI_COLOR=1 显式打开。
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
		if strings.TrimSpace(os.Getenv("ARTEX_CLI_COLOR")) == "1" {
			return true
		}
		if runtime.GOOS == "windows" {
			return false
		}
		return stdoutIsTerminal()
	default:
		return false
	}
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
