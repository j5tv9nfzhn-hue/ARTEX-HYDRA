package db

import (
	"testing"

	"github.com/Autumn-27/artex/skills"
)

// TestRetireSkillRefs 造一批"指向已下线技能"的存量脏数据，跑一次迁移，检查三张
// 表各自被处理成什么样，并用一个自建技能名作对照——名单外的技能一行都不能被碰。
//
// 迁移是一次性闸门控制的，而 Open 启动时已经跑过一轮，所以这里先删掉闸门再手动
// 触发。测试跑在共享 dev 库上，所有数据都用自己的名字前缀并在结束时清掉。
func TestRetireSkillRefs(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	retired := skills.RetiredSkillNames()
	if len(retired) == 0 {
		t.Skip("技能库没有已下线的目录名，无需迁移")
	}
	gone := retired[0]

	var agentID int64
	if err := d.QueryRow(`SELECT id FROM agents ORDER BY id LIMIT 1`).Scan(&agentID); err != nil {
		t.Fatalf("取一个 agent id: %v", err)
	}
	if err := d.ensureSkillProposalTable(); err != nil {
		t.Fatalf("建 skill_proposals: %v", err)
	}

	const keep = "zz-test-retire-keep"
	cleanup := func() {
		_, _ = d.Exec(`DELETE FROM agent_skill_visibility WHERE skill_name IN ($1,$2)`, gone, keep)
		_, _ = d.Exec(`DELETE FROM skill_usage WHERE skill IN ($1,$2)`, gone, keep)
		_, _ = d.Exec(`DELETE FROM skill_proposals WHERE skill IN ($1,$2)`, gone, keep)
	}
	cleanup()
	defer cleanup()

	seed := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO agent_skill_visibility(agent_id,skill_name) VALUES ($1,$2) ON CONFLICT DO NOTHING`, []any{agentID, gone}},
		{`INSERT INTO agent_skill_visibility(agent_id,skill_name) VALUES ($1,$2) ON CONFLICT DO NOTHING`, []any{agentID, keep}},
		// 未命中行是 SkillGaps 的输入，迁移要清掉；命中行是真实调用历史，要留下。
		{`INSERT INTO skill_usage(skill,agent_key,found) VALUES ($1,'worker',false)`, []any{gone}},
		{`INSERT INTO skill_usage(skill,agent_key,found) VALUES ($1,'worker',true)`, []any{gone}},
		{`INSERT INTO skill_usage(skill,agent_key,found) VALUES ($1,'worker',false)`, []any{keep}},
		{`INSERT INTO skill_proposals(id,skill,action,content,status) VALUES ($1,$2,'create','x','pending')`, []any{"zz-test-retire-prop-gone", gone}},
		{`INSERT INTO skill_proposals(id,skill,action,content,status) VALUES ($1,$2,'create','x','pending')`, []any{"zz-test-retire-prop-keep", keep}},
	}
	for _, s := range seed {
		if _, err := d.Exec(s.q, s.args...); err != nil {
			t.Fatalf("造数据失败 %v: %v", s.args, err)
		}
	}

	// 闸门在 Open 时已被置上，清掉才能再触发一轮。
	if _, err := d.Exec(`DELETE FROM settings WHERE key=$1`, skillRefsRetiredGate); err != nil {
		t.Fatalf("清迁移闸门: %v", err)
	}
	rep, err := d.retireSkillRefs()
	if err != nil {
		t.Fatalf("retireSkillRefs: %v", err)
	}
	if rep.Visibility < 1 || rep.UsageMisses < 1 || rep.Proposals < 1 {
		t.Errorf("迁移报告偏小：%+v（三者都应 >= 1）", rep)
	}

	count := func(q string, args ...any) int64 {
		t.Helper()
		var n int64
		if err := d.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", q, err)
		}
		return n
	}

	checks := []struct {
		name string
		q    string
		args []any
		want int64
	}{
		{"已下线:可见性应清空", `SELECT count(*) FROM agent_skill_visibility WHERE skill_name=$1`, []any{gone}, 0},
		{"自建:可见性应保留", `SELECT count(*) FROM agent_skill_visibility WHERE skill_name=$1`, []any{keep}, 1},
		{"已下线:未命中应清空", `SELECT count(*) FROM skill_usage WHERE skill=$1 AND found=false`, []any{gone}, 0},
		{"已下线:命中历史应保留", `SELECT count(*) FROM skill_usage WHERE skill=$1 AND found=true`, []any{gone}, 1},
		{"自建:未命中应保留", `SELECT count(*) FROM skill_usage WHERE skill=$1 AND found=false`, []any{keep}, 1},
		{"已下线:待审阅提议应被否决", `SELECT count(*) FROM skill_proposals WHERE skill=$1 AND status=$2`, []any{gone, SkillProposalRejected}, 1},
		{"自建:待审阅提议应保留", `SELECT count(*) FROM skill_proposals WHERE skill=$1 AND status=$2`, []any{keep, SkillProposalPending}, 1},
	}
	for _, c := range checks {
		if got := count(c.q, c.args...); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}

	// 否决要留下可追溯的说明，否则审阅者看到一条"被拒"却不知道为什么。
	var note string
	if err := d.QueryRow(`SELECT note FROM skill_proposals WHERE skill=$1`, gone).Scan(&note); err != nil {
		t.Fatalf("读否决说明: %v", err)
	}
	if note == "" {
		t.Error("被自动否决的提议没有留说明")
	}

	// 播种闸门必须被清掉，否则 75 个新技能永远不会补进来。
	if v, _, _ := d.GetSetting(builtinVisibilityGate); v == "done" {
		t.Error("播种闸门没有被重置，新技能不会被播种")
	}

	// 幂等：闸门已置上，再跑一次应当是空操作。
	again, err := d.retireSkillRefs()
	if err != nil {
		t.Fatalf("第二次 retireSkillRefs: %v", err)
	}
	if again.Visibility != 0 || again.UsageMisses != 0 || again.Proposals != 0 {
		t.Errorf("第二次执行不是空操作：%+v", again)
	}
}
