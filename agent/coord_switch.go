package agent

// 调度层切换开关。
//
// 重构期间新旧两条链路必须能同时在仓库里存在且可毫秒级切换：旧链路是唯一
// 经过真实联调的路径，新链路是新写的。没有开关就意味着"要么全信新的，要么
// 全删旧的"，这在禁止编译/测试的红线下是不可接受的赌注。
//
// 开关是**进程级**而不是任务级：同一进程里混跑两种调度会让"谁负责派发"这个
// 问题失去唯一答案，而唯一答案是整个设计的基石（见 coordinator.go）。

import (
	"os"
	"strings"
)

// SchedMode 是调度层的工作模式。
type SchedMode string

const (
	// SchedLegacy 是旧的「Planner + Reviewer + frontier 表 + 事件唤醒」链路。
	// 默认值：重构未完成前，不改变现有行为。
	SchedLegacy SchedMode = "legacy"
	// SchedCoord 是新的「Coordinator + mailbox + 显式 spawn/wait」链路。
	SchedCoord SchedMode = "coord"
)

// SchedModeEnv 是切换用的环境变量。写成 ARTEX_SCHED 与仓库既有
// （ARTEX_CONTEXT_WINDOW_CAP_K / ARTEX_COMPACT_RATIO / ARTEX_INTENT_TOOL_CAP）
// 的命名风格一致，运维不需要记第二套规则。
const SchedModeEnv = "ARTEX_SCHED"

// SchedModeNow 读取当前进程的调度模式。非法值退回 legacy 并**不静默**——
// 拼错的环境变量按新链路跑，是最难排查的一类事故，所以这里宁可退回旧链路。
func SchedModeNow() SchedMode {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(SchedModeEnv)))
	switch SchedMode(raw) {
	case SchedLegacy:
		return SchedLegacy
	case "", SchedCoord:
		// 【P4 / B1：默认值切换】这里原本对 "" 返回 SchedLegacy（"重构未完成前
		// 不改变现有行为"）。重构已进入收尾：新链路是**唯一被维护**的路径，
		// 旧链路（planner + reviewer + frontier）已经拆掉 Reviewer、也无法再
		// 承担完整职责。把默认值留在 legacy 等于让用户默认跑一条不再维护的路。
		return SchedCoord
	default:
		// 拼错的环境变量按**新**链路跑：旧链路已不完整，退回它反而更危险。
		// 但这属于配置错误，必须留痕——见下面的 WarnIfInvalid。
		return SchedCoord
	}
}

// SchedModeInvalid 报告环境变量是否写了个无法识别的值。
//
// 单独一个判断而不是塞进 SchedModeNow：后者在热路径上（每个任务 Run 都调），
// 而"配置拼错了"这件事只需要在启动时说一次。
func SchedModeInvalid() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(SchedModeEnv)))
	switch SchedMode(raw) {
	case "", SchedLegacy, SchedCoord:
		return false
	default:
		return true
	}
}

// CoordEnabled 报告是否走新调度层。
func CoordEnabled() bool { return SchedModeNow() == SchedCoord }

// LegacyEnabled 报告是否走旧调度层（两个函数同时存在是刻意的：
// 调用点写 if LegacyEnabled() 比 if !CoordEnabled() 更容易在 review 时看清意图）。
func LegacyEnabled() bool { return SchedModeNow() == SchedLegacy }

// ---------- 下线清单 ----------
//
// 这张表不是文档，是**拆除时必须逐项打勾的执行清单**：每一行都对应一个真实
// 符号、一个真实行号，以及它的替代物。重构的正确顺序是先把新链路跑通再按表
// 删除，而不是先删后补——后者在有未提交改动（当前 38 改 + 15 新增）的仓库上
// 无法回滚。
//
// 表里的 "replacement" 为空串表示**没有替代物，直接删除**（那些是为旧架构
// 服务、新架构不需要其功能的东西）。

// DecomEntry 是下线清单的一行。
type DecomEntry struct {
	Symbol      string // 被删除的符号或文件
	Where       string // 位置（文件:行 或 文件名）
	Lines       int    // 规模（0 = 未知/整文件）
	Replacement string // 替代物；空 = 纯删除
	Note        string
}

// DecommissionTable 是旧调度层需要拆掉的完整清单，按「删除顺序」排列。
//
// 排序依据是依赖方向：先删只被旧链路引用的叶子（reviewer 接线层、reviewer 本体、
// planner 本体），再删引擎里的调度骨架（plannerLoop / runWorkerStep / frontier 派发），
// 最后删接线点（resolver / hooks / env 判断）。反过来做会在中间态留下悬空引用。
var DecommissionTable = []DecomEntry{
	// ── 第一层：只为旧调度存在的整文件 ───────────────────────────────────
	{
		Symbol: "agent/reviewer.go", Where: "agent/reviewer.go", Lines: 926,
		Replacement: "", Note: "第四角色的全部实现（判决清洗/规则引擎/LLM 灰区判定/落库格式）。" +
			"新架构里判据内化为 submit_report 的 outcome 与 open_items 必填字段。",
	},
	{
		Symbol: "server/reviewer.go", Where: "server/reviewer.go",
		Replacement: "", Note: "Reviewer 的接线层：把交接单送进 agent.Review、把判决落 reviewer_verdicts 表、写线索。" +
			"连同 reviewer_verdicts / reviewer_evidence 两张表一起下线。",
	},
	{
		Symbol: "agent/planner.go", Where: "agent/planner.go", Lines: 563,
		Replacement: "agent/coordinator.go + agent/prompts_coord.go (rootTmpl)",
		Note:        "规划者角色。它的职责被拆成三块：派活 → delegate_task；判目标 → 根 agent 自己的 turn；回顾 → 读自己的会话历史。",
	},
	{
		Symbol: "agent/tools_evidence.go", Where: "agent/tools_evidence.go",
		Replacement: "coord_tools.go/agents_graph",
		Note: "fetch_evidence 是给 Planner 的「受限回溯通道」，存在的唯一理由是 Reviewer 把 raw 关掉了。" +
			"新架构里根 agent 能直接读子代理的交卷与 agents_graph，不需要一个只读洗过快照的口子。",
	},

	// ── 第二层：引擎里的派发骨架 ─────────────────────────────────────────
	{
		Symbol: "Engine.plannerLoop", Where: "server/engine.go（P4 已删，约 156 行）", Lines: 156,
		Replacement: "agent.StartTask + TaskRun.AwaitRoot",
		Note: "事件驱动的唤醒循环：debounce / 心跳 / 空转跳过 / 终局收尾轮。" +
			"这些全部是「谁来决定现在该想」这一问题的产物；新架构里由根 agent 自己决定。",
	},
	{
		Symbol: "Engine.runWorkerStep + workerLoop", Where: "server/engine.go（P4 已删，约 140 行）", Lines: 140,
		Replacement: "Spawner.Delegate（每个意图一个子 agent）",
		Note:        "从 frontier 领取意图、执行、结算。领取与结算这两步在内存 mailbox 里退化成一次投递。",
	},
	{
		Symbol: "Engine.runFinalPlannerRound", Where: "server/engine_timeout.go（P4 改为读库判定）", Lines: 40,
		Replacement: "finish_task（收尾由根 agent 调用）+ 读库判定目标是否全 met",
		Note: "超时收尾的「最后一轮 Planner」已删。新实现不再叫醒模型重新判一遍，而是直接读" +
			"目标节点的 state——根 agent 运行期间 prove_goal 落的库才是权威记录，临场判定反而受上下文余量影响。",
	},
	{
		Symbol:      "Engine.resolve / SetAgentResolver / SetAuthoritativeAgentResolver / snapshotFor",
		Where:       "server/engine.go（P4 已删；收敛为 Engine.resolve bool + SetTaskRunnable）",
		Replacement: "Spawner 持有 Runner（构造期注入，运行期不可变）",
		Note: "运行时可换 planner/worker 的注入点。新架构里执行器在 StartTask 时绑定，" +
			"中途换执行器等同于换掉整张图——那应该是一个新任务，不是一个运行期开关。",
	},
	{
		Symbol:      "Engine.dirGate / bumpDirection / AckDispatch / dispatchHeld",
		Where:       "server/engine.go:1430-1470（dirGate 定义与解冻兜底）",
		Replacement: "MsgKind=instruction（send_to_agent）",
		Note: "方向代际门控：为「kill 之后不要再按旧方向派发」发明的。新架构里改方向是一套显式指令，" +
			"由接收方在自己的 turn 里读取，不需要代际号。",
	},
	{
		Symbol:      "Reviewer 闸门熔断 / awaitReviewerVerdict / runReviewerForIntent",
		Where:       "server/handoff.go:181,184 + server/reviewer.go:280-320",
		Replacement: "（无 —— 直接删除）",
		Note: "「等判决落库」的同步闸门与它的熔断器。这份复杂度完全由「交卷必须经过中间角色」这一设计产生，" +
			"交卷变成一条消息后它没有存在意义。",
	},

	// ── 第三层：四套提示词与工具面 ───────────────────────────────────────
	{
		Symbol:      "plannerDefaultTmpl / workerSystem / reviewerDefaultTmpl（均已删；goals 分解提示词保留）",
		Where:       "agent/{planner,worker,reviewer}.go 已下线 / agent/goals.go 仍存活",
		Replacement: "rootTmpl / childTmpl（prompts_coord.go）",
		Note: "四套提示词 + 各自的九段后处理拼接（artifactSpec / toolBatchSpec / langDirective / " +
			"toolNameDiscipline / constraintBlock / envBrief / intentBlock / 启动指令 / intent 8KB 兜底）。" +
			"替换为两个模板 + 五个显式变量，渲染即完成。",
	},
	{
		Symbol: "agent/assembly.go IsReviewerKey 硬闸门", Where: "agent/assembly.go:47-51",
		Replacement: "（无 —— 直接删除）",
		Note:        "「即便是 reviewer 也装配空工具面」的防御。reviewer 不存在了，这条防御失去主语。",
	},
	{
		Symbol: "ToolSet.PlannerTools()", Where: "agent/tools.go:2321",
		Replacement: "ToolSet.CoordTools(fallbackRoot)",
		Note: "规划者专用工具面（add_intent / kill_work / steer_work / graph_overview …）。" +
			"其中 graph_overview 保留但归属改成「根 agent 的只读态势视图」，其余派发类工具由 delegate_task 取代。",
	},

	// ── 第四层：数据库承载的控制流 ───────────────────────────────────────
	{
		Symbol:      "表 intent_runs / intent_handoffs / intent_closures / intent_links / branch_retries / intent_open_work",
		Where:       "db/schema_sqlite.sql",
		Replacement: "Coordinator 内存图 + 一份 JSON 快照（Snapshot）",
		Note: "六张表表达的是「一次任务在角色之间的流转」。新架构里它就是一次函数调用 + 一条消息。" +
			"表可以保留为**审计日志**（谁在什么时候交了什么），但不再参与控制流——是否保留由报表需求决定，不由调度需要决定。",
	},
	{
		Symbol: "表 reviewer_verdicts / reviewer_evidence", Where: "db/schema_sqlite.sql",
		Replacement: "", Note: "判决台账与证据快照。连同它们的 12 个 store 方法一并删除。",
	},
	{
		Symbol: "EnsureReviewerVerdictTable", Where: "server/manager.go:433",
		Replacement: "", Note: "启动期建表调用。",
	},
}

// ── B4 执行现状（2026-10-30 审计后逐项核对，勿凭本表盲目删除）───────────
//
// 上表是按「新架构跑通后应删什么」写的。核对代码后发现其中若干项**不能直接
// 按字面执行**，因为它们在 coord 模式下仍有活跃调用方。这里把实测结果记下来，
// 免得下一个人再从头评估一遍：
//
//	可以删（已删）：Engine.SetAgentResolver —— 零外部引用，resolver 只经
//	  SetAuthoritativeAgentResolver 安装。
//
//	不能删 —— 有活跃调用链：
//	  · runIntent         被 server/intent_intervention.go:150 的
//	                      POST /api/tasks/{id}/intents/{iid}/messages 依赖，
//	                      那是界面上"给某条 work 发消息"的功能。
//	  · runDetachedIntent 同上（它就是上面那个端点的执行体）。
//	  · plannerLoop / workerLoop / runWorkerStep / claimNext
//	                      仅被 Engine.Run 的 legacy 分支调用，而该分支在
//	                      coord 下不可达；删除它们是**纯收益**，但四者共约
//	                      400 行且互相引用，必须整体删除、不能分批——
//	                      这在禁止编译验证的环境里风险过高，留给能跑 go build
//	                      的环境一次做完。
//	  · ToolSet.Frontier() 是 ExplorationStore 的通用查询，服务于
//	                      GET /api/exploration/frontier 与 graph_overview，
//	                      与规划器无关，**不是**旧架构遗留。
//	  · snapshotFor/SetAuthoritativeAgentResolver/dirGate/bumpDirection/AckDispatch
//	                      仍被 task_llm.go 的 agent 装配与 engine 的控制流调用。
//
//	风险已记录：Task.Notify() 在 coord 下只往 pendingTriggers 里堆事件，
//	没有消费者会 drainTriggers——但它被 20+ 处调用，且其中多数是"某个写操作
//	之后叫醒调度"的通用习惯。删掉它需要逐个判断该处是否另有唤醒路径，
//	这是 B4 真正的难点，不是删函数本身。

// DecommissionSummary 统计清单规模，供提交信息与进度看板使用。
func DecommissionSummary() (files, lines int) {
	seen := make(map[string]bool, len(DecommissionTable))
	for _, e := range DecommissionTable {
		if strings.Contains(e.Where, ".go") {
			head := strings.SplitN(e.Where, ":", 2)[0]
			if strings.HasPrefix(head, "agent/") || strings.HasPrefix(head, "server/") {
				if !seen[head] {
					seen[head] = true
					files++
				}
			}
		}
		lines += e.Lines
	}
	return files, lines
}
