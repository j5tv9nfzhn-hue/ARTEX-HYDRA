package server

// 复核（verifier）功能的残留清理。
//
// 背景：独立复核曾是一条完整链路——播种一个 '独立复核' agent、漏洞入库时按阈值
// 自动派发、在漏洞详情里手动发起。后来工具提供者 findingVerifyTools() 被置空，
// 复核员变成"没有任何工具的 agent"，链路只剩两样东西还在跑：
//   1. 每次漏洞入库仍会 enqueue 一次复核 → 起一个空会话烧 token；
//   2. 系统设置里仍留着「自动复核闸门」，界面上看起来功能还在。
// 这两处已随功能一起删除，这里负责把**历史安装**里已经播种进库的空壳 agent 清掉：
// 否则对话页仍会按 agent_key 解析出「独立复核」这个已下线的角色名。
//
// 只删系统播种的那一行（key = verifier）。子表都是 ON DELETE CASCADE，
// 唯一需要先断开的是 agents.current_prompt_id → agent_prompts 的循环外键。

import (
	"log"

	"github.com/Autumn-27/artex/db"
)

func (s *Server) retireVerifierRemnants() {
	if s.m == nil || s.m.pg == nil {
		return
	}
	const flag = "verifier_retired_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	pg := s.m.pg
	// 先断开循环外键，再删 agent（prompts / visibility / skills 都是级联）。
	if _, err := pg.Exec(`UPDATE agents SET current_prompt_id=NULL WHERE key=$1`, db.VerifierAgentKey); err != nil {
		log.Printf("[verify] 清理解绑 current_prompt_id 失败: %v", err)
		return
	}
	if _, err := pg.Exec(`DELETE FROM agents WHERE key=$1`, db.VerifierAgentKey); err != nil {
		log.Printf("[verify] 删除复核残留 agent 失败: %v", err)
		return
	}
	// tools.agents 是 JSON 数组，不是外键，单独摘掉。
	_ = pg.RemoveAgentFromToolBindings(db.VerifierAgentKey)
	if err := pg.SetSetting(flag, "true"); err != nil {
		log.Printf("[verify] 写清理标记失败（下次启动会重试）: %v", err)
		return
	}
	log.Printf("[verify] 已下线独立复核：清掉历史播种的 verifier agent 与工具绑定")
}
