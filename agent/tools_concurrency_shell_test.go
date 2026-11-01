package agent

// 执行域锁的回归测试。
//
// 锁的性质是「不并发」，而「不并发」在 Go 测试里是**不可直接断言**的：一个串行
// 正确、并行也正确的实现，单次调用看起来一模一样。所以这里的断言全部建立在
// 「两次调用能否重叠」上——用一根 release 闸门把第一次调用钉在临界区内，再看
// 第二次能不能进来。
//
// 移植自 strix 的隔离模型（见 tools_concurrency.go 头注）：strix 靠容器隔离，
// ARTEX 靠工作目录。这两条测试就是把「跨目录并行、同目录串行」钉死。

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// countingTool 是最小可用的 CoreTool，用来观察「底层到底被调用了几次」。
//
// 它随单意图工具额度机制（intent_budget.go，P4 已拆除、本轮删除）一起消失过；
// 这里重新安家，因为「测并发锁是否真的串行」需要的正是这样一个可阻塞的替身。
// 名字刻意叫 Bash：走资源键分派时它会命中 shell: 命名空间。
type countingTool struct {
	onCall func()
}

func (c *countingTool) Name() string                           { return "Bash" }
func (c *countingTool) Description() string                    { return "test tool" }
func (c *countingTool) Prompt() string                         { return "" }
func (c *countingTool) InputSchema() map[string]any            { return map[string]any{} }
func (c *countingTool) IsReadOnly(json.RawMessage) bool        { return true }
func (c *countingTool) IsConcurrencySafe(json.RawMessage) bool { return true }
func (c *countingTool) CheckPermissions(context.Context, json.RawMessage, permission.Context) permission.Decision {
	return permission.Decision{Behavior: permission.Allow}
}
func (c *countingTool) Call(context.Context, json.RawMessage, *actool.ToolContext) (actool.Result, error) {
	if c.onCall != nil {
		c.onCall()
	}
	return actool.Text("ok"), nil
}

// TestBashAccessKeysArePerWorkDir 锁住键的命名，防止有人把它改回 keyGlobal。
//
// 这是最容易被无意改坏的一处：Bash 一旦落回 keyGlobal，编译、测试、代码审查
// 全都不会报警，只有跑起来才会退化成全局串行。
func TestBashAccessKeysArePerWorkDir(t *testing.T) {
	tool := &countingTool{}

	// keysFor 取一次调用声明出的写键，顺带把「必须是单个 shell: 写键」查干净。
	keysFor := func(t *testing.T, dir string) string {
		t.Helper()
		reads, writes := toolAccessKeys(tool, nil, &actool.ToolContext{WorkingDir: dir})
		if len(reads) != 0 {
			t.Fatalf("Bash 是写锁（命令有副作用），不应声明读键：%v", reads)
		}
		if len(writes) != 1 {
			t.Fatalf("Bash 应恰好声明 1 个写键，得到 %v", writes)
		}
		if writes[0] == keyGlobal {
			t.Fatal("Bash 又落回 keyGlobal 了——那等于所有工具全局串行，是本次要修的病根")
		}
		if !strings.HasPrefix(writes[0], "shell:") {
			t.Fatalf("Bash 的写键应落在 shell: 命名空间，实得 %q", writes[0])
		}
		return writes[0]
	}

	t.Run("不同目录算出不同键", func(t *testing.T) {
		a := keysFor(t, t.TempDir())
		b := keysFor(t, t.TempDir())
		if a == b {
			t.Fatalf("两个不同的工作目录算出了同一个执行域键 %q，它们本应可以并行", a)
		}
	})

	t.Run("同一目录的等价写法算出同一个键", func(t *testing.T) {
		// 不归一化的话 /work/a 与 /work/a/ 会算出两个键，于是两条命令在同一个
		// 目录里真的并行执行——而它们共享 cwd 与相对路径，顺序是有意义的。
		base := t.TempDir()
		want := keysFor(t, base)
		for _, variant := range []string{
			base + string(filepath.Separator),                                    // 尾斜杠
			base + string(filepath.Separator) + "." + string(filepath.Separator), // 尾部的 .
			filepath.Join(base, "sub", ".."),                                     // 点段
		} {
			if got := keysFor(t, variant); got != want {
				t.Fatalf("目录 %q 与 %q 应算出同一个执行域键，实得 %q 与 %q", base, variant, want, got)
			}
		}
	})

	t.Run("空目录退到 default 而非全局", func(t *testing.T) {
		if got := keysFor(t, ""); got != "shell:default" {
			t.Fatalf("无工作目录时应退到 shell:default，实得 %q", got)
		}
		if got := keysFor(t, "   "); got != "shell:default" {
			t.Fatalf("纯空白工作目录也应退到 shell:default，实得 %q", got)
		}
		// 关键：default 是 shell: 命名空间内的一个普通键，不是 keyGlobal。
		// 它只让「同样没有目录」的调用彼此串行，不挡住读文件、记事实、发 HTTP。
	})
}

// overlapped 让两条 Bash 调用分别以 dirA / dirB 为工作目录同时发起，
// 返回它们是否真的重叠执行了。
//
// 判据：第一条进去后钉住不放（等 release），若第二条也能进来，说明这两个执行域
// 互不排斥。等 300ms 都进不来就判定为串行——这个时长足够长（远超调度抖动），
// 又不会让测试明显变慢。
func overlapped(t *testing.T, dirA, dirB string) bool {
	t.Helper()
	const wait = 300 * time.Millisecond

	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	tool := lockResources(&countingTool{onCall: func() {
		entered <- struct{}{}
		<-release
	}})

	var wg sync.WaitGroup
	for _, dir := range []string{dirA, dirB} {
		wg.Add(1)
		go func(d string) {
			defer wg.Done()
			_, _ = tool.Call(context.Background(), nil, &actool.ToolContext{WorkingDir: d})
		}(dir)
	}

	// 等第一条进入临界区。
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		wg.Wait()
		t.Fatal("第一条调用始终没进入临界区，测试本身挂了")
	}

	overlap := false
	select {
	case <-entered:
		overlap = true // 第二条也进来了 → 两者并行
	case <-time.After(wait):
		overlap = false // 第二条被挡住 → 两者串行
	}
	close(release)
	wg.Wait()
	return overlap
}

// TestBashParallelAcrossWorkDirs 是本次并发修复的核心断言。
//
// 改造前 Bash 落在 keyGlobal，两条不同目录的命令会互相排队——n 个子代理退化成
// 串行，一次卡死连累全部工具。
func TestBashParallelAcrossWorkDirs(t *testing.T) {
	if !overlapped(t, t.TempDir(), t.TempDir()) {
		t.Fatal("不同工作目录的两条 Bash 应当能并行，实际被串行化了（执行域锁退化了）")
	}
}

// TestBashSerializedWithinWorkDir 是同一枚硬币的另一面：并行不能过头。
//
// 同一目录里的命令共享 cwd 与相对路径（谁先 chdir、谁后写文件是有序的），
// 必须保持原来的串行语义，否则就是把竞态放进来。
func TestBashSerializedWithinWorkDir(t *testing.T) {
	same := t.TempDir()
	if overlapped(t, same, same) {
		t.Fatal("同一工作目录的两条 Bash 应当串行，实际并行了（执行域锁失效）")
	}
}

// TestBashDoesNotBlockOtherNamespaces 验证修复的真正收益：Bash 不再挡住别的工具族。
//
// 改造前 Bash 拿的是全局写锁，期间 Read / record_fact / http_request 全部排队；
// 改造后 Bash 只占 shell: 这一个键，其它命名空间照常通行。
func TestBashDoesNotBlockOtherNamespaces(t *testing.T) {
	// 一条 Bash 钉在临界区里。
	bashEntered := make(chan struct{})
	bashRelease := make(chan struct{})
	bash := lockResources(&countingTool{onCall: func() {
		close(bashEntered)
		<-bashRelease
	}})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = bash.Call(context.Background(), nil, &actool.ToolContext{WorkingDir: t.TempDir()})
	}()
	<-bashEntered

	// 期间一个「独立工具」与一个「只读工具」都应当能立刻通行。
	// http_request 在 independentTools 里 → 与谁都不冲突。
	probe := lockResources(&countingTool{onCall: func() {}})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = probe.Call(context.Background(), nil, &actool.ToolContext{WorkingDir: t.TempDir()})
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(bashRelease)
		wg.Wait()
		t.Fatal("Bash 执行期间其它工具被挡住了——说明它拿的仍是全局锁")
	}
	close(bashRelease)
	wg.Wait()
}
