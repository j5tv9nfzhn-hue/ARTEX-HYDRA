package db

import (
	"context"
	"log"
	"os"
	"strings"
	"time"
)

// WAL 自愈。
//
// 背景：SQLite 的检查点只能回填「比最老读事务还老」的 WAL 帧。本应用的读走独立
// 连接池，UI 又持续轮询，读事务几乎从不完全消失——于是检查点可能长期推不动，
// WAL 只增不减。WAL 变大之后，每次提交都要顺带做检查点，写入延迟被反过来拖慢。
// 这是并发场景下真实的劣化路径。
//
// 已经有的两道防线：
//  1. journal_size_limit(64MiB) 写进了 DSN —— 硬上限，超了自动截断；
//  2. wal_autocheckpoint(1000) 显式声明 —— 每次提交后按需回填。
//
// 但它们都是「SQLite 内部按规则行事」，遇到读者持续钉住旧帧时，PASSIVE 检查点
// 会直接放弃（它不阻塞任何人，也就意味着它可以什么都不做）。所以在应用层再补
// 一层主动维护：定期看一眼 WAL 多大，明显偏大就在后台跑一次更有力的检查点。
//
// 设计原则与 ReclaimFreePages 一致：最佳努力、有界、可重入、绝不阻塞用户路径，
// 失败只记日志。它永远不该成为「需要用户手动调的东西」——出了问题程序自己处理。

const (
	// walHealthyBytes 是 WAL 的正常水位。低于它什么都不做：默认
	// wal_autocheckpoint(1000 页 ≈ 4MB) 本来就会让 WAL 稳定在几 MB 量级，
	// 频繁介入只会白白抢写锁。
	walHealthyBytes = 32 << 20 // 32MiB

	// walEmergencyBytes 是「必须处理」的水位。到了这里说明检查点长时间没能
	// 回填（典型原因：一个长读事务或一个忘了释放的 Rows），必须用 RESTART
	// 强制把 WAL 拉回起点，否则它会一直涨到 journal_size_limit 才被截断。
	walEmergencyBytes = 96 << 20 // 96MiB

	// walMaintenanceBudget 是一次维护允许占用的最长时间。超过就放弃、下次再来：
	// 维护是后台行为，不该长时间占着写锁。
	walMaintenanceBudget = 5 * time.Second

	// walStatusBudget 是单次**观测**的预算，比维护预算短一个量级。
	// 观测挂在存储页的 HTTP 请求上，不能让它把页面拖到几秒；维护跑在后台
	// 协程里，慢一点没人等。
	walStatusBudget = 500 * time.Millisecond
)

// WALStatus 是 WAL 的当前状态，供存储页/诊断展示与自愈判定使用。
type WALStatus struct {
	SizeBytes  int64 `json:"size_bytes"` // -wal 文件当前大小
	Frames     int   `json:"frames"`     // WAL 中的帧数
	Backfilled int   `json:"backfilled"` // 已回填进主库的帧数
	Busy       bool  `json:"busy"`       // true = 检查点被读者挡住，无法推进
	Healthy    bool  `json:"healthy"`    // 综合判定
}

// sqliteFilePathFromDSN 从 "file:<path>?<params>" 里取出文件路径。
//
// WAL 维护要按路径去看 -wal 文件大小，而调用点手上只有 DSN。这里做最小解析：
// 去掉 "file:" 前缀、在第一个 '?' 处截断。找不到路径（空串）时调用方跳过维护
// ——宁可不动，也不要对着一个猜出来的路径做检查点。
func sqliteFilePathFromDSN(dsn string) string {
	s := strings.TrimSpace(dsn)
	if !strings.HasPrefix(strings.ToLower(s), "file:") {
		return ""
	}
	s = s[len("file:"):]
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i]
	}
	return s
}

// walSize 读 -wal 文件大小。取不到（还没创建、已被截断）时返回 0。
func walSize(path string) int64 {
	st, err := os.Stat(path + "-wal")
	if err != nil {
		return 0
	}
	return st.Size()
}

// WALStatusOf 读一次 WAL 状态。
//
// PRAGMA wal_checkpoint(PASSIVE) 返回三个整数：busy、日志帧数、已回填帧数。
// PASSIVE 不阻塞任何读者和写者，所以它是安全的**观测**手段：即使它什么都没能
// 回填，也不会给前台制造等待。我们只用它读数，不依赖它清理。
func (d *DB) WALStatusOf(path string) WALStatus {
	// 观测专用预算，比维护预算短：这个函数跟着存储页的 HTTP 请求走，用户点开
	// 页面不该等满 5 秒。PASSIVE 检查点即使被打断也没有副作用（不写主库之外的
	// 任何东西），超时就当作「这轮没读到帧数」，只报文件大小。
	ctx, cancel := context.WithTimeout(context.Background(), walStatusBudget)
	defer cancel()

	st := WALStatus{SizeBytes: walSize(path)}

	var busy, frames, backfilled int
	conn, err := d.Conn(ctx)
	if err != nil {
		return st
	}
	defer conn.Close()
	row := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)")
	if err := row.Scan(&busy, &frames, &backfilled); err != nil {
		// 有些版本的驱动把 PRAGMA 结果当单列返回；取不到帧数就只按文件大小判定。
		st.Healthy = st.SizeBytes < walHealthyBytes
		return st
	}
	st.Busy = busy != 0
	st.Frames = frames
	st.Backfilled = backfilled
	st.Healthy = st.SizeBytes < walHealthyBytes && !st.Busy
	return st
}

// maintainWAL 是主动维护：WAL 偏大时跑一次更有力的检查点。
//
// 分两档：
//   - 超过 walHealthyBytes：RESTART。它在 FULL 的基础上把 WAL 指针拉回起点，
//     让下一次写入从文件头开始覆盖写，而不是继续往后追加。代价是要等当前所有
//     读者释放快照——所以放在后台、带 ctx 预算，等不到就算了。
//   - 超过 walEmergencyBytes：TRUNCATE，直接把文件截到 0。只有在 RESTART 也
//     没能降下来（说明有读者长期钉住旧帧）时才用，属于兜底。
//
// 返回值是维护后的大小，便于调用方记录「这次省了多少」。
func (d *DB) maintainWAL(path string) int64 {
	before := walSize(path)
	if before < walHealthyBytes {
		return before // 正常水位，不动它
	}

	mode := "RESTART"
	if before >= walEmergencyBytes {
		mode = "TRUNCATE"
	}

	ctx, cancel := context.WithTimeout(context.Background(), walMaintenanceBudget)
	defer cancel()

	conn, err := d.Conn(ctx)
	if err != nil {
		log.Printf("[db] WAL 维护：获取连接失败：%v", err)
		return before
	}
	defer conn.Close()

	var busy, frames, backfilled int
	if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint("+mode+")").
		Scan(&busy, &frames, &backfilled); err != nil {
		log.Printf("[db] WAL 维护：%s 检查点失败：%v", mode, err)
		return before
	}
	after := walSize(path)
	if busy != 0 {
		// 有读者挡住了这次检查点。不是错误——只说明这次没降下来，下次维护再试。
		// 记一行日志是为了排障时能看出「WAL 大是因为有长读事务」。
		log.Printf("[db] WAL 维护：%s 检查点被读者挡住，WAL %d→%d 字节（帧 %d/%d）",
			mode, before, after, backfilled, frames)
		return after
	}
	if before-after >= walHealthyBytes/2 {
		log.Printf("[db] WAL 维护：%s 检查点回收 %d→%d 字节", mode, before, after)
	}
	return after
}

// StartWALMaintenance 起一个后台协程，定期做 WAL 自愈，直到 ctx 结束。
//
// 调用方是启动流程（server 侧），跟随进程生命周期。它不做任何「用户可配置」
// 的事：间隔和阈值都是代码常量，因为这是数据库内部健康度问题，不是产品功能。
//
// 间隔取 2 分钟：WAL 从 32MiB 涨到 96MiB 在这类工作负载下需要相当长时间，
// 2 分钟的检查频率足以在它变成问题之前介入；而每次检查只是一次 PASSIVE
// PRAGMA，代价可以忽略。
func (d *DB) StartWALMaintenance(ctx context.Context) {
	if d == nil || d.path == "" {
		return
	}
	path := d.path
	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.maintainWAL(path)
			}
		}
	}()
}

// WALStatus 返回当前 WAL 状态，供存储页展示与排障。内存库返回零值。
//
// 与 WALStatusOf 的区别只是路径从 DB 自己取——调用方（server 的存储页）
// 手上只有一个 *db.DB，不该关心库文件在哪。
func (d *DB) WALStatus() WALStatus {
	if d == nil || d.path == "" {
		return WALStatus{Healthy: true}
	}
	return d.WALStatusOf(d.path)
}
