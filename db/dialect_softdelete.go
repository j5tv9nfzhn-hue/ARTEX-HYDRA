package db

import (
	"database/sql"
	"errors"
	"fmt"
)

// upsertCompanySQLiteInsert 只负责"抢占"这一行：nkey 已存在时什么都不做，
// 并且不返回任何行。与本文件配套的第 2 条语句见 upsertCompanySQLiteUpdate。
const upsertCompanySQLiteInsert = `
INSERT INTO companies(name, nkey, logo)
VALUES ($1, $2, $3)
ON CONFLICT (nkey) DO NOTHING
RETURNING id`

// upsertCompanySQLiteUpdate 是 xmax <> 0 分支的等价物：把已有企业改写成
// 新值并回传 id。占位符按 (name, logo, nkey) 的自然顺序排：
// COALESCE($2, logo)：调用方传空 logo 时保留原值。
const upsertCompanySQLiteUpdate = `
UPDATE companies
SET name = $1, logo = COALESCE($2, logo), updated_at = now()
WHERE nkey = $3
RETURNING id`

// upsertCompanyByNkey 按规范化名称 upsert 一家企业，并报告这次调用到底是
// 新建（created=true）还是命中已有企业（created=false）。
//
// SQLite 没有 xmax 系统列，不能靠单条 upsert 判断"是插入还是更新"，改用
// "抢占式 INSERT + UPDATE"的两语句写法，对外返回值完全一致。
func upsertCompanyByNkey(d *DB, name, nkey string, logoVal any) (id int64, created bool, err error) {
	tx, err := d.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback() //nolint:errcheck

	// 插入分支：唯一键无冲突，DO NOTHING 不会吞掉本行，所以必然返回一行。
	if scanErr := tx.QueryRow(upsertCompanySQLiteInsert, name, nkey, logoVal).Scan(&id); scanErr == nil {
		if err := tx.Commit(); err != nil {
			return 0, false, err
		}
		return id, true, nil
	} else if !errors.Is(scanErr, sql.ErrNoRows) {
		return 0, false, scanErr
	}

	// 更新分支：DO NOTHING 命中已有 nkey，一条都没写，所以回传 0 行。
	if err := tx.QueryRow(upsertCompanySQLiteUpdate, name, logoVal, nkey).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// 写锁保护下不可达；真发生了说明 schema 或并发假设被破坏，
			// 明确报出来而不是伪装成正常结果。
			return 0, false, fmt.Errorf("upsert company %q: conflicting row is gone", nkey)
		}
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return id, false, nil
}
