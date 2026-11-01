package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// legacyDSN 造一个「v2.3.x 形态」的数据库文件：先把完整 schema 建好，再把
// provider_id 这一列连同它的索引一起退回去。
//
// 这样得到的文件与现场升级前的老库同构（llm_providers 已存在、llm_profiles
// 没有 provider_id），而且是真实落到磁盘的文件，Open 走的路径与生产完全一致。
func legacyDSN(t *testing.T) string {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "legacy-2.3.db") + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
	con, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	for _, stmt := range sqliteStatements(schemaSQLiteSQL) {
		if _, err := con.Exec(stmt); err != nil {
			con.Close()
			t.Fatalf("build fixture schema: %v\n  > %s", err, stmt)
		}
	}
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS idx_llm_profiles_provider`,
		`ALTER TABLE llm_profiles DROP COLUMN provider_id`,
	} {
		if _, err := con.Exec(stmt); err != nil {
			con.Close()
			t.Fatalf("degrade fixture: %v\n  > %s", err, stmt)
		}
	}
	if err := con.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}
	return dsn
}

// TestOpenUpgradesLegacyLLMProfiles 复现 v2.4.0 首次升级时的现场故障：
//
//	open stores: apply sqlite schema: statement 63/228: SQL logic error:
//	no such column: provider_id (1)
//	  > CREATE INDEX IF NOT EXISTS idx_llm_profiles_provider ON llm_profiles(provider_id);
//
// 老库的 llm_profiles 建在「供应商」改造之前，没有 provider_id 列；schema 每次
// 启动整段重放，而 CREATE TABLE IF NOT EXISTS 对已存在的表是空操作，所以只要
// schema 里有任何 DDL 引用这一列，启动就会当场失败。CI 里的库全是新建的，
// 这类问题只会在真实升级路径上出现——这条用例就是那条路径。
func TestOpenUpgradesLegacyLLMProfiles(t *testing.T) {
	dsn := legacyDSN(t)

	d, err := Open(dsn)
	if err != nil {
		t.Fatalf("老库升级失败（依赖补列的 DDL 又跑到补列前面去了？）：%v", err)
	}

	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('llm_profiles') WHERE name='provider_id'`).Scan(&n); err != nil {
		d.Close()
		t.Fatalf("查列失败：%v", err)
	}
	if n != 1 {
		d.Close()
		t.Fatal("provider_id 列没有被补回来")
	}
	if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_llm_profiles_provider'`).Scan(&n); err != nil {
		d.Close()
		t.Fatalf("查索引失败：%v", err)
	}
	if n != 1 {
		d.Close()
		t.Fatal("idx_llm_profiles_provider 没有建起来（补列之后的 DDL 没跑）")
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 再开一次：schema 重放 + 补列 + 后置 DDL 都必须幂等，
	// 否则用户第二次启动会直接失败。
	d2, err := Open(dsn)
	if err != nil {
		t.Fatalf("第二次打开失败（迁移不幂等）：%v", err)
	}
	if err := d2.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

// legacyFindingsDSN 造一个「反证闭环改造之前」形态的数据库：完整 schema 建好后，
// 把 findings 的四列退回去，模拟改造前已存在的老库。
//
// 为什么必须单独造：CI 里的库全是新建的，CREATE TABLE 自带这四列，升级路径
// （sqliteColumnAdds 的 ALTER）一次都不会被走到。老库升级是这条改造唯一的
// 不可逆路径——补列失败等于用户升级后整个进程起不来。
func legacyFindingsDSN(t *testing.T) string {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "legacy-findings.db") + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
	con, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	for _, stmt := range sqliteStatements(schemaSQLiteSQL) {
		if _, err := con.Exec(stmt); err != nil {
			con.Close()
			t.Fatalf("build fixture schema: %v\n  > %s", err, stmt)
		}
	}
	for _, col := range findingAnalysisColumns {
		if _, err := con.Exec(`ALTER TABLE findings DROP COLUMN ` + col); err != nil {
			con.Close()
			t.Fatalf("degrade fixture (%s): %v", col, err)
		}
	}
	if err := con.Close(); err != nil {
		t.Fatalf("close fixture: %v", err)
	}
	return dsn
}

// findingAnalysisColumns 是反证闭环落到 findings 表上的四个列名。
// 与 agent.validateFindingAnalysis 校验的四个字段一一对应，改名必须两边同步。
var findingAnalysisColumns = []string{
	"counterevidence", "confidence", "confidence_rationale", "severity_change_conditions",
}

// TestOpenUpgradesLegacyFindingsAnalysis 验证老库能补出反证四列，且可重复打开。
//
// 失败形态与 v2.4.0 的 provider_id 事故同源：schema 每次启动整段重放，而
// CREATE TABLE IF NOT EXISTS 对已存在的表是空操作——老库里这四列不存在，
// 任何引用它们的 DDL/查询都会当场炸。
func TestOpenUpgradesLegacyFindingsAnalysis(t *testing.T) {
	dsn := legacyFindingsDSN(t)

	d, err := Open(dsn)
	if err != nil {
		t.Fatalf("老库升级失败（反证四列的补列没跑到使用它们之前？）：%v", err)
	}
	for _, col := range findingAnalysisColumns {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('findings') WHERE name=?`, col).Scan(&n); err != nil {
			d.Close()
			t.Fatalf("查列 %s 失败：%v", col, err)
		}
		if n != 1 {
			d.Close()
			t.Fatalf("老库没有补出 %s 列", col)
		}
	}
	// 补列给的是空串默认值：老漏洞「没做过反证」，不能用任何 confidence 取值假装做过。
	var ce, conf string
	if err := d.QueryRow(`SELECT COALESCE(counterevidence,''), COALESCE(confidence,'') FROM findings LIMIT 1`).Scan(&ce, &conf); err != nil &&
		err.Error() != "sql: no rows in result set" {
		d.Close()
		t.Fatalf("读历史行失败：%v", err)
	}
	if conf != "" {
		d.Close()
		t.Fatalf("历史漏洞的 confidence 应为空白（未评估），实得 %q", conf)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// 再开一次：补列必须幂等，否则用户第二次启动直接失败。
	d2, err := Open(dsn)
	if err != nil {
		t.Fatalf("第二次打开失败（迁移不幂等）：%v", err)
	}
	if err := d2.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
