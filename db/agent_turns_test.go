package db

// agents.max_turns 轮次天花板的回归测试。
//
// 这个值之所以值得单独守：它曾经是 0（不限），而 0 与「从没设过」是同一个值，
// 无法区分——于是「改列默认」对已存在的库是空操作。生产上因此长期没有轮次
// 天花板，唯一收口是 run_seconds 墙钟，导致退化式空转与真长任务在账面上无法
// 区分。这两条用例分别盯住新库与老库两条路径。

import (
	"path/filepath"
	"testing"
)

// TestAgentMaxTurnsDefaultIsStrixCeiling 新库：内置 agent 直接拿到 500。
func TestAgentMaxTurnsDefaultIsStrixCeiling(t *testing.T) {
	d, err := Open("file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "agent-turns.sqlite")))
	if err != nil {
		t.Fatalf("建库失败：%v", err)
	}
	defer d.Close()

	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM agents WHERE builtin = 1 AND max_turns != 500`).Scan(&n); err != nil {
		t.Fatalf("查 agents 失败：%v", err)
	}
	if n != 0 {
		t.Fatalf("有 %d 个内置 agent 的 max_turns 不是 500（应等于 Strix 的 DEFAULT_MAX_TURNS）", n)
	}
	// 至少一个内置 agent 存在，否则上面的断言是空转。
	var total int
	if err := d.QueryRow(`SELECT COUNT(*) FROM agents WHERE builtin = 1`).Scan(&total); err != nil {
		t.Fatalf("查 agents 失败：%v", err)
	}
	if total == 0 {
		t.Fatal("没有内置 agent，用例本身失效")
	}
}

// TestAgentMaxTurnsBackfillsLegacyZero 老库升级：停在旧默认值 0 的内置 agent 被抬到 500。
//
// 造法照 schema_legacy_test.go 的套路：先建完整 schema，再把数据退化成升级前的
// 形态（这里是把 max_turns 全部写回 0），然后走真实的 Open 路径。
func TestAgentMaxTurnsBackfillsLegacyZero(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "legacy-turns.db") + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"

	// 第一次打开：建库并播种。
	d0, err := Open(dsn)
	if err != nil {
		t.Fatalf("建库失败：%v", err)
	}
	// 退化成老库形态：所有内置 agent 回到 0（「不限」），并插一条非内置的作对照。
	if _, err := d0.Exec(`UPDATE agents SET max_turns = 0`); err != nil {
		d0.Close()
		t.Fatalf("退化失败：%v", err)
	}
	// 对照行：模拟「用户自建/显式设成 0（不限）」的 agent。必须显式写 0——
	// 省略该列会拿到新的列默认 500，那就不是「显式设置」了，测不出回填的边界。
	if _, err := d0.Exec(`INSERT INTO agents(key, name, role, builtin, enabled, max_turns) VALUES ('custom_probe', '自建', 'worker', 0, 1, 0)`); err != nil {
		d0.Close()
		t.Fatalf("插对照行失败：%v", err)
	}
	if err := d0.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 第二次打开 = 老库升级路径，回填应当在这里发生。
	d1, err := Open(dsn)
	if err != nil {
		t.Fatalf("老库升级失败：%v", err)
	}
	defer d1.Close()

	var stuck int
	if err := d1.QueryRow(`SELECT COUNT(*) FROM agents WHERE builtin = 1 AND max_turns = 0`).Scan(&stuck); err != nil {
		t.Fatalf("查 agents 失败：%v", err)
	}
	if stuck != 0 {
		t.Fatalf("回填后仍有 %d 个内置 agent 停在 0（不限）——改列默认对存量行无效，回填没生效", stuck)
	}

	// 对照：非内置的 agent 不该被动。用户/自建的轮次设置属于显式配置。
	var custom int
	if err := d1.QueryRow(`SELECT COUNT(*) FROM agents WHERE key = 'custom_probe' AND max_turns = 0`).Scan(&custom); err != nil {
		t.Fatalf("查对照行失败：%v", err)
	}
	if custom != 1 {
		t.Fatal("非内置 agent 的 max_turns 被改了——回填只该动 builtin=1 的行")
	}

	// 幂等：再开一次不应报错，也不应把值改坏。
	d2, err := Open(dsn)
	if err != nil {
		t.Fatalf("二次打开失败（回填不幂等）：%v", err)
	}
	defer d2.Close()
	var again int
	if err := d2.QueryRow(`SELECT COUNT(*) FROM agents WHERE builtin = 1 AND max_turns != 500`).Scan(&again); err != nil {
		t.Fatalf("查 agents 失败：%v", err)
	}
	if again != 0 {
		t.Fatalf("二次打开后 %d 个内置 agent 的 max_turns 不是 500", again)
	}
}
