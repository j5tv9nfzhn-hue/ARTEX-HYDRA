package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The database location is the one piece of storage configuration left, so it is
// pinned here rather than discovered at runtime. These tests also guard the DSN
// contract: every pragma below is load-bearing, and a test that quietly lost one
// would only surface later as a time.Time scan failure or a lock-contention
// flake in an unrelated package.

// pointConfigAt redirects the package-level config lookup at a scratch file so
// the precedence tests do not depend on -- or disturb -- the developer's real
// configuration.
func pointConfigAt(t *testing.T, path string) {
	t.Helper()
	t.Setenv("ARTEX_CONFIG", path)
}

func TestSQLitePathPrecedence(t *testing.T) {
	t.Setenv(envSQLitePath, "")
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	pointConfigAt(t, cfg)

	// Nothing configured: the file lands next to the config file.
	if got := SQLitePath(); filepath.Base(got) != sqliteFileName {
		t.Errorf("默认库文件名 = %q，预期 %q", filepath.Base(got), sqliteFileName)
	}

	// Config file, then environment on top of it.
	fromFile := filepath.Join(dir, "from-file.db")
	writeConfig(t, cfg, Config{Database: Database{Path: fromFile}})
	if got := SQLitePath(); got != fromFile {
		t.Errorf("配置文件指定 path 时 = %q，预期 %q", got, fromFile)
	}

	fromEnv := filepath.Join(dir, "from-env.db")
	t.Setenv(envSQLitePath, fromEnv)
	if got := SQLitePath(); got != fromEnv {
		t.Errorf("环境变量应覆盖配置文件，实际 = %q", got)
	}
}

func TestSQLiteDSNCarriesRequiredPragmas(t *testing.T) {
	t.Setenv(envSQLitePath, filepath.Join(t.TempDir(), "a.db"))
	dsn := SQLiteDSN()

	// Each of these is load-bearing rather than tuning; see SQLiteDSN's comment.
	//
	// busy_timeout 的正确值从 config.SQLiteBusyTimeout 推导，不再写死数字：
	// 硬编码会让"调小预算"这件事变成一次两处不同步的改动（DSN 改了、断言还在
	// 等旧值，测试红给你看，而你很可能去改测试而不是改回预算）。
	busyWant := "busy_timeout(" + strconv.FormatInt(SQLiteBusyTimeout.Milliseconds(), 10) + ")"
	for _, want := range []string{
		"file:",
		"journal_mode(WAL)",   // readers must not block behind a writer
		busyWant,              // contended writes wait instead of SQLITE_BUSY
		"synchronous(NORMAL)", // no per-commit fsync: a commit must not hog the write lock
		"foreign_keys(ON)",    // SQLite ignores REFERENCES without this
		// WAL 的两条边界契约：没有 journal_size_limit，WAL 只增不减；没有显式的
		// autocheckpoint，检查点频率就依赖一个我们没写下来的默认值。
		"journal_size_limit(67108864)",
		"wal_autocheckpoint(1000)",
		"_texttotime=1",     // otherwise every time.Time scan fails
		"_txlock=immediate", // otherwise read-then-write has a race window
	} {
		if !strings.Contains(dsn, want) {
			t.Errorf("DSN 缺少 %q：%s", want, dsn)
		}
	}
}

// TestSQLiteDSNForMatchesSQLiteDSN pins the two constructors to one contract.
// Tests and tooling use SQLiteDSNFor with their own path; if it ever drifted from
// SQLiteDSN, those callers would silently exercise a different database
// configuration than production.
func TestSQLiteDSNForMatchesSQLiteDSN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "same.db")
	t.Setenv(envSQLitePath, path)
	if got, want := SQLiteDSNFor(path), SQLiteDSN(); got != want {
		t.Errorf("SQLiteDSNFor 与 SQLiteDSN 不一致：\n got %q\nwant %q", got, want)
	}
}

func TestSQLitePathIsAbsoluteAndCreatesParent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nested", "deeper", "artex.db")
	t.Setenv(envSQLitePath, target)

	got := SQLitePath()
	if !filepath.IsAbs(got) {
		t.Errorf("SQLitePath() = %q，应为绝对路径", got)
	}
	if fi, err := os.Stat(filepath.Dir(got)); err != nil || !fi.IsDir() {
		t.Errorf("父目录未被创建：%v", err)
	}
}

// TestSQLitePathSourceNamesOrigin pins the three startup-log labels so an
// operator can tell which source won without reading the code.
func TestSQLitePathSourceNamesOrigin(t *testing.T) {
	t.Setenv(envSQLitePath, "")
	pointConfigAt(t, filepath.Join(t.TempDir(), "nope.json"))
	if got := SQLitePathSource(); !strings.Contains(got, "内置默认") {
		t.Errorf("无配置时来源 = %q，预期内置默认", got)
	}

	t.Setenv(envSQLitePath, filepath.Join(t.TempDir(), "x.db"))
	if got := SQLitePathSource(); !strings.Contains(got, envSQLitePath) {
		t.Errorf("环境变量来源 = %q，应含 %q", got, envSQLitePath)
	}
}
