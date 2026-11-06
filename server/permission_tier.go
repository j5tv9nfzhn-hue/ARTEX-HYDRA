package server

// 全局权限档位（只读 / 标准 / 完全访问）的读写入口。
//
// 档位的执行点在 agent 侧：agent.permissionModeOrDefault 每次装配 agent 时读
// CurrentPermissionTier()，所以这里改完立即对流新生效，不需要重启进程，也不需要
// 重建 LLM/工具面。持久化走 settings 表（与活动保留策略同一套 KV），不加列。

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/Autumn-27/artex/agent"
)

func (s *Server) permissionTierRoutes(mux *http.ServeMux) {
	s.loadPermissionTier()
	mux.HandleFunc("GET /api/permission-tier", s.getPermissionTier)
	mux.HandleFunc("PUT /api/permission-tier", s.putPermissionTier)
}

func (s *Server) loadPermissionTier() {
	if s.m == nil || s.m.pg == nil {
		return
	}
	raw, ok, err := s.m.pg.GetSetting(agent.PermissionTierSetting)
	if err != nil {
		log.Printf("[permission] 读取权限档位失败，沿用默认 bypass: %v", err)
		return
	}
	if !ok || raw == "" {
		// 首启把默认档位写进库：默认值必须在设置页可见、可回显，而不是只活在
		// 代码常量里。否则运营者看到的是「未设置」，出问题时无法判断实际生效值。
		if err := s.m.pg.SetSetting(agent.PermissionTierSetting, string(agent.PermissionTierBypass)); err != nil {
			log.Printf("[permission] 写入默认权限档位失败（本次运行仍以 bypass 生效）: %v", err)
		}
		log.Printf("[permission] 未配置过权限档位，已落默认值：%s（完全访问）", agent.PermissionTierBypass)
		return
	}
	if err := agent.SetPermissionTier(raw); err != nil {
		log.Printf("[permission] 库里的权限档位非法(%q)，沿用默认 bypass", raw)
		return
	}
	log.Printf("[permission] 全局权限档位：%s（只读=%v）", agent.CurrentPermissionTier(), agent.CurrentPermissionTier().ReadOnly())
}

func (s *Server) getPermissionTier(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	tier := agent.CurrentPermissionTier()
	writeJSON(w, http.StatusOK, map[string]any{
		"tier":      string(tier),
		"read_only": tier.ReadOnly(),
		// default_tier 让前端把「完全访问」显示成默认选中项，而不是空白。
		"default_tier": string(agent.PermissionTierBypass),
		"options": []map[string]any{
			{"tier": string(agent.PermissionTierPlan), "label": "只读", "desc": "只允许只读工具；侦察与阅读可以做，任何写操作与命令执行都会被拒"},
			{"tier": string(agent.PermissionTierDefault), "label": "标准", "desc": "非只读工具需要命中 allow 规则；无人值守时可能表现为「只会读」"},
			{"tier": string(agent.PermissionTierBypass), "label": "完全访问", "desc": "规则之外一律放行；无人值守的默认档"},
		},
	})
}

func (s *Server) putPermissionTier(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var in struct {
		Tier string `json:"tier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体不是合法 JSON")
		return
	}
	if err := agent.SetPermissionTier(in.Tier); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	tier := agent.CurrentPermissionTier()
	if s.m != nil && s.m.pg != nil {
		if err := s.m.pg.SetSetting(agent.PermissionTierSetting, string(tier)); err != nil {
			log.Printf("[permission] 权限档位持久化失败（本次运行仍然生效）: %v", err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tier": string(tier), "read_only": tier.ReadOnly()})
}
