package server

// *Server 满足 CoordAgents 的薄桥（8 行）。
//
// 为什么不直接把 agentMaxTurns / agentRunSeconds 改成导出：它们在包内被 20+ 处
// 调用，改名只为满足一个新接口属于本末倒置。两个转发方法成本为零，也不会
// 让旧代码产生任何 diff。

func (s *Server) AgentMaxTurns(key string) int { return s.agentMaxTurns(key) }

func (s *Server) AgentRunSeconds(key string) int { return s.agentRunSeconds(key) }
