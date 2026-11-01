package db

import "testing"

// countRows 是这类断言的最小面，避免为了数行数去碰业务 API。
func countRows(t *testing.T, d *DB, query string, arg int64) int {
	t.Helper()
	var n int
	if err := d.QueryRow(query, arg).Scan(&n); err != nil {
		t.Fatalf("清点失败 (%s): %v", query, err)
	}
	return n
}

// TestDeleteTaskCascadeAcrossReviewerTableShapes 把 reviewer 遗留表的两种库形态都钉住。
//
// reviewer_evidence / reviewer_verdicts 的 CREATE TABLE 已随 Reviewer 下线从
// schema_sqlite.sql 移除，但删除级联与归档清库里仍在清理它们，所以调用点按
// tableExists 分叉。此前测试库都是新建的，只跑得到「表不存在」那条分支，而
// 「表存在」的历史库行为根本没被验证过——这个用例补上：
//
//   - 历史库（表还在）：删除任务必须照旧清掉遗留行，不留孤儿；
//   - 新库（表已不在）：删除任务必须成功，且真的执行到 DELETE FROM tasks。
//
// 后者是真实故障的回归：表不存在时那句 DELETE 会报 no such table 并整段回滚，
// 事务在删 tasks 行之前就 return，任务永远删不掉。
func TestDeleteTaskCascadeAcrossReviewerTableShapes(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// 注册在 d.Close() 之后 → 先于它执行（defer 后进先出），库还开着。
	// 只做尽力清理：失败也不能 Fatal，否则会盖掉真正的断言失败。
	defer func() {
		for _, tbl := range []string{"reviewer_evidence", "reviewer_verdicts"} {
			_, _ = d.Exec(`DROP TABLE IF EXISTS ` + tbl)
		}
	}()

	legacyTables := []string{"reviewer_evidence", "reviewer_verdicts"}
	dropLegacy := func() {
		for _, tbl := range legacyTables {
			if _, err := d.Exec(`DROP TABLE IF EXISTS ` + tbl); err != nil {
				t.Fatalf("清理 %s 失败: %v", tbl, err)
			}
		}
	}
	makeLegacy := func() {
		for _, tbl := range legacyTables {
			if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS ` + tbl + ` (task_id INTEGER)`); err != nil {
				t.Fatalf("构造历史 %s 表失败: %v", tbl, err)
			}
		}
	}
	// ---- 分支一：历史库形态，表都还在 ----
	makeLegacy()
	legacy, err := d.CreateTask("历史库形态删除", "legacy-shape.example.test", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, tbl := range legacyTables {
		if _, err := d.Exec(`INSERT INTO `+tbl+`(task_id) VALUES($1)`, legacy.ID); err != nil {
			t.Fatalf("写入 %s 失败: %v", tbl, err)
		}
	}
	if err := d.DeleteTask(legacy.ID); err != nil {
		t.Fatalf("历史库形态下删除任务失败: %v", err)
	}
	for _, tbl := range legacyTables {
		if n := countRows(t, d, `SELECT COUNT(*) FROM `+tbl+` WHERE task_id=$1`, legacy.ID); n != 0 {
			t.Fatalf("%s 残留 %d 行：历史库应被照旧清理，不能留孤儿", tbl, n)
		}
	}
	if n := countRows(t, d, `SELECT COUNT(*) FROM tasks WHERE id=$1`, legacy.ID); n != 0 {
		t.Fatal("历史库形态下 tasks 行仍在，删除没生效")
	}

	// ---- 分支二：按当前 schema 新建的库，两张表都不存在 ----
	dropLegacy()
	if tableExists(d, "reviewer_evidence") || tableExists(d, "reviewer_verdicts") {
		t.Fatal("前置条件不成立：两张表应该都不存在")
	}
	fresh, err := d.CreateTask("新库形态删除", "fresh-shape.example.test", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteTask(fresh.ID); err != nil {
		t.Fatalf("新库形态下删除任务失败（表不存在时曾整段回滚）: %v", err)
	}
	if n := countRows(t, d, `SELECT COUNT(*) FROM tasks WHERE id=$1`, fresh.ID); n != 0 {
		t.Fatal("DELETE FROM tasks 没执行到：任务行仍在，正是被修掉的故障")
	}
}
