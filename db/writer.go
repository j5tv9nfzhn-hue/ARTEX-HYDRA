package db

// 单写协程 + 批量提交。
//
// SQLite 是「一写多读」：同一时刻只允许一个写事务。此前每条写入各自开事务，并发一高
// 就在文件锁上排队（busy_timeout 内干等），而且提交次数 = 调用次数——Windows 上每次
// 提交还要过一次 fsync（常常还要穿过杀毒软件的过滤驱动）。
//
// 这里把「独立、可整体丢弃」的写入收进一个队列，由唯一一个写协程执行：同一时间窗口内
// 到达的多条写入合成一个事务提交，锁只抢一次、fsync 只做一次。调用方仍然同步拿到结果
// （提交完成后才返回），所以「写完立刻能按 id 读回」的语义不变。
//
// 边界：有强事务语义的写入（读-改-写、跨表一致性）继续走各自的 d.Begin()，不进队列；
// 队列里的 job 也不能再调用 WriteQueued（单协程会自锁）。

import (
	"context"
	"database/sql"
	"sync"
	"time"
)

const (
	// writeBatchWindow 是「凑批」的基础等待窗口:第一条写入到达后再等这么久收同伴。
	// 5ms 远小于一次 fsync 的代价,攒到 2 条就已经回本。
	//
	// 它是**下限**而不是固定值：真实窗口由积压量动态决定，见 batchWindowFor。
	writeBatchWindow = 5 * time.Millisecond
	// writeBatchMax 是单批上限:太大时锁持有时间变长,读会被 WAL 写事务拖慢。
	writeBatchMax = 200
	// writeQueueDepth 是队列缓冲:满了以后 submit 阻塞在 channel 上(背压),不丢写。
	//
	// 并发任务开起来后这里会堆得比单任务高得多，512 意味着尖峰时可以吸收 512 条
	// 待提交写入而不断背压。它不需要更大：真正的过载信号是「队列长期接近满」，
	// 那时该做的是降低写入频率而不是加深队列。
	writeQueueDepth = 512

	// writeBatchWindowMax 是动态窗口的上限。积压再多也不会等超过这个时间——
	// 窗口的意义是「等同伴」，不是「人为拖延」。20ms 仍在一次慢 fsync 的量级内，
	// 但足以让 5 个并发 agent 的写入挤进同一批。
	writeBatchWindowMax = 20 * time.Millisecond
)

type writeJob struct {
	run   func(*sql.Tx) error
	done  chan error
	began time.Time
}

// WriteStats 是单写协程的累计观测值。存储页展示它，用来回答「批量提交到底省了多少」：
// Batches/Jobs 的比值就是平均批量，AvgWaitMillis 是一次写入从入队到提交完成的平均耗时。
type WriteStats struct {
	Batches       uint64  `json:"batches"`
	Jobs          uint64  `json:"jobs"`
	MaxBatch      int     `json:"max_batch"`
	LastBatch     int     `json:"last_batch"`
	Rollbacks     uint64  `json:"rollbacks"`
	QueueDepth    int     `json:"queue_depth"`
	AvgBatch      float64 `json:"avg_batch"`
	AvgWaitMillis float64 `json:"avg_wait_ms"`
}

type writeQueue struct {
	db *DB
	ch chan *writeJob

	statsMu   sync.Mutex
	batches   uint64
	jobs      uint64
	okJobs    uint64
	rollbacks uint64
	maxBatch  int
	lastBatch int
	waitNanos uint64
}

func newWriteQueue(d *DB) *writeQueue {
	q := &writeQueue{db: d, ch: make(chan *writeJob, writeQueueDepth)}
	go q.loop()
	return q
}

// writeQueue 懒初始化:测试里直接 &DB{DB: raw} 构造的句柄也能用。
func (d *DB) writer() *writeQueue {
	d.wqMu.Lock()
	defer d.wqMu.Unlock()
	if d.wq == nil {
		d.wq = newWriteQueue(d)
	}
	return d.wq
}

// WriteQueued 把一个独立写入提交给单写协程,等它所在批次提交完成后返回。
// ctx 不传:后台写入的等待预算由 busy_timeout 兜底(见 config.SQLiteBusyTimeout),
// 用请求 ctx 反而会在排队时把「本来会成功」的写入打断。
func (d *DB) WriteQueued(run func(*sql.Tx) error) error {
	return d.writer().submit(context.Background(), run)
}

func (q *writeQueue) submit(ctx context.Context, run func(*sql.Tx) error) error {
	job := &writeJob{run: run, done: make(chan error, 1), began: time.Now()}
	select {
	case q.ch <- job:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-job.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// batchWindowFor 决定这一批的凑批窗口：积压越多等得越久，但有硬上限。
//
// 为什么需要动态窗口。固定 5ms 是为「单任务、写入稀疏」调的——那条路径上队列
// 里通常只有 1~2 条，多等没有意义。但任务并发开起来后写入是成簇到达的：一个
// 子 agent 的一次工具调用会连着写 activity + 若干图节点，5 个 agent 同时跑就是
// 五簇交错。固定 5ms 只能随机捞到簇的一小部分，剩下的落进下一批——每批都只有
// 三五条，fsync 次数跟着涨，而 fsync 正是 Windows 上最贵的那一步。
//
// 于是把窗口与积压量挂钩：积压说明「还有一批人马上要写」，那就多等一会儿把它们
// 收进来，用一次 fsync 结掉。上界 20ms 保证不会为了凑批而人为拖慢交互——用户在
// 界面上看到的一次写入等待，最坏也只是多这 20ms。
//
// 注意这不是"负载越高越慢"的退让：窗口变长换来的是**批更大、提交更少**，队列
// 排空的总时间反而更短。它唯一的代价是单条写入的延迟上限从 5ms 抬到 20ms。
func batchWindowFor(depth int) time.Duration {
	switch {
	case depth >= 64:
		return writeBatchWindowMax
	case depth >= 16:
		return writeBatchWindow * 3 // 15ms
	case depth >= 4:
		return writeBatchWindow * 2 // 10ms
	default:
		return writeBatchWindow
	}
}

// loop 是唯一执行写事务的协程。第一批之后窗口内到达的写入会并进同一批。
func (q *writeQueue) loop() {
	for first := range q.ch {
		batch := []*writeJob{first}
		timer := time.NewTimer(batchWindowFor(len(q.ch)))
	drain:
		for len(batch) < writeBatchMax {
			select {
			case j := <-q.ch:
				batch = append(batch, j)
			case <-timer.C:
				break drain
			}
		}
		timer.Stop()
		q.commit(batch)
	}
}

// commit 用一个事务提交整批。任一条失败则整批回滚并通知全部等待者——批内的写入都是
// 可整体重放的 activity 追加,回滚不会丢语义;换成「一条失败只丢一条」需要在事务外
// 重试,反而把锁持有时间拉长。
func (q *writeQueue) commit(batch []*writeJob) {
	tx, err := q.db.Begin()
	if err != nil {
		q.record(batch, len(batch), true)
		for _, j := range batch {
			j.done <- err
		}
		return
	}
	for _, j := range batch {
		if err := j.run(tx); err != nil {
			_ = tx.Rollback()
			q.record(batch, len(batch), true)
			for _, k := range batch {
				k.done <- err
			}
			return
		}
	}
	if err := tx.Commit(); err != nil {
		q.record(batch, len(batch), true)
		for _, j := range batch {
			j.done <- err
		}
		return
	}
	q.record(batch, len(batch), false)
	for _, j := range batch {
		j.done <- nil
	}
}

// record 累加一批的观测值（每批一次，锁竞争可忽略）。
func (q *writeQueue) record(batch []*writeJob, size int, failed bool) {
	now := time.Now()
	var waited uint64
	for _, j := range batch {
		if !j.began.IsZero() {
			waited += uint64(now.Sub(j.began))
		}
	}
	q.statsMu.Lock()
	defer q.statsMu.Unlock()
	q.jobs += uint64(size)
	if !failed {
		// 只有提交成功的那批才算进「平均批量」：回滚的批不该把分母做大。
		q.batches++
		q.okJobs += uint64(size)
	}
	q.waitNanos += waited
	q.lastBatch = size
	if size > q.maxBatch {
		q.maxBatch = size
	}
	if failed {
		q.rollbacks++
	}
}

// snapshot 取一份一致的观测快照。
func (q *writeQueue) snapshot() WriteStats {
	q.statsMu.Lock()
	defer q.statsMu.Unlock()
	st := WriteStats{
		Batches:    q.batches,
		Jobs:       q.jobs,
		MaxBatch:   q.maxBatch,
		LastBatch:  q.lastBatch,
		Rollbacks:  q.rollbacks,
		QueueDepth: len(q.ch),
	}
	if q.batches > 0 {
		st.AvgBatch = float64(q.okJobs) / float64(q.batches)
	}
	if q.jobs > 0 {
		st.AvgWaitMillis = float64(q.waitNanos) / float64(q.jobs) / float64(time.Millisecond)
	}
	return st
}

// WriteStats 返回单写协程的累计观测值；队列还没启动时返回零值。
func (d *DB) WriteStats() WriteStats {
	d.wqMu.Lock()
	q := d.wq
	d.wqMu.Unlock()
	if q == nil {
		return WriteStats{}
	}
	return q.snapshot()
}
