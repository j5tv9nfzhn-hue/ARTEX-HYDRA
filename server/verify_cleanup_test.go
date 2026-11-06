package server

import (
	"context"
	"testing"

	"github.com/Autumn-27/artex/db"
)

// TestRetireVerifierRemnants 守住「复核功能下线后不留空壳 agent」：
// 历史库里播种过 key=verifier 的「独立复核」agent，对话页会按 agent_key 解析出
// 这个名字并显示在顶部标题上。清理必须真的把它删掉，且可重复执行。
func TestRetireVerifierRemnants(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	td := t.TempDir()
	s := New(context.Background(), m, td, td, td)
	pg := s.m.pg
	if pg == nil {
		t.Skip("数据库未就绪")
	}

	// 复现历史安装：agent 行 + 一条提示词（循环外键，清理必须能断开）。
	tx, err := pg.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var agentID int64
	if err := tx.QueryRow(`INSERT INTO agents(key,name,description,role,builtin,enabled)
VALUES ($1,'独立复核','x','assistant',false,true) RETURNING id`, db.VerifierAgentKey).Scan(&agentID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("插入 verifier agent: %v", err)
	}
	var pid int64
	if err := tx.QueryRow(`INSERT INTO agent_prompts(agent_id,version,template_text,note,updated_by)
VALUES ($1,1,'p','内置默认','system') RETURNING id`, agentID).Scan(&pid); err != nil {
		_ = tx.Rollback()
		t.Fatalf("插入 prompt: %v", err)
	}
	if _, err := tx.Exec(`UPDATE agents SET current_prompt_id=$1 WHERE id=$2`, pid, agentID); err != nil {
		_ = tx.Rollback()
		t.Fatalf("绑定 current_prompt_id: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// 清掉标记，让清理真正执行（New 里可能已经跑过一次）。
	_, _ = pg.Exec(`DELETE FROM settings WHERE key='verifier_retired_v1'`)

	s.retireVerifierRemnants()

	cur, err := pg.GetAgentByKey(db.VerifierAgentKey)
	if err != nil {
		t.Fatalf("查询 agent: %v", err)
	}
	if cur != nil {
		t.Fatalf("verifier agent 应已被删除，仍存在：%+v", cur)
	}

	// 幂等：再跑一次不报错、也不影响其它 agent。
	s.retireVerifierRemnants()
	if again, _ := pg.GetAgentByKey(db.VerifierAgentKey); again != nil {
		t.Fatalf("重复清理不应把 agent 变回来")
	}
}
