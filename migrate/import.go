package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Mode 决定导入语义。
type Mode string

const (
	// ModeMerge 保留当前库，把旧库的行按表并进来（主键冲突的行跳过）。
	// 适合"新程序已经跑起来了、不想丢掉这段时间的数据"。
	ModeMerge Mode = "merge"
	// ModeReplace 先清空当前库的每张表再灌旧库——等价于"换成旧库那份数据"。
	// 当前库在动手前会被完整备份，出错也能拿回来。
	ModeReplace Mode = "replace"
)

// ParseMode 把前端传来的字符串收敛成两种模式之一，默认 merge（更安全）。
func ParseMode(s string) Mode {
	if strings.EqualFold(strings.TrimSpace(s), string(ModeReplace)) {
		return ModeReplace
	}
	return ModeMerge
}

// TableImport 是单表的搬运结果，用来让用户对账"到底搬了什么"。
type TableImport struct {
	Name    string `json:"name"`
	OldRows int64  `json:"old_rows"`
	Copied  int64  `json:"copied"`
	Skipped int64  `json:"skipped"`
	Note    string `json:"note,omitempty"`
}

// Result 是一次导入的完整结果。
type Result struct {
	Mode    string        `json:"mode"`
	OldDir  string        `json:"old_dir"`
	NewRoot string        `json:"new_root"`
	Backup  string        `json:"backup_path"`
	Tables  []TableImport `json:"tables"`
	Copied  int64         `json:"copied_rows"`
	Skipped int64         `json:"skipped_rows"`
	// DataFiles / SkillFiles 是实际拷贝的文件数（不是条目数）。
	DataFiles  int `json:"data_files"`
	SkillFiles int `json:"skill_files"`
	// CredentialKey / JwtKey: "copied"（从旧目录拷）/ "kept"（保留现有的）/ "missing"。
	CredentialKey string   `json:"credential_key"`
	JwtKey        string   `json:"jwt_key"`
	Warnings      []string `json:"warnings"`
	// RestartRequired 恒为 true：凭据主密钥与设置缓存都在内存里，导入后必须重启才干净。
	RestartRequired bool `json:"restart_required"`
}

// Import 把 oldDir 的数据搬进"当前进程正在用的库 + 当前安装目录"。
//
// target 必须是进程自己那条 *sql.DB（导入走同一个连接池，而不是另开一个库文件），
// 这样 SQLite 的写锁天然串行，不会出现"两个进程同时写一个 db"。
func Import(ctx context.Context, target *sql.DB, targetRoot, oldDir string, mode Mode, opts Options) (Result, error) {
	res := Result{Mode: string(mode), RestartRequired: true}

	oldAbs, err := filepath.Abs(oldDir)
	if err != nil {
		return res, fmt.Errorf("解析旧目录失败：%w", err)
	}
	newAbs, err := filepath.Abs(targetRoot)
	if err != nil {
		return res, fmt.Errorf("解析当前安装目录失败：%w", err)
	}
	res.OldDir, res.NewRoot = oldAbs, newAbs

	// ① 前置校验：旧库必须存在；不能自己导自己；另一个程序不能在跑。
	if !fileExists(filepath.Join(oldAbs, "artex.db")) {
		return res, errors.New("旧目录里没有 artex.db，无法导入")
	}
	if samePath(oldAbs, newAbs) {
		return res, errors.New("旧目录就是当前安装目录，无需导入")
	}
	if busy := busyPorts(probePorts, opts.OwnPorts); len(busy) > 0 {
		return res, fmt.Errorf("端口 %s 仍被监听：请先完全退出另一个 ARTEX 程序（含流量记录代理）再重试", portsText(busy))
	}

	// ② 动手前先备份当前库：VACUUM INTO 是唯一能在"库正被本进程打开"时安全
	//    拿到一致快照的办法（直接拷文件会把 WAL 里的新数据丢掉）。
	backup := filepath.Join(newAbs, "artex.db.bak-"+time.Now().Format("20060102-150405"))
	if err := backupDB(ctx, target, backup); err != nil {
		return res, fmt.Errorf("备份当前数据库失败：%w", err)
	}
	res.Backup = backup

	// ③ 表数据搬运：一个连接上 ATTACH 旧库，逐表按"共同列"复制。
	oldKey := readFileBytes(filepath.Join(oldAbs, "credential.key"))
	newKey := readFileBytes(filepath.Join(newAbs, "credential.key"))
	keysDiffer := len(oldKey) > 0 && len(newKey) > 0 && string(oldKey) != string(newKey)
	if keysDiffer {
		if mode == ModeMerge {
			res.Warnings = append(res.Warnings,
				"两边的 credential.key 不同：合并模式跳过 llm_* 表（旧库的 API key 无法用当前主密钥解密）。如需沿用旧库的模型配置，请改用「替换」模式。")
		} else {
			res.Warnings = append(res.Warnings,
				"两边的 credential.key 不同：已用旧目录的 credential.key 覆盖，重启后模型配置按旧库生效；期间在新程序里改过的 API key 需要重新填。")
		}
	}

	tables, copied, skipped, terr := copyTables(ctx, target, oldAbs, mode, keysDiffer)
	if terr != nil {
		return res, terr
	}
	res.Tables, res.Copied, res.Skipped = tables, copied, skipped

	// ④ 主密钥 / 会话密钥。
	res.CredentialKey = syncKeyFile(oldAbs, newAbs, "credential.key", mode == ModeReplace)
	res.JwtKey = syncKeyFile(oldAbs, newAbs, "jwt.key", false)

	// ⑤ 工作空间与技能（旧覆盖新，与 CLI 的 import-old 完全一致）。
	if n, cerr := copyTree(filepath.Join(oldAbs, "data"), filepath.Join(newAbs, "data"), map[string]bool{".delete-staging": true}); cerr != nil {
		res.Warnings = append(res.Warnings, "data/ 拷贝不完整："+cerr.Error())
	} else {
		res.DataFiles = n
	}
	if n, cerr := copyTree(filepath.Join(oldAbs, "skills"), filepath.Join(newAbs, "skills"), nil); cerr != nil {
		res.Warnings = append(res.Warnings, "skills/ 拷贝不完整："+cerr.Error())
	} else {
		res.SkillFiles = n
	}

	// ⑥ 旧版把 worker 的 CWD 产物落在安装根目录（i<意图ID>），库里引用着它们；
	//    不搬进新 data/ 就点不开历史报告。语义与 CLI 的 -import-old 一致。
	if n, cerr := copyLegacyRunDirs(oldAbs, filepath.Join(newAbs, "data")); cerr != nil {
		res.Warnings = append(res.Warnings, "i<意图ID> 产物目录拷贝不完整："+cerr.Error())
	} else {
		res.DataFiles += n
	}

	if res.Copied == 0 {
		res.Warnings = append(res.Warnings, "没有复制任何行：请确认旧库版本与当前程序兼容（表结构交集为空）")
	}
	return res, nil
}

// backupDB 用 VACUUM INTO 给当前库做一致性快照。
func backupDB(ctx context.Context, target *sql.DB, path string) error {
	_ = os.Remove(path)
	q := `VACUUM INTO '` + sqlQuote(path) + `'`
	if _, err := target.ExecContext(ctx, q); err != nil {
		return err
	}
	return nil
}

// copyTables 在单个连接上完成"附加旧库 → 逐表复制 → 提交"。
//
// 单连接是硬约束：ATTACH 是连接级的，换一条连接就看不到 old 库了。
func copyTables(ctx context.Context, target *sql.DB, oldDir string, mode Mode, skipLLM bool) ([]TableImport, int64, int64, error) {
	conn, err := target.Conn(ctx)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("取数据库连接失败：%w", err)
	}
	defer conn.Close()

	// 外键在逐表复制过程中必然瞬时不一致（子表先到、父表后到），先关掉；
	// PRAGMA 不能写在事务里，所以放在 BEGIN 之前。
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return nil, 0, 0, fmt.Errorf("关闭外键约束失败：%w", err)
	}
	attach := `ATTACH DATABASE 'file:` + sqlQuote(filepath.ToSlash(filepath.Join(oldDir, "artex.db"))) + `?mode=ro' AS old`
	if _, err := conn.ExecContext(ctx, attach); err != nil {
		return nil, 0, 0, fmt.Errorf("附加旧数据库失败（可能被其它程序占用）：%w", err)
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "DETACH DATABASE old") }()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, 0, 0, fmt.Errorf("开启事务失败：%w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var results []TableImport
	var copiedTotal, skippedTotal int64
	for _, name := range tableNames(ctx, conn) {
		if skipLLM && strings.HasPrefix(name, "llm_") {
			results = append(results, TableImport{Name: name, Note: "跳过：主密钥不同"})
			continue
		}
		cols := commonColumns(ctx, conn, name)
		if len(cols) == 0 {
			results = append(results, TableImport{Name: name, Note: "跳过：两边没有共同列"})
			continue
		}
		var oldRows int64
		if err := conn.QueryRowContext(ctx, fmt.Sprintf(`SELECT count(*) FROM old."%s"`, dq(name))).Scan(&oldRows); err != nil {
			results = append(results, TableImport{Name: name, Note: "跳过：" + err.Error()})
			continue
		}
		if oldRows == 0 {
			continue
		}

		if mode == ModeReplace {
			if _, err := conn.ExecContext(ctx, fmt.Sprintf(`DELETE FROM main."%s"`, dq(name))); err != nil {
				return nil, 0, 0, fmt.Errorf("清空 %s 失败：%w", name, err)
			}
		}

		colList := strings.Join(cols, ", ")
		q := fmt.Sprintf(`INSERT OR IGNORE INTO main."%s" (%s) SELECT %s FROM old."%s"`,
			dq(name), colList, colList, dq(name))
		batch, err := conn.ExecContext(ctx, q)
		if err != nil {
			// 单表失败不拖垮整次导入：记下来让用户看到，其余表照搬。
			results = append(results, TableImport{Name: name, OldRows: oldRows, Note: "失败：" + err.Error()})
			continue
		}
		n, _ := batch.RowsAffected()
		results = append(results, TableImport{
			Name: name, OldRows: oldRows, Copied: n, Skipped: oldRows - n,
		})
		copiedTotal += n
		skippedTotal += oldRows - n
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, 0, 0, fmt.Errorf("提交失败：%w", err)
	}
	committed = true

	// 只保留"真搬了东西"或"有异常"的表，前端表格才看得清。
	kept := results[:0]
	for _, r := range results {
		if r.Copied > 0 || r.Note != "" {
			kept = append(kept, r)
		}
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Copied > kept[j].Copied })
	return kept, copiedTotal, skippedTotal, nil
}

// tableNames 列出主库里的普通表（跳过 sqlite_ 内部表）。
func tableNames(ctx context.Context, conn *sql.Conn) []string {
	rows, err := conn.QueryContext(ctx, `SELECT name FROM main.sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && !strings.HasPrefix(n, "sqlite_") {
			out = append(out, n)
		}
	}
	return out
}

// commonColumns 返回主库与旧库都存在的列（按主库顺序）。
//
// 版本升级会加列、删列，直接 `SELECT *` 一遇到不一致就整表失败；按交集插值
// 才是跨版本唯一稳的做法——缺的列走默认值。
func commonColumns(ctx context.Context, conn *sql.Conn, table string) []string {
	mainCols := columnNames(ctx, conn, "main", table)
	oldSet := map[string]bool{}
	for _, c := range columnNames(ctx, conn, "old", table) {
		oldSet[c] = true
	}
	var out []string
	for _, c := range mainCols {
		if oldSet[c] {
			out = append(out, `"`+dq(c)+`"`)
		}
	}
	return out
}

func columnNames(ctx context.Context, conn *sql.Conn, schema, table string) []string {
	q := fmt.Sprintf(`PRAGMA %s.table_info("%s")`, schema, dq(table))
	rows, err := conn.QueryContext(ctx, q)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    any
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return out
		}
		out = append(out, name)
	}
	return out
}

// syncKeyFile 处理 credential.key / jwt.key：目标是"该有的时候有、该覆盖时覆盖"。
//
// overwrite=false 时只在目标缺失时拷贝——jwt.key 换了会让已登录设备掉线，
// 没必要为了迁移去动它；credential.key 在合并模式下同理（见 Import 里的警告）。
func syncKeyFile(oldRoot, newRoot, name string, overwrite bool) string {
	src := filepath.Join(oldRoot, name)
	dst := filepath.Join(newRoot, name)
	if !fileExists(src) {
		return "missing"
	}
	if fileExists(dst) && !overwrite {
		return "kept"
	}
	if err := copyFile(src, dst); err != nil {
		return "missing"
	}
	return "copied"
}

// copyTree 递归拷贝目录，返回拷贝的文件数。
func copyTree(src, dst string, skip map[string]bool) (int, error) {
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return 0, nil // 旧目录没有这一层：不算失败
	}
	count := 0
	var firstErr error
	err := filepath.Walk(src, func(path string, info os.FileInfo, werr error) error {
		if werr != nil {
			return nil // 单个文件读不了就跳过，别让整棵树中断
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil || rel == "." {
			return nil
		}
		if skip != nil {
			for _, part := range strings.Split(rel, string(filepath.Separator)) {
				if skip[part] {
					return nil
				}
			}
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		if err := copyFile(path, target); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		count++
		return nil
	})
	if err != nil && firstErr == nil {
		firstErr = err
	}
	return count, firstErr
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// copyLegacyRunDirs 把旧安装根目录的 i<意图ID> 产物目录拷进当前工作空间。
// 旧版 worker 在 workDir 为空时回退到进程 CWD，这些目录因此坐在安装根目录，
// 而库里记着它们下面的文件——不搬走就点不开历史报告。
func copyLegacyRunDirs(oldRoot, dataDir string) (int, error) {
	entries, err := os.ReadDir(oldRoot)
	if err != nil {
		return 0, nil // 旧目录读不了不算失败：这一层本来就不是必要条件
	}
	total := 0
	var firstErr error
	for _, e := range entries {
		if !e.IsDir() || !legacyRunDirName(e.Name()) {
			continue
		}
		n, cerr := copyTree(filepath.Join(oldRoot, e.Name()), filepath.Join(dataDir, e.Name()), nil)
		total += n
		if cerr != nil && firstErr == nil {
			firstErr = cerr
		}
	}
	return total, firstErr
}

// legacyRunDirName 判断目录名是否 i<意图ID> 形态（i 后全是数字）。
func legacyRunDirName(name string) bool {
	if len(name) < 2 || name[0] != 'i' {
		return false
	}
	for _, r := range name[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func readFileBytes(path string) []byte {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return raw
}

// dq 把标识符里的双引号转义（SQL 里 " 用 "" 表示）。
func dq(s string) string { return strings.ReplaceAll(s, `"`, `""`) }

// sqlQuote 转义单引号字符串字面量。
func sqlQuote(s string) string { return strings.ReplaceAll(s, `'`, `''`) }
