package agent

// 外部子进程并发闸门：全局令牌（AcquireExternalProc）与后台配额（AcquireBackgroundProc）。
//
// 这个文件管的是「宿主机上同时跑着多少个外部子进程」，不是「agent 能发多少条命令」。
// 两条边界必须写死，否则下一个人只能靠猜：
//
// 受全局令牌约束（容量 maxConcurrentExternalProcs = max(2, CPU/2)，上限 8）：
//   - runManagedExternalCommand：外部编译型工具直调（nmap、nuclei、ffuf、sqlmap 等）。
//   - 前台 Shell 执行：Bash 工具入口（shell_builtins.go）在整条执行链上持有令牌，
//     因此一次性 spawn（shell_exec.go 的 runOneShot）与常驻会话租用都被它盖住。
//
// 刻意不受全局令牌约束的（改这几处前先读完上面的理由）：
//   - 常驻 PowerShell 会话宿主进程（shell_session.go 的 newPSSession）：它要活几分钟到
//     几十分钟，而令牌容量最多 8。让它长期占位会把前台 Bash 饿死——而契约恰恰要求
//     模型把长任务转后台，于是「起 2 个后台任务 → 再也跑不了任何前台命令」会是常态。
//   - 一次性探测（shell_detect.go 的 git --exec-path、shell_probe.go 的版本探测）：
//     探一次就退出，不构成负载。
//   - 外部复核 helper（verification_helper.go）：它自带独立限流槽 verificationHelperSlots。
//   - SDK 托管的后台任务（Windows + PowerShell 时，后台调用交给 norma 的 TaskManager，
//     见 shell_builtins.go 的 shellBackgroundDelegable）：ARTEX 拿不到它的生命周期，
//     没法在这里计数，并发由 SDK 侧负责。本文件的配额只管 ARTEX 自己起的后台进程。
//
// 后台 Shell 走另一把闸门（AcquireBackgroundProc，容量 = 全局额度的一半）：后台进程
// 天生长跑型，塞进全局桶会饿死前台，放到桶外又让模型能无限起后台进程。独立配额两头
// 都占：前台不受影响，后台存活数有上限；额度用尽时明确报错，不静默排队。
import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

var (
	extProcSemOnce sync.Once
	extProcSem     chan struct{}

	bgProcSemOnce sync.Once
	bgProcSem     chan struct{}
)

// maxConcurrentExternalProcs 按报告规约将跨 Worker 全局并发外部子进程槽位数限制为
// max(2, runtime.NumCPU()/2)（4 核机器上最多同时运行 2 个重型外部子进程），
// 防止多个 Worker 同时拉起外部工具或 Shell 子进程把宿主机 CPU 打满。
func maxConcurrentExternalProcs() int {
	n := runtime.NumCPU() / 2
	if n < 2 {
		return 2
	}
	if n > 8 {
		return 8
	}
	return n
}

func getExternalProcSem() chan struct{} {
	extProcSemOnce.Do(func() {
		extProcSem = make(chan struct{}, maxConcurrentExternalProcs())
	})
	return extProcSem
}

// AcquireExternalProc 获取一个全局外部子进程执行令牌，返回释放函数。
func AcquireExternalProc(ctx context.Context) (func(), error) {
	sem := getExternalProcSem()
	select {
	case sem <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-sem })
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// maxConcurrentBackgroundProcs 是同时存活的后台 Shell 进程数上限。
//
// 取全局额度的一半：后台进程是长跑型的（契约要求长任务必须转后台），给太少会逼着
// 模型把扫描串起来，给太多就等于没闸门。
func maxConcurrentBackgroundProcs() int {
	n := maxConcurrentExternalProcs() / 2
	if n < 1 {
		n = 1
	}
	return n
}

func getBackgroundProcSem() chan struct{} {
	bgProcSemOnce.Do(func() {
		bgProcSem = make(chan struct{}, maxConcurrentBackgroundProcs())
	})
	return bgProcSem
}

// AcquireBackgroundProc 获取一个后台 Shell 进程名额，返回释放函数。
//
// 与 AcquireExternalProc 的区别：名额按「后台进程还活着」计，不是按「命令在跑」计。
// 所以释放函数必须活到后台进程退出为止（见 shell_bg.go 的回收 goroutine），不能像
// 前台那样 defer 掉——早还了名额就等于没限。
func AcquireBackgroundProc(ctx context.Context) (func(), error) {
	sem := getBackgroundProcSem()
	select {
	case sem <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() { <-sem })
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// runManagedExternalCommand 直接通过操作系统的进程创建原语（Windows 下直接 CreateProcess，
// 完全绕过 powershell.exe / cmd.exe）启动外部编译型工具（如 nmap、nuclei、ffuf、sqlmap、curl.exe），
// 受跨 Worker 全局外部进程信号量节流，并配置 WaitDelay 防止孙进程继承管道句柄导致挂死。
func runManagedExternalCommand(ctx context.Context, binPath string, args []string, workDir string, extraEnv []string, timeout time.Duration) (output string, exitCode int, err error) {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	release, err := AcquireExternalProc(runCtx)
	if err != nil {
		return "", -1, err
	}
	defer release()

	cmd := exec.CommandContext(runCtx, binPath, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	configureExternalProc(cmd)
	cmd.WaitDelay = 3 * time.Second
	cmd.Cancel = func() error {
		return terminateExternalProc(cmd)
	}

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	runErr := cmd.Run()
	outStr := clipUTF8(buf.String(), 64*1024)
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		outStr += "\n... [命令执行超时已终止] ..."
		return outStr, 124, nil
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return outStr, exitErr.ExitCode(), nil
		}
		return outStr, 1, runErr
	}
	return outStr, 0, nil
}
