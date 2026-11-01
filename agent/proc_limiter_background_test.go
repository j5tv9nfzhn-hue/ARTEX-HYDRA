package agent

// 后台 Shell 进程配额的回归测试。
//
// 闸门的性质是「同时存活的后台进程不超过 N 个」，而「不超过」在 Go 测试里不能直接
// 断言：一个串行正确、并行也正确的实现，单次调用看起来一模一样。所以这里的断言全部
// 建立在「几个持有者真的同时待在临界区里」上——进临界区就记账、出来就减账，全程记
// 最大值；再加一条「额度用尽时明确报错」的断言，防止实现退化成静默排队。

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fillBackgroundQuota 占满后台名额，返回释放函数。
func fillBackgroundQuota(t *testing.T) []func() {
	t.Helper()
	quota := maxConcurrentBackgroundProcs()
	held := make([]func(), 0, quota)
	for i := 0; i < quota; i++ {
		release, err := AcquireBackgroundProc(context.Background())
		if err != nil {
			for _, r := range held {
				r()
			}
			t.Fatalf("占第 %d 个后台名额失败：%v", i, err)
		}
		held = append(held, release)
	}
	return held
}

// TestBackgroundProcQuotaBoundsConcurrency 断言同时存活的后台进程不超过配额。
//
// 变异对照：把 AcquireBackgroundProc 里的 select 去掉（等于不设限）会被这条抓住——
// 并发数会直接冲到 worker 总数。
func TestBackgroundProcQuotaBoundsConcurrency(t *testing.T) {
	quota := maxConcurrentBackgroundProcs()
	if quota < 1 {
		t.Fatalf("后台配额至少为 1，实得 %d", quota)
	}
	const workers = 12

	var cur, peak atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			release, err := AcquireBackgroundProc(ctx)
			if err != nil {
				t.Errorf("取后台名额失败：%v", err)
				return
			}
			defer release()

			n := cur.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			// 持有一段时间，让后面的 worker 有机会挤进来——不这样就量不到并发。
			time.Sleep(50 * time.Millisecond)
			cur.Add(-1)
		}()
	}
	wg.Wait()

	got := peak.Load()
	if got > int64(quota) {
		t.Fatalf("同时存活的后台进程达到 %d 个，超过配额 %d", got, quota)
	}
	if got < int64(quota) {
		t.Fatalf("同时存活的后台进程只达到 %d 个，配额 %d 没用满（闸门可能过紧）", got, quota)
	}
}

// TestBackgroundProcAcquireRespectsContext 断言名额用尽时不会无限期阻塞。
func TestBackgroundProcAcquireRespectsContext(t *testing.T) {
	held := fillBackgroundQuota(t)
	defer func() {
		for _, r := range held {
			r()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := AcquireBackgroundProc(ctx); err == nil {
		t.Fatal("名额用尽时应当拿不到名额，而不是无限期阻塞")
	}
}

// TestRunOwnBackgroundRefusesWhenQuotaFull 断言名额用尽时明确报错。
//
// 静默排队在这里是错的：后台进程不随本轮对话结束而中断，排队会让模型以为命令已经
// 开跑，转头又起一条重复的。
func TestRunOwnBackgroundRefusesWhenQuotaFull(t *testing.T) {
	held := fillBackgroundQuota(t)
	defer func() {
		for _, r := range held {
			r()
		}
	}()

	// 执行器给一个不存在的路径也无妨：名额满的时候根本走不到启动那一步。
	withSpec(t, ShellSpec{Mode: ShellModeBash, Bin: "definitely-not-a-real-shell"}, func() {
		// 等待上限给 100ms，让测试不用等满生产环境的 3 秒。
		res := runOwnBackgroundWait(context.Background(), "echo hi", "", nil, 100*time.Millisecond)
		if !res.IsError {
			t.Fatalf("名额用尽时应当报错，实得成功结果：%s", resultText(res))
		}
		if !strings.Contains(resultText(res), "名额已满") {
			t.Fatalf("报错文案应当说明是名额用尽，实得：%s", resultText(res))
		}
	})
}

// TestRunOwnBackgroundReleasesSlotOnExit 断言后台进程退出后名额被归还。
//
// 这条防的是「名额泄漏」：一旦回收 goroutine 没跑到，后台名额会被永久占光，之后
// 所有后台命令都起不来——症状是「跑着跑着突然全都不行了」，极难定位。
func TestRunOwnBackgroundReleasesSlotOnExit(t *testing.T) {
	quota := maxConcurrentBackgroundProcs()
	// 先占满，再放掉 quota-1 个，只留最后一个给后台进程——只有这样，「后台拿到的名额
	// 有没有还回来」才能被区分出来：名额没还，池子里就永远少一个。
	held := fillBackgroundQuota(t)
	if len(held) != quota {
		for _, r := range held {
			r()
		}
		t.Fatalf("应当正好占掉 %d 个名额，实得 %d", quota, len(held))
	}
	for _, r := range held[:quota-1] {
		r()
	}
	defer func() {
		for _, r := range held {
			r()
		}
	}()

	// 用测试二进制自身当后台进程：它收到未定义的 "-c" 参数会在解析 flag 时直接退出，
	// 不会递归跑测试。
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	withSpec(t, ShellSpec{Mode: ShellModeBash, Bin: self}, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res := runOwnBackgroundWait(ctx, "true", "", nil, time.Second)
		if res.IsError {
			t.Fatalf("后台启动不该失败：%s", resultText(res))
		}
	})

	// 后台起来之后，把我手上剩下的那一个也还掉，这样池子里就只剩「后台那个名额」说话。
	for _, r := range held[quota-1:] {
		r()
	}

	// 后台进程退出后，名额应当回到池子里：现在应当能一次拿到 quota 个。
	// 回收 goroutine 晚一拍是正常的，所以轮询到超时为止。
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := 0
		var acquired []func()
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			release, err := AcquireBackgroundProc(ctx)
			cancel()
			if err != nil {
				break
			}
			acquired = append(acquired, release)
			got++
		}
		for _, r := range acquired {
			r()
		}
		if got == quota {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("后台进程退出后名额没有归还：只拿回 %d 个，期望 %d 个（名额泄漏）", got, quota)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestBackgroundProcDoesNotConsumeGlobalSlot 断言后台进程不吃全局外部进程令牌。
//
// 这是整个设计的关键：后台进程是长跑型的，如果它占着全局令牌，模型按契约把长任务
// 转后台之后前台命令就会被饿死（全局额度最多 8 个，被后台占满后一条前台命令都发
// 不出去）。反过来，前台令牌也不该替后台兜底——那是两个不同的闸门。
func TestBackgroundProcDoesNotConsumeGlobalSlot(t *testing.T) {
	held := fillBackgroundQuota(t)
	defer func() {
		for _, r := range held {
			r()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	release, err := AcquireExternalProc(ctx)
	if err != nil {
		t.Fatalf("后台进程不应占用全局外部进程令牌：%v", err)
	}
	release()
}
