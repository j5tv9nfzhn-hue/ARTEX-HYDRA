package db

import "testing"

// TestSQLLocalDateShiftsByOffset 守住总览页「UTC+8 少一天」的修复。
//
// 时间列落盘是 UTC 文本，直接切前 10 字节得到的是 UTC 日期。UTC+8 的
// 00:00–08:00 换算回 UTC 还停在前一天，于是"今日用量"被算进昨天、日柱整体错位。
// 这条用例用两个刚好跨日的样本把边界钉死。
func TestSQLLocalDateShiftsByOffset(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	cases := []struct {
		name   string
		ts     string
		offset int
		want   string
	}{
		{"UTC 20:30 + 8h → 次日", "2026-11-05 20:30:00.000000000+00:00", 480, "2026-11-06"},
		{"UTC 15:59 + 8h → 当天最后一分钟", "2026-11-05 15:59:00.000000000+00:00", 480, "2026-11-05"},
		{"UTC 16:01 + 8h → 次日第一分钟", "2026-11-05 16:01:00.000000000+00:00", 480, "2026-11-06"},
		{"offset=0 退回 UTC 日期", "2026-11-05 20:30:00.000000000+00:00", 0, "2026-11-05"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			if err := d.QueryRow(`SELECT sqlLocalDate($1, $2)`, c.ts, c.offset).Scan(&got); err != nil {
				t.Fatalf("sqlLocalDate 调用失败: %v", err)
			}
			if got != c.want {
				t.Fatalf("sqlLocalDate(%s, %d)=%q，期望 %q", c.ts, c.offset, got, c.want)
			}
		})
	}
}
