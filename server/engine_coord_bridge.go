package server

// *Server 满足 CoordAgents 的薄桥。
//
// 为什么不直接把 agentMaxTurns / agentRunSeconds 改成导出：它们在包内被 20+ 处
// 调用，改名只为满足一个新接口属于本末倒置。两个转发方法成本为零，也不会
// 让旧代码产生任何 diff。

func (s *Server) AgentMaxTurns(key string) int { return s.agentMaxTurns(key) }

func (s *Server) AgentRunSeconds(key string) int { return s.agentRunSeconds(key) }

func (s *Server) AgentsForTask(t *Task) *taskAgentBundle { return s.agentsForTask(t) }

// AgentSlots 是「子代理并发上限」（settings.workers）；0 视为未设置，由
// Engine.coordAgentSlots 兜底。对齐 STRIX：派几个由根 agent 自决，这里只兜资源。
func (s *Server) AgentSlots() int {
	if s == nil || s.m == nil {
		return 0
	}
	return s.m.Workers()
}
