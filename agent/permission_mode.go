package agent

// 全局工具权限档位：把「这个系统允许 agent 做什么」从三处散落的
// permission.ModeBypass 字面量，收敛成一个可配置、可在运行时改的开关。
//
// 三档与 norma 的模式一一对应：
//
//	plan    只读（ModePlan）      —— 侦察与阅读，不落任何写操作
//	default 标准（ModeDefault）   —— 非只读工具需要 allow 规则命中
//	bypass  完全访问（ModeBypass）—— 规则之外一律放行（无人值守的默认）
//
// 默认保持 bypass：这台机器上没有人在旁边点「允许」，退到 ModeDefault 会让
// Bash/Write 这类真正干活的工具全部被拒（见 runner_coord.go 的说明）。
// 档位只决定"能不能执行"，不改变工具面：能看见哪些工具仍由 ToolResolve 的
// 角色绑定决定，两者互不替代。

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"github.com/Autumn-27/norma/permission"
)

// PermissionTier 是全局权限档位。
type PermissionTier string

const (
	PermissionTierPlan    PermissionTier = "plan"
	PermissionTierDefault PermissionTier = "default"
	PermissionTierBypass  PermissionTier = "bypass"
)

// PermissionTierSetting 是持久化到 settings 表的键名（与 db.GetSetting 同源）。
const PermissionTierSetting = "permission_tier"

var currentPermissionTier atomic.Value

func init() {
	currentPermissionTier.Store(PermissionTierBypass)
}

// ParsePermissionTier 解析档位字符串；"readonly"/"read-only" 也认作 plan。
func ParsePermissionTier(s string) (PermissionTier, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "plan", "readonly", "read-only", "只读":
		return PermissionTierPlan, true
	case "default", "standard", "标准":
		return PermissionTierDefault, true
	case "bypass", "full", "完全访问":
		return PermissionTierBypass, true
	}
	return "", false
}

// SetPermissionTier 设置全局档位。非法值返回错误且不改变现状。
func SetPermissionTier(s string) error {
	t, ok := ParsePermissionTier(s)
	if !ok {
		return fmt.Errorf("未知的权限档位 %q（可选 plan | default | bypass）", s)
	}
	currentPermissionTier.Store(t)
	return nil
}

// CurrentPermissionTier 返回当前生效档位。优先级：
// ARTEX_PERMISSION_TIER 环境变量 > SetPermissionTier 写入值 > bypass。
func CurrentPermissionTier() PermissionTier {
	if raw := strings.TrimSpace(os.Getenv("ARTEX_PERMISSION_TIER")); raw != "" {
		if t, ok := ParsePermissionTier(raw); ok {
			return t
		}
	}
	if t, ok := currentPermissionTier.Load().(PermissionTier); ok && t != "" {
		return t
	}
	return PermissionTierBypass
}

// Mode 把档位映射成 norma 的权限模式。
func (t PermissionTier) Mode() permission.Mode {
	switch t {
	case PermissionTierPlan:
		return permission.ModePlan
	case PermissionTierDefault:
		return permission.ModeDefault
	default:
		return permission.ModeBypass
	}
}

// ReadOnly 表示该档位不允许任何写操作。
func (t PermissionTier) ReadOnly() bool { return t == PermissionTierPlan }

// EffectivePermissionMode 是**所有**装配路径应当采用的权限模式。
//
// 之所以要这个函数而不是各处直接写 permission.ModeBypass：五处硬编码字面量会让
// 全局档位形同虚设——用户在系统设置里选「只读」，对话页与任务链仍然全量放行，
// 而设置页看起来又是生效的。默认值（完全访问）与可配置性必须在同一条路径上，
// 否则"默认"就只是碰巧等于硬编码。
func EffectivePermissionMode() permission.Mode {
	return CurrentPermissionTier().Mode()
}
