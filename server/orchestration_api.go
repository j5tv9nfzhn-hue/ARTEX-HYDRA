package server

// 运行时编排模板 + 复测入口的归属声明。
//
// 为什么模板要单独一个端点，而不是塞进 GET /api/agents：
//
//	对齐 STRIX —— 子代理不是「清单里的角色」，而是根 agent 在任务里按
//	name + task + skills 临时造出来的。所以前端不该有一份「专家列表」，
//	只该有一份「根与子代理的系统提示词模板 + 共用的运行配置」。
//
// 具体表现：这四条 agents 行（coord_root / coord_child / worker / goals）
// 仍在库里（提示词要可编辑、可恢复默认），但 pgListAgents 会把它们过滤掉，
// 由本端点单独输出。编辑入口复用既有的 /api/agents/{key} 系列，前端不用改协议。
//
// 复测（retester）同理：它是**从漏洞详情页手动发起的一次性实例**，不是可选角色。
// 声明成 untouchable 后，它既不出现在清单里，也不给改 / 删——库里那一行只作为
// 会话的执行配置载体（与 coord_child 一样是「机制」，不是「选项」）。

import (
	"net/http"

	"github.com/Autumn-27/artex/db"
)

// untouchableAgentKeys 是既不进清单、也不给改删的「机制型」agent 行。
// 与 orchestrationAgentKeys 的区别只是：模板可以点进去编辑提示词，复测不行。
var untouchableAgentKeys = map[string]bool{
	db.FindingRetestAgentKey: true,
}

// hiddenFromList 报告该 agent 行是否不应出现在 /api/agents 清单里。
func hiddenFromList(key string) bool {
	return orchestrationAgentKeys[key] || untouchableAgentKeys[key]
}

// pgListOrchestration 返回运行时模板：根、子代理、共用运行配置、一次性目标拆解。
//
// 每条带 description 与 role 字段，前端按 key 分组渲染；不返回自定义 agent。
func (s *Server) pgListOrchestration(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	ags, err := pg.ListAgents()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	kept := make([]*db.Agent, 0, len(orchestrationAgentKeys))
	for _, a := range ags {
		if orchestrationAgentKeys[a.Key] {
			kept = append(kept, a)
		}
	}
	dtos := agentDTOs(kept)
	if mcp, skill, tools, err := pg.AgentBindingCounts(); err == nil {
		for i := range dtos {
			dtos[i].McpCount = mcp[kept[i].ID]
			dtos[i].SkillCount = skill[kept[i].ID]
			dtos[i].ToolCount = tools[kept[i].Key]
		}
	}
	writeJSON(w, 200, map[string]any{"agents": dtos})
}
