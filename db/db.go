// Package db is the SQLite data source for ARTEX. It opens the database file,
// applies the schema, and seeds built-in agents and the variable catalog.
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"io/fs"
	"sync"

	"github.com/Autumn-27/artex/config"
	"github.com/Autumn-27/artex/mcpbuiltin"
	"github.com/Autumn-27/artex/skills"

	_ "modernc.org/sqlite"
)

//go:embed schema_sqlite.sql
var schemaSQLiteSQL string

// DSN resolves the SQLite connection string and reports where it came from.
//
// There is no configuration that can be missing: the database is a file next to
// the config file, created on first open. source is retained for the startup log line so an operator can see which
// path was used.
func DSN() (dsn, source string, err error) {
	return config.SQLiteDSN(),
		"SQLite（" + config.SQLitePathSource() + "，" + config.SQLitePath() + "）", nil
}

// DB wraps the shared *sql.DB.
type DB struct {
	*sql.DB
	// rd 是只读连接池（query_only=ON）。读走它，写只在主池上发生——这样「误把写语句
	// 放到读路径」会当场报 readonly database，而不是悄悄抢写锁。为 nil 时回落到主池
	// （内存库/测试构造的句柄）。
	rd *sql.DB
	// wq 是单写协程（见 db/writer.go）。懒初始化：测试里直接 &DB{DB: raw} 构造的
	// 句柄也能用，不必依赖 openSQLite。
	wqMu sync.Mutex
	wq   *writeQueue
	// path 是主库文件路径（内存库为空）。WAL 状态查询需要按路径去看 -wal 文件
	// 大小，而 sql.DB 本身不暴露 DSN——所以打开时记下来。
	path string
}

// ReadDB 返回只读连接池；没有（内存库/测试句柄）时返回主池。
func (d *DB) ReadDB() *sql.DB {
	if d.rd != nil {
		return d.rd
	}
	return d.DB
}

// Close 先关只读池再关主池，避免文件句柄比连接活得久。
func (d *DB) Close() error {
	if d.rd != nil {
		_ = d.rd.Close()
	}
	return d.DB.Close()
}

// Open connects, applies the schema (idempotent), and seeds builtin rows.
//
// The DSN is accepted rather than resolved internally so the many call sites that
// do DSN() → Open(dsn) keep working unchanged; it must be a SQLite URI.
func Open(dsn string) (*DB, error) {
	if !isSQLiteDSN(dsn) {
		return nil, fmt.Errorf("unsupported database DSN %q：ARTEX 仅支持 SQLite（file:…）", config.Redact(dsn))
	}
	return openSQLite(dsn)
}

// builtinAgent describes one of the fixed agents and its prompt-variable catalog.
type builtinAgent struct {
	key, name, role, desc string
	vars                  []promptVar
	interactiveShell      bool // 建行时的默认交互式 shell 开关；ON CONFLICT 不覆盖用户后续手动开关
	runSeconds            *int // 建行时的单次 run 墙钟上限(秒)；nil=用种子默认(1200)，0=不限时
}

type promptVar struct{ name, desc, example, source string }

// intp 返回 v 的指针，用于给 builtinAgent 可选字段(如 runSeconds)显式取值。
func intp(v int) *int { return &v }

// builtinAgents mirrors docs §5(a). 内置工具不入库；这里只 seed agent + 变量目录。
//
// 新调度层（coord）只有一个根 Agent，子代理由根在任务里临时派发，因此这里
// **不再 seed 任何内置角色**：planner / mainagent / reviewer / auto / pentest
// 都已下线（前三个的源码是 //go:build decommissioned 桩，后两个的对话入口
// 已并入根 Agent）。保留下来的两条不是「角色」：
//
//	worker —— DefaultCoordRootAgentKey，根 Agent 与子代理共用的运行配置载体
//	          （模型档位 / 轮次 / 工具绑定）与提示词挂载点，必须有库行。
//	goals  —— 建任务时那一次 LLM 目标拆解，不是常驻角色。
//
// 旧行由 pruneLegacyAgents 在启动时清掉，避免它们继续出现在 Agent 页。
var builtinAgents = []builtinAgent{
	{"goals", "目标拆解", "goals", "把渗透任务目标拆解成若干独立、可验证的子目标。", []promptVar{
		{"EngagementDescription", "任务描述（测试对象/背景）", "测试 example.com 站点", "exploration"},
		// Now 是全局 runtime 变量(见 server.globalPromptVars),不再在各 agent 目录里
		// 重复定义,否则 withGlobalVars 追加时会与全局项撞名。
	}, false, nil},
	// worker 的 key 被 agent.DefaultCoordRootAgentKey 引用，不能改名；
	// 显示名改成「根 Agent」，避免和旧版「执行」角色混淆。
	{"worker", "根 Agent", "worker", "根 Agent 与子代理共用的运行配置（模型档位/轮次/工具绑定）与提示词载体。", []promptVar{
		{"ProxyAddr", "记录代理地址(驱动 if 双文案)", "127.0.0.1:8080", "runtime"},
		{"WorkerName", "worker 自我标识(可选)", "worker-1", "runtime"},
	}, false, nil},
}

// legacyAgentKeys 是已下线、但旧库里可能仍留有行的内置角色。
// 启动时按这个名单清库，让 Agent 页只剩当前世代。
var legacyAgentKeys = []string{"planner", "mainagent", "reviewer", "auto", "pentest"}

// pruneLegacyAgents 删除已下线内置角色的库行（含提示词、变量目录、工具/技能绑定）。
// 用 settings 标记做成一次性迁移：用户若手工把某个 key 建回来（自定义 agent 同名），
// 后续启动不会再被删掉。
func (d *DB) pruneLegacyAgents() error {
	if v, _, _ := d.GetSetting("agent_prune_legacy_v1"); v == "true" {
		return nil
	}
	for _, key := range legacyAgentKeys {
		if _, err := d.Exec(`DELETE FROM agents WHERE key = $1 AND builtin`, key); err != nil {
			return fmt.Errorf("prune legacy agent %s: %w", key, err)
		}
	}
	// agent_prompts / agent_prompt_vars 靠 ON DELETE CASCADE 跟着走；外键开关
	// 关掉时（历史库）会留下孤儿行，这里兜一次底，避免提示词页出现无主的版本。
	if _, err := d.Exec(`DELETE FROM agent_prompts WHERE agent_id NOT IN (SELECT id FROM agents)`); err != nil {
		return fmt.Errorf("prune orphan agent prompts: %w", err)
	}
	if _, err := d.Exec(`DELETE FROM agent_prompt_vars WHERE agent_id NOT IN (SELECT id FROM agents)`); err != nil {
		return fmt.Errorf("prune orphan agent vars: %w", err)
	}
	return d.SetSetting("agent_prune_legacy_v1", "true")
}

// seedBuiltins inserts the fixed built-in agents and their variable catalog (idempotent).
func (d *DB) seedBuiltins() error {
	for _, a := range builtinAgents {
		var agentID int64
		err := d.QueryRow(`
INSERT INTO agents(key, name, description, role, builtin, enabled, interactive_shell, run_seconds)
VALUES ($1, $2, NULLIF($3,''), $4, true, true, $5, COALESCE($6, 1200))
ON CONFLICT (key) DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description
RETURNING id`, a.key, a.name, a.desc, a.role, a.interactiveShell, a.runSeconds).Scan(&agentID)
		if err != nil {
			return fmt.Errorf("agent %s: %w", a.key, err)
		}
		for _, v := range a.vars {
			if _, err := d.Exec(`
INSERT INTO agent_prompt_vars(agent_id, var_name, description, example, source)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (agent_id, var_name) DO UPDATE
  SET description = EXCLUDED.description, example = EXCLUDED.example, source = EXCLUDED.source`,
				agentID, v.name, v.desc, v.example, v.source); err != nil {
				return fmt.Errorf("agent %s var %s: %w", a.key, v.name, err)
			}
		}
	}
	// Drop catalog entries for variables that were renamed, so the white-list no
	// longer advertises a name templates can't resolve (EngagementTitle→Description).
	// 'Now' 从各 agent 目录提升为全局 runtime 变量后,旧库里 goals 仍残留一条 'Now'
	// 会与全局项撞名(前端变量列表 key 重复);一并清掉。
	if _, err := d.Exec(`DELETE FROM agent_prompt_vars WHERE var_name IN ('EngagementTitle', 'CoverageGaps', 'Now')`); err != nil {
		return fmt.Errorf("cleanup renamed vars: %w", err)
	}
	// Default-on interactive_shell for the runtime agents (planner/worker/mainagent/auto)
	// ONCE — respects a later user toggle-off (guarded by a settings flag). goals(one-shot
	// decomposer) stays off. Runs after the column exists (schema applied before seed).
	if v, _, _ := d.GetSetting("interactive_shell_default_v1"); v != "true" {
		if _, err := d.Exec(`UPDATE agents SET interactive_shell=true WHERE key = 'worker'`); err != nil {
			return fmt.Errorf("seed interactive_shell defaults: %w", err)
		}
		_ = d.SetSetting("interactive_shell_default_v1", "true")
	}
	// 内置浏览器 MCP（npx @playwright/mcp）已下线：它驱动的是 Chromium，而无头
	// Chromium 会在 sec-ch-ua 里暴露 HeadlessChrome，正是最容易被拦的形态。
	// 浏览器自动化统一走系统浏览器 CDP（L1），这里不再自动注册任何 Chromium 系浏览器。
	// Seed placeholder REA (Reverse Engineering Assistant) 与 WPE64 封包实验 MCP
	// 行 —— 默认禁用、stdio、command 留空（操作员在 /system/mcp 填启动器）。
	// REA 供 software_re REProvider 预设绑定；WPE64 与内置 mitmproxy 记录代理
	// 协同（skills/wpe64-mcp + skills/mitmproxy-traffic）。仅在缺失时插入，不覆盖
	// 用户后续对 args/env/enabled/可见性的修改。
	if _, err := d.Exec(`
INSERT INTO mcp_servers(name, transport, command, args, env, enabled)
VALUES ('rea', 'stdio', NULL, '[]', '{}', false)
ON CONFLICT (name) DO NOTHING`); err != nil {
		return fmt.Errorf("seed rea mcp: %w", err)
	}
	if _, err := d.Exec(`
INSERT INTO mcp_servers(name, transport, command, args, env, enabled)
VALUES ('wpe64', 'stdio', NULL, '[]', '{}', false)
ON CONFLICT (name) DO NOTHING`); err != nil {
		return fmt.Errorf("seed wpe64 mcp: %w", err)
	}
	// Seed Go 内置 MCP 服务器（mcpbuiltin）：transport=builtin 表示由本进程实现，
	// 不启子进程、不依赖 Node/Python/容器。默认禁用——启用即用，无需安装任何东西。
	for _, name := range mcpbuiltin.Names() {
		args := fmt.Sprintf(`["%s"]`, name)
		if _, err := d.Exec(`
INSERT INTO mcp_servers(name, transport, command, args, env, enabled)
VALUES ($1, 'stdio', $2, $3, '{}', false)
ON CONFLICT (name) DO NOTHING`, name, mcpbuiltin.Command, args); err != nil {
			return fmt.Errorf("seed builtin mcp %s: %w", name, err)
		}
	}
	// NOTE: the placeholder ScopeSentry data-source MCP (empty URL + empty X-API-Key,
	// disabled) is seeded directly in schema_sqlite.sql §F so a raw `psql < schema_sqlite.sql` init
	// also gets it. schema_sqlite.sql is Exec'd on every startup, so it stays idempotent.
	if err := d.seedBuiltinMCPServersAndTools(); err != nil {
		return fmt.Errorf("seed builtin mcp tools: %w", err)
	}
	if err := d.seedBuiltinSkillVisibility(); err != nil {
		return fmt.Errorf("seed skill visibility: %w", err)
	}
	if err := d.seedDefaultInterceptRules(); err != nil {
		return fmt.Errorf("seed intercept rules: %w", err)
	}
	if err := d.seedDefaultInterceptRulesV2(); err != nil {
		return fmt.Errorf("seed intercept rules v2: %w", err)
	}
	if err := d.seedDefaultInterceptRulesV3(); err != nil {
		return fmt.Errorf("seed intercept rules v3: %w", err)
	}
	if err := d.seedDefaultAssetInterceptRules(); err != nil {
		return fmt.Errorf("seed asset intercept rules: %w", err)
	}
	// 最后清旧角色行：必须在 builtinAgents 播种之后，否则 ON CONFLICT 会把
	// 已删除的旧 key 当成「不存在」而重建（这里本来也不再 seed 它们）。
	if err := d.pruneLegacyAgents(); err != nil {
		return err
	}
	return nil
}

// seedDefaultAssetInterceptRules inserts the built-in asset blocklist (fuzzy
// domain matches for government / education sites) once on first startup. Gated
// by a settings flag so a user's later disable/delete is never resurrected on
// restart — same policy as the intercept-rule seed.
func (d *DB) seedDefaultAssetInterceptRules() error {
	if v, _, _ := d.GetSetting("asset_intercept_default_rules_v1"); v == "done" {
		return nil
	}
	rules := []struct {
		kind    string
		pattern string
		note    string
	}{
		{"fuzzy_domain", ".gov", "[内置] 政府网站 (.gov)"},
		{"fuzzy_domain", ".gov.cn", "[内置] 政府网站 (.gov.cn)"},
		{"fuzzy_domain", ".edu", "[内置] 教育网站 (.edu)"},
		{"fuzzy_domain", ".edu.cn", "[内置] 教育网站 (.edu.cn)"},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO asset_intercept_rules(enabled, kind, pattern, note, builtin)
VALUES (true, $1, $2, $3, true)
ON CONFLICT DO NOTHING`, r.kind, r.pattern, r.note); err != nil {
			return fmt.Errorf("asset rule %q: %w", r.pattern, err)
		}
	}
	return d.SetSetting("asset_intercept_default_rules_v1", "done")
}

// builtinSkillAgentKeys 是内置技能包默认可见的内置 Agent。
// 现在只剩根 Agent 的运行配置行（key=worker）；旧四角色与 auto/pentest 已下线。
var builtinSkillAgentKeys = []string{"worker"}

// builtinSkillNames 列出随二进制下发的内置技能包目录名。技能文件本身在文件系统上
// （SkillDir，由 SDK 加载），DB 只存可见性绑定。
//
// 直接读 skills.Bundled（//go:embed 的内嵌目录），不维护手写名单——内置库此前整包
// 拷贝上游 818 个技能，现已整体换成 usestrix/strix 的 75 个技能，名单随内嵌目录走，
// 换库不需要同步改这里。
func builtinSkillNames() []string {
	entries, err := fs.ReadDir(skills.Bundled, ".")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names
}

const upsertSkillVisibilitySQL = `INSERT INTO agent_skill_visibility(agent_id, skill_name, enabled)
SELECT id, $2, true FROM agents WHERE key=$1
ON CONFLICT (agent_id, skill_name) DO NOTHING`

// seedBuiltinSkillVisibility 为内置技能包播种默认可见性。按内置目录动态推导，
// 不再维护手写名单；Insert-if-absent（ON CONFLICT DO NOTHING）保证用户之后关掉的
// 开关不会被重启复活。用设置项做一次性闸门，避免每次启动跑数千条 INSERT。
func (d *DB) seedBuiltinSkillVisibility() error {
	if v, _, _ := d.GetSetting("builtin_skill_visibility_v3"); v == "done" {
		return nil
	}
	for _, skillName := range builtinSkillNames() {
		for _, key := range builtinSkillAgentKeys {
			if _, err := d.Exec(upsertSkillVisibilitySQL, key, skillName); err != nil {
				return fmt.Errorf("skill %s → agent %s: %w", skillName, key, err)
			}
		}
	}
	return d.SetSetting("builtin_skill_visibility_v3", "done")
}

// seedDefaultInterceptRules inserts built-in safety intercept rules once on
// first startup. The seed is gated by a settings flag so user edits (disable,
// delete, re-order) are never overwritten on subsequent restarts.
func (d *DB) seedDefaultInterceptRules() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v1"); v == "done" {
		return nil
	}
	type rule struct {
		name     string
		target   string // tool_name | tool_input
		typ      string // string | regex
		pattern  string
		action   string
		message  string
		priority int
	}
	rules := []rule{
		// ── 系统破坏性命令 (priority 100) ──────────────────────────────────
		{
			name:     "[内置] 递归强制删除 rm -rf",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\brm\b.{0,80}(?:-[a-z]*r[a-z]*f[a-z]*|-[a-z]*f[a-z]*r[a-z]*|--recursive|--no-preserve-root)`,
			action:   "deny",
			message:  "禁止执行递归强制删除（rm -rf / rm --recursive），可能永久损坏系统或靶机环境",
			priority: 100,
		},
		{
			name:     "[内置] 删除系统关键目录",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\brm\b[^"'\n]{0,60}["'\s](/|/etc|/bin|/usr|/boot|/var|/lib|/sys|/proc|/dev|/sbin|/root)`,
			action:   "deny",
			message:  "禁止删除系统关键路径",
			priority: 100,
		},
		{
			name:     "[内置] 磁盘格式化 mkfs",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\bmkfs\b`,
			action:   "deny",
			message:  "禁止格式化磁盘（mkfs）",
			priority: 100,
		},
		{
			name:     "[内置] 覆写磁盘设备 dd",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\bdd\b[^|\n]{0,100}\bof=\s*/dev/[a-zA-Z]`,
			action:   "deny",
			message:  "禁止使用 dd 覆写磁盘设备",
			priority: 100,
		},
		{
			name:     "[内置] Fork 炸弹",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `:\(\)\s*\{[^}]*:\|:`,
			action:   "deny",
			message:  "禁止执行 Fork 炸弹",
			priority: 100,
		},
		{
			name:     "[内置] 关机 / 重启",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\b(?:shutdown|reboot|halt|poweroff|init\s+[06])\b`,
			action:   "deny",
			message:  "禁止执行关机或重启命令",
			priority: 100,
		},
		{
			name:     "[内置] 杀死全部进程",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\bkill\s+-9\s+-1\b|\bkillall\s+-9\b`,
			action:   "deny",
			message:  "禁止 kill -9 -1 或 killall -9（杀死所有进程）",
			priority: 100,
		},
		{
			name:     "[内置] 磁盘擦除 shred / wipe",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\b(?:shred|wipe)\b[^|\n]{0,80}/dev/[a-zA-Z]`,
			action:   "deny",
			message:  "禁止对磁盘设备执行 shred/wipe 擦除",
			priority: 100,
		},
		{
			name:     "[内置] 清空防火墙规则",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `\biptables\s+(?:-F|--flush)\b|\bnft\s+flush\s+ruleset\b`,
			action:   "deny",
			message:  "禁止清空防火墙规则（iptables -F / nft flush）",
			priority: 100,
		},
		// ── 数据库破坏性操作 (priority 90) ─────────────────────────────────
		{
			name:     "[内置] SQL DROP DATABASE / TABLE / SCHEMA",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bDROP\s+(?:DATABASE|TABLE|SCHEMA|INDEX|VIEW|TABLESPACE|USER|ROLE)\b`,
			action:   "deny",
			message:  "禁止执行 DROP 操作，可能不可逆地销毁数据库对象",
			priority: 90,
		},
		{
			name:     "[内置] SQL TRUNCATE",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bTRUNCATE\s+(?:TABLE\s+)?\w`,
			action:   "deny",
			message:  "禁止执行 TRUNCATE，可能清空数据表所有数据",
			priority: 90,
		},
		{
			name:     "[内置] MongoDB drop / dropDatabase",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\.(?:dropDatabase|dropCollection|drop)\s*\(`,
			action:   "deny",
			message:  "禁止执行 MongoDB drop 操作",
			priority: 90,
		},
		{
			name:     "[内置] Redis FLUSHALL / FLUSHDB",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\b(?:FLUSHALL|FLUSHDB)\b`,
			action:   "deny",
			message:  "禁止执行 Redis FLUSHALL / FLUSHDB，可能清空全部缓存数据",
			priority: 90,
		},
		// ── HTTP 破坏性请求 (priority 80) ──────────────────────────────────
		// Agent 发送 DELETE 请求的三种常见方式：
		//   1. curl -X DELETE / --request DELETE（Bash 工具直接执行或写入脚本）
		//   2. Python HTTP 客户端 .delete() 方法
		//   3. JS/通用脚本里的 method: 'DELETE' / method="DELETE"
		{
			name:     "[内置] curl / wget 发送 DELETE 请求",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\bcurl\b[^|\n&;"]{0,300}(?:-X\s*DELETE|--request\s+DELETE|-XDELETE)|\bwget\b[^|\n&;"]{0,300}--method[=\s]+DELETE`,
			action:   "deny",
			message:  "禁止通过 curl/wget 发送 HTTP DELETE 请求，可能删除目标系统数据",
			priority: 80,
		},
		{
			name:     "[内置] Python HTTP 客户端 DELETE（requests/httpx/aiohttp）",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)\b(?:requests|httpx|aiohttp|urllib\.request)\.delete\s*\(|session\.delete\s*\(|client\.delete\s*\(`,
			action:   "deny",
			message:  "禁止使用 Python HTTP 客户端发送 DELETE 请求",
			priority: 80,
		},
		{
			name:     "[内置] 脚本中声明 HTTP DELETE 方法（JS/通用）",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)axios\.delete\s*\(|method\s*[:=]\s*['"]DELETE['"]`,
			action:   "deny",
			message:  "禁止在脚本中声明并发送 HTTP DELETE 请求",
			priority: 80,
		},
		{
			name:     "[内置] 批量清空 / 清除接口路径",
			target:   "tool_input",
			typ:      "regex",
			pattern:  `(?i)/(?:clear|wipe|flush|purge|truncate|drop|destroy|factory[-_]reset|reset[-_]all)(?:[/?#"'\s]|$)`,
			action:   "deny",
			message:  "禁止调用批量清空或销毁类接口（/clear /wipe /flush /purge 等）",
			priority: 80,
		},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
VALUES ($1, true, $2, $3, $4, $5, $6, $7, false, 60, 'deny')
ON CONFLICT DO NOTHING`,
			r.name, r.priority, r.target, r.typ, r.pattern, r.action, r.message,
		); err != nil {
			return fmt.Errorf("rule %q: %w", r.name, err)
		}
	}
	return d.SetSetting("intercept_default_rules_v1", "done")
}

// seedDefaultInterceptRulesV2 migrates the two safety patterns that used to be
// hard-coded in guard.go (destructive shell + data-exfil pipe) into ordinary
// intercept rules. Gated by its own flag so it also lands on DBs that already ran
// v1. Unlike the old guard.go floor, these are plain [内置] rules — the user can
// disable or delete them. The exfil rule ships DISABLED by default (its
// curl/wget/nc pipe pattern mis-fires on legitimate CTF/pentest reverse-shell and
// data-transfer pipes); enable it manually when exfil gating is actually wanted.
func (d *DB) seedDefaultInterceptRulesV2() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v2"); v == "done" {
		return nil
	}
	rules := []struct {
		name     string
		pattern  string
		action   string
		message  string
		enabled  bool
		priority int
	}{
		{
			name:     "[内置] 破坏性系统命令",
			pattern:  `(?i)\b(rm\s+-rf\s+/|mkfs|dd\s+if=|:\(\)\s*\{|shutdown|reboot|>\s*/dev/sd)`,
			action:   "deny",
			message:  "破坏性命令被拒绝（rm -rf / / mkfs / dd / fork bomb / 关机重启 / 覆写磁盘设备）",
			enabled:  true,
			priority: 100,
		},
		{
			name:     "[内置] 数据外泄管道",
			pattern:  `(?i)(curl|wget|nc|ncat)\b[^|]*\b(\|\s*(curl|wget|nc))`,
			action:   "deny",
			message:  "疑似数据外泄管道被拒绝（命令输出经 curl/wget/nc 外传）",
			enabled:  false,
			priority: 80,
		},
	}
	for _, r := range rules {
		if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
VALUES ($1, $2, $3, 'tool_input', 'regex', $4, $5, $6, false, 60, 'deny')
ON CONFLICT DO NOTHING`,
			r.name, r.enabled, r.priority, r.pattern, r.action, r.message,
		); err != nil {
			return fmt.Errorf("rule %q: %w", r.name, err)
		}
	}
	return d.SetSetting("intercept_default_rules_v2", "done")
}

// seedDefaultInterceptRulesV3 adds the delete-endpoint path rule. The v1 HTTP rules
// only catch the DELETE *method* (curl -X DELETE, requests.delete(, method:'DELETE'),
// and v1's path rule covers only /clear /wipe /flush /purge /truncate /drop /destroy
// /factory-reset /reset-all — so a plain `curl 'http://t/api/user/delete?id=1'` (a
// delete endpoint reached with GET/POST, which is how most web apps expose deletion)
// slipped through every built-in rule. Own flag so it also lands on DBs that already
// ran v1/v2, where editing the v1 seed would have no effect.
//
// The pattern deliberately requires a separator after the verb so /delivery,
// /details, /delta and /delegate do not match, while /deleteAll, /delete_user and
// /delete-user do. destroy is re-covered here because v1's rule does not allow a
// suffix (/destroyAll was missed).
//
// Exported as a package const only so the seeded regex is unit-testable without a DB.
const deleteEndpointPathPattern = `(?i)/(?:(?:delete|remove|unlink|erase|destroy)[-\w]*|del)(?:[/?#"'\s]|$)`

func (d *DB) seedDefaultInterceptRulesV3() error {
	if v, _, _ := d.GetSetting("intercept_default_rules_v3"); v == "done" {
		return nil
	}
	const name = "[内置] 删除类接口路径"
	if _, err := d.Exec(`
INSERT INTO intercept_rules(name, enabled, priority, match_target, match_type, pattern, action, message, timeout_enabled, timeout_seconds, timeout_action)
SELECT $1, true, 80, 'tool_input', 'regex', $2, 'deny', $3, false, 60, 'deny'
WHERE NOT EXISTS (SELECT 1 FROM intercept_rules WHERE name = $1)`,
		name,
		deleteEndpointPathPattern,
		"禁止调用删除类接口（/delete /remove /unlink /erase 等），不论使用哪种 HTTP 方法——多数应用的删除接口用 GET/POST 就能触发，同样会真实删除目标数据",
	); err != nil {
		return fmt.Errorf("rule %q: %w", name, err)
	}
	return d.SetSetting("intercept_default_rules_v3", "done")
}

// advisoryLock is a no-op, and deliberately so.
//
// These calls exist to serialise mutations that span several tables against each
// other on a server running many goroutines against one database. SQLite already
// provides that guarantee structurally: the whole file is locked for the duration
// of a write transaction (see the _txlock=immediate note in config.SQLiteDSN), so
// two concurrent multi-table writes cannot interleave. The function is kept as a
// named extension point so call sites still document *why* the mutation needs to
// be atomic rather than merely *that* it is.
func advisoryLock(context.Context, *sql.Tx, int64) error { return nil }

// AdvisoryLock is the exported face of advisoryLock for callers outside this
// package that build their own transactions (server/finding_retests.go's seed
// migration, for one).
func AdvisoryLock(ctx context.Context, tx *sql.Tx, key int64) error {
	return advisoryLock(ctx, tx, key)
}

// coordinateWithSchemaMigration marks long, multi-table archive transactions
// that must not race startup DDL. On SQLite the concern disappears for the same
// reason as advisoryLock: the write transaction holds the file lock, and the
// schema is applied on the single connection that opens the database.
func coordinateWithSchemaMigration(tx *sql.Tx) error {
	return advisoryLock(context.Background(), tx, 0)
}

// queryRower is the shared surface of *sql.DB and *sql.Tx, so scalar helpers
// work both inside and outside a transaction.
type queryRower interface {
	QueryRow(query string, args ...any) *sql.Row
}

// tableExists reports whether a SQLite table is present.
//
// It exists for the leftovers of decommissioned tables: reviewer_verdicts and
// reviewer_evidence had their CREATE TABLE dropped from schema_sqlite.sql, but
// the delete cascade and the archive purge still DELETE from them. Databases
// upgraded from an older build keep both tables (retained as audit data), where
// dropping the statements would let orphan rows pile up; databases created from
// the current schema have no such tables, where keeping them makes the DELETE
// fail with "no such table" and roll the whole transaction back. So the callers
// fork on existence instead of choosing one shape.
func tableExists(q queryRower, table string) bool {
	var n int
	if err := q.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=$1`, table).Scan(&n); err != nil {
		return false
	}
	return n > 0
}
