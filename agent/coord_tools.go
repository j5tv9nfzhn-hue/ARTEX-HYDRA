package agent

// 协调器工具面 —— 取代旧架构里 Planner 的 add_intent / kill_work / steer_work
// 与 Worker 的交接单这三套控制接口。
//
// 设计要点：每个动作都对应一次真实的状态迁移，不提供"设置字段"式的松接口。
// 工具越少，模型选错的概率越低，也不需要工具路由去裁剪工具面。
//
//	delegate_task    派一个子 agent（唯一的并行来源）
//	send_to_agent    给在跑的 agent 追加 query / instruction / information
//	wait_for_agents  等一次（同一 turn 只允许一次）
//	agents_graph     看整张图（只读，含各 agent 交卷的结构化产物 id）
//	stop_agent       停一个子 agent（不是停整棵树）
//
// 收尾动作在 root/child 两套工具面的尾部分别接 finish_task / submit_report。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	acperm "github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// 工具名常量：供工具面装配、日志与前端标签复用，避免三处各写一遍字符串。
const (
	ToolDelegateTask  = "delegate_task"
	ToolSendToAgent   = "send_to_agent"
	ToolWaitForAgents = "wait_for_agents"
	ToolAgentsGraph   = "agents_graph"
	ToolStopAgent     = "stop_agent"
	ToolSubmitReport  = "submit_report"
	ToolFinishTask    = "finish_task"
)

// waitWindowDefault 是 wait_for_agents 的单次等待窗口。
// 取值理由是"比一次典型子任务短、比一次模型调用长"：太短会频繁返回而让模型
// 以为子任务卡住，太长则一次卡顿就把父级的上下文预算耗在一个空等上。
const waitWindowDefault = 5 * time.Minute

// CoordTools 返回协调器工具集（根 agent 用）。
//
// 这些工具的 handler 只用 ctx 里的 Spawner，不碰 *ToolSet 的探索存储，
// 因此是**方法值**而不是实例状态依赖——宿主可以用任意 *ToolSet 调它，
// 也可以直接调用导出的 CoordToolSet(ts)（见下）。
func (t *ToolSet) CoordTools(fallbackRoot bool) []actool.CoreTool {
	out := []actool.CoreTool{
		t.delegateTask(),
		t.sendToAgent(),
		t.waitForAgents(),
		t.agentsGraph(),
		t.submitReport(),
	}
	if fallbackRoot {
		out = append(out, t.finishTask())
	}
	return out
}

// ChildCoordTools 返回子 agent 的协调器工具集：没有 finish_task（那是根的收尾），
// 也没有 delegate 之外的编排权，但保留 spawn 能力以便递归分组（受 MaxDepth 限制）。
func (t *ToolSet) ChildCoordTools() []actool.CoreTool {
	return []actool.CoreTool{
		t.delegateTask(),
		t.sendToAgent(),
		t.waitForAgents(),
		t.agentsGraph(),
		// 停单个子代理。子代理也拿得到：它能停自己派生的下级，
		// 亲缘判定保证越不了级（见 isDescendantOf）。
		t.stopAgent(),
		t.submitReport(),
	}
}

// ---------- delegate_task ----------

func (t *ToolSet) delegateTask() actool.CoreTool {
	desc := "派一个子 agent 去执行一件**自包含**的任务（唯一的并行来源）。\n" +
		"子 agent 看不到你的会话历史，所以 task 必须把背景、目标、已知资产、约束、" +
		"判定标准一次写全；写得含糊，子 agent 就只能靠猜，回来的是废结论。\n" +
		"派发是异步的：调用立刻返回 agent id，你可以继续做别的事，或稍后 wait_for_agents。\n" +
		"**同一个面不要派两个 agent 去做**——重复覆盖既烧钱又会让结论互相矛盾。"
	return t.writeObjTool(ToolDelegateTask, desc,
		obj(map[string]any{
			"task":            str("子任务全文：背景 / 目标 / 已知资产与证据 / 约束 / 什么算完成。必填。"),
			"name":            str("短标签（如 sqli-login），用于生成 agent id 和 UI 展示。省略则按 worker 编号。"),
			"role":            str("角色词（如 recon / exploit / verify），仅用于展示与分组，不影响调度。"),
			"skills":          strArr("要给子 agent 的领域能力名（可空）。"),
			"asset_ids":       intArr("该任务涉及的资产 id（可空）。"),
			"intent_id":       intp("关联的意图/工作项 id（可空）。"),
			"max_turns":       intp("子 agent 的轮次上限（可空，默认由宿主配置）。"),
			"wait_for_report": boolp("true = 派发后就地等它交卷并直接拿到结论。默认 false。"),
		}, "task"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Task          string   `json:"task"`
				Name          string   `json:"name"`
				Role          string   `json:"role"`
				Skills        []string `json:"skills"`
				AssetIDs      []int64  `json:"asset_ids"`
				IntentID      int64    `json:"intent_id"`
				MaxTurns      int      `json:"max_turns"`
				WaitForReport bool     `json:"wait_for_report"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			sp, err := SpawnerFrom(ctx)
			if err != nil {
				return actool.Errorf("当前没有可用的调度层（不在任务内，或调度未装配）。请自己直接完成这项工作"), nil
			}
			if strings.TrimSpace(a.Task) == "" {
				return actool.Errorf("task 不能为空"), nil
			}
			parent := AgentIDFrom(ctx)
			if parent == "" {
				parent = firstNonEmpty(func() string {
					if root := sp.Coord.Root(); root != nil {
						return root.ID
					}
					return ""
				}())
			}
			view, derr := sp.Delegate(ctx, AgentSpec{
				ParentID: parent,
				Name:     a.Name,
				Role:     a.Role,
				Task:     a.Task,
				Skills:   a.Skills,
				AssetIDs: a.AssetIDs,
				IntentID: a.IntentID,
				MaxTurns: a.MaxTurns,
				// 深度由 ctx 给出（谁在派），不信任模型自报：模型无法通过参数绕过递归闸门。
				Depth: AgentDepthFrom(ctx),
			})
			if derr != nil {
				return actool.Errorf(derr.Error()), nil
			}
			if !a.WaitForReport {
				return jsonResult(map[string]any{
					"agent_id": view.ID,
					"state":    view.State,
					"note":     "已派发，异步执行中。你可以继续做别的事；要看结论时用 wait_for_agents（一轮只等一次）。",
				})
			}
			outcome, msgs := sp.Wait(ctx, parent, []string{view.ID}, waitWindowDefault)
			return jsonResult(map[string]any{
				"agent_id":     view.ID,
				"wait_outcome": string(outcome),
				"reports":      renderMessages(msgs),
			})
		})
}

// ---------- send_to_agent ----------

func (t *ToolSet) sendToAgent() actool.CoreTool {
	desc := "给一个在跑的 agent 追加消息。三种语义只有一种正确用法：\n" +
		"- query：问一件具体的事，它会在下一步回答；\n" +
		"- instruction：改它的方向（唯一会打断它当前动作的类型）；\n" +
		"- information：通知它一个事实，不打断它。\n" +
		"消息要短。你在替它省上下文，不是在补一段文档。\n" +
		"目标已终结且不可恢复时会直接报错——那时不要重试，去看它的报告。"
	return t.writeObjTool(ToolSendToAgent, desc,
		obj(map[string]any{
			"agent_id": str("目标 agent id（delegate_task 返回的，或 agents_graph 里看到的）。"),
			"kind":     str("query | instruction | information，默认 information。"),
			"body":     str("消息正文。要具体：说清要它改什么、为什么、以什么为完成标准。"),
		}, "agent_id", "body"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				AgentID string `json:"agent_id"`
				Kind    string `json:"kind"`
				Body    string `json:"body"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			sp, err := SpawnerFrom(ctx)
			if err != nil {
				return actool.Errorf("当前没有可用的调度层"), nil
			}
			kind := MsgKind(strings.ToLower(strings.TrimSpace(a.Kind)))
			if !kind.valid() {
				kind = MsgInformation
			}
			if strings.TrimSpace(a.Body) == "" {
				return actool.Errorf("body 不能为空"), nil
			}
			msg, serr := sp.Coord.Send(AgentIDFrom(ctx), a.AgentID, kind, a.Body)
			if serr != nil {
				if errors.Is(serr, ErrUnreachable) {
					return actool.Errorf(serr.Error() + "。去看 agents_graph 或它的报告，不要重发"), nil
				}
				return actool.Errorf(serr.Error()), nil
			}
			return jsonResult(map[string]any{"message_id": msg.ID, "kind": msg.Kind, "delivered_to": msg.To})
		})
}

// ---------- wait_for_agents ----------

func (t *ToolSet) waitForAgents() actool.CoreTool {
	desc := "等到子 agent 有交卷/消息，或全部终结。\n" +
		"**一轮只等一次**：同一 turn 第二次调用会立刻返回 already_waited，不要再试。\n" +
		"返回后先读 reports，再决定下一步：继续等没有意义（该来的会自己进你的上下文）。\n" +
		"不传 agent_ids 就等全部直接子 agent。"
	return t.writeObjTool(ToolWaitForAgents, desc,
		obj(map[string]any{
			"agent_ids": strArr("要等的 agent id（可空 = 全部直接子 agent）。"),
			"window_s":  intp("等待窗口秒数（可空，默认 300，上限 1800）。"),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				AgentIDs []string `json:"agent_ids"`
				WindowS  int      `json:"window_s"`
			}
			_ = json.Unmarshal(in, &a)
			sp, err := SpawnerFrom(ctx)
			if err != nil {
				return actool.Errorf("当前没有可用的调度层"), nil
			}
			me := AgentIDFrom(ctx)
			if me == "" {
				return actool.Errorf("当前会话没有 agent 身份，无法等待"), nil
			}
			targets := a.AgentIDs
			if len(targets) == 0 {
				for _, ch := range sp.Coord.Children(me) {
					if !ch.State.IsTerminal() || ch.Pending > 0 {
						targets = append(targets, ch.ID)
					}
				}
			}
			if len(targets) == 0 {
				return jsonResult(map[string]any{
					"wait_outcome": string(WaitNoTargets),
					"note":         "没有可等的子 agent。要么还没派，要么都已经交卷——去 agents_graph 看状态。",
				})
			}
			window := waitWindowDefault
			if a.WindowS > 0 {
				window = time.Duration(a.WindowS) * time.Second
				if window > 30*time.Minute {
					window = 30 * time.Minute
				}
			}
			outcome, msgs := sp.Wait(ctx, me, targets, window)
			payload := map[string]any{
				"wait_outcome": string(outcome),
				"waited":       targets,
				"reports":      renderMessages(msgs),
			}
			switch outcome {
			case WaitAlreadyWaited:
				payload["note"] = "本轮已经等过一次。直接用现有信息继续，等新消息会自己进来。"
			case WaitNoTargets:
				payload["note"] = "目标都已终结且没有待读消息。不要再等。"
			case WaitTimeoutFx:
				payload["note"] = "等待窗口用尽但目标还活着。不要立刻再等——先自己推进别的工作。"
			case WaitCancelled:
				payload["note"] = "任务被暂停或删除。"
			}
			return jsonResult(payload)
		})
}

// ---------- agents_graph ----------

// isDescendantOf 判断 target 是否在 ancestor 的子树里（含间接后代）。
//
// 沿 ParentID 向上走，遇空即到根。maxDepth 兜底防环：Coordinator 的父子边
// 由 Register 建立，理论上无环，但一个错误的 Write 可能就是无限循环——
// 这条路径在工具调用里，卡住会让整个 agent 循环停摆。
func isDescendantOf(c *Coordinator, target, ancestor string) bool {
	if target == "" || ancestor == "" || target == ancestor {
		return false
	}
	const maxDepth = 64
	cur := target
	for i := 0; i < maxDepth; i++ {
		v, ok := c.View(cur)
		if !ok {
			return false
		}
		if v.ParentID == "" {
			return false // 走到根仍没遇到 ancestor
		}
		if v.ParentID == ancestor {
			return true
		}
		cur = v.ParentID
	}
	return false
}

// stopAgent 停掉一个在跑的子 agent。
//
// 为什么必须有这个工具：Coordinator.Stop 一直存在，但没暴露成工具，于是
// "一个子代理跑飞了"这件事在根的视角里无解——它只能干等 wait_for_agents 返回，
// 或者直接 finish_task 把整张图关掉。strix 的 stop_agent、opencode 的
// task_cancel 都独立成工具，正是为了让"停一个分支"不必等于"停整棵树"。
//
// 语义边界：
//   - 只能停**自己的后代**（RootID 必须一致），否则根能停别人的图；
//   - 停根自己无意义（那是 finish_task 的职责），直接拒绝；
//   - 已终结的 agent 会被 Stop 拒绝（unknown/已关闭），照实报错不复述。
func (t *ToolSet) stopAgent() actool.CoreTool {
	desc := "停掉一个正在跑的子 agent（不是停整个任务）。它会收到取消信号，状态记为 stopped，\n" +
		"已经在库里的产出保留。停单个分支用它；要结束整个任务用 finish_task。\n" +
		"停完先看 agents_graph 确认状态，不要立刻重发同一个任务——先弄清它为什么需要被停。"
	return t.writeObjTool(ToolStopAgent, desc,
		obj(map[string]any{
			"agent_id": str("要停的 agent id（agents_graph 里看到的，delegate_task 返回的）。"),
			"reason":   str("停它的原因，一句话。会记进该 agent 的状态与活动流，供事后再看。"),
		}, "agent_id", "reason"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				AgentID string `json:"agent_id"`
				Reason  string `json:"reason"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			sp, err := SpawnerFrom(ctx)
			if err != nil {
				return actool.Errorf("当前没有可用的调度层"), nil
			}
			target := strings.TrimSpace(a.AgentID)
			if target == "" {
				return actool.Errorf("agent_id 不能为空"), nil
			}
			me := AgentIDFrom(ctx)
			if target == me {
				return actool.Errorf("不能用 stop_agent 停自己：结束整个任务请用 finish_task"), nil
			}
			// 只允许停自己的后代：跨图操作会让"谁负责派发"失去唯一答案。
			// AgentView.Root 是 bool（"它本身是不是根"），不是根 id，所以判定
			// 亲缘关系必须沿 ParentID 往上走，不能拿 Root 当 id 比。
			if _, ok := sp.Coord.View(target); !ok {
				return actool.Errorf("找不到 agent " + target + "：先用 agents_graph 看当前有哪些"), nil
			}
			if !isDescendantOf(sp.Coord, target, me) {
				return actool.Errorf(target + " 不是你派生的 agent，不能停"), nil
			}
			reason := strings.TrimSpace(a.Reason)
			if reason == "" {
				reason = "编排者主动停止"
			}
			if err := sp.Coord.Stop(target, reason); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{
				"stopped":   target,
				"reason":    reason,
				"note":      "状态已置为 stopped；它的历史产出保留，可读 agents_graph 看结论。",
				"remaining": sp.Coord.ActiveCount(),
			})
		})
}

func (t *ToolSet) agentsGraph() actool.CoreTool {
	desc := "看整张 agent 图：谁在跑、谁已交卷、各自结论与消耗。派活前后各看一次即可，" +
		"不要用它代替 wait_for_agents。"
	return t.readObjTool(ToolAgentsGraph, desc, obj(map[string]any{}),
		func(ctx context.Context, _ json.RawMessage) (actool.Result, error) {
			sp, err := SpawnerFrom(ctx)
			if err != nil {
				return actool.Errorf("当前没有可用的调度层"), nil
			}
			all := sp.Coord.All()
			sort.SliceStable(all, func(i, j int) bool {
				if all[i].Root != all[j].Root {
					return all[i].Root
				}
				return all[i].StartedAt.Before(all[j].StartedAt)
			})
			rows := make([]map[string]any, 0, len(all))
			for _, v := range all {
				row := map[string]any{
					"id":         v.ID,
					"parent":     v.ParentID,
					"root":       v.Root,
					"state":      v.State,
					"turn":       v.Turn,
					"pending":    v.Pending,
					"role":       v.Role,
					"elapsed_s":  int64(time.Since(v.StartedAt).Seconds()),
					"tool_calls": v.Usage.ToolCalls,
				}
				if v.Brief != nil {
					row["outcome"] = v.Brief.Outcome
					row["summary"] = preview(v.Brief.Summary, 240)
					row["open_items"] = len(v.Brief.OpenItems)
					// 交卷的**结构化产物**优先于自述：summary 是被截断的散文，
					// 而 facts / evidence / asset_ids 是能拿去核对的东西。
					// strix 的 _filed_reports_by 读报告库而不是读 narrative，
					// 理由相同——父 agent 要据此行动，就不能只信"我说我做完了"。
					if len(v.Brief.Facts) > 0 {
						facts := make([]map[string]any, 0, len(v.Brief.Facts))
						for i, f := range v.Brief.Facts {
							if i >= 8 { // 上限：图是概览，不是全文
								break
							}
							row2 := map[string]any{"summary": preview(f.Summary, 120)}
							if f.IntentID != 0 {
								row2["intent_id"] = f.IntentID
							}
							if len(f.AssetIDs) > 0 {
								row2["asset_ids"] = f.AssetIDs
							}
							facts = append(facts, row2)
						}
						row["facts"] = facts
						row["facts_total"] = len(v.Brief.Facts)
					}
					if len(v.Brief.Evidence) > 0 {
						row["evidence_total"] = len(v.Brief.Evidence)
						row["evidence"] = preview(strings.Join(v.Brief.Evidence, " | "), 200)
					}
					row["has_counter_view"] = v.Brief.CounterView != ""
				}
				if v.LastError != "" {
					row["last_error"] = preview(v.LastError, 200)
				}
				rows = append(rows, row)
			}
			return jsonResult(map[string]any{
				"task_id":            sp.Coord.TaskID,
				"agents":             rows,
				"active":             sp.Coord.ActiveCount(),
				"children_of_caller": len(sp.Coord.Children(AgentIDFrom(ctx))),
			})
		})
}

// ---------- submit_report（子 agent 收尾） ----------

func (t *ToolSet) submitReport() actool.CoreTool {
	desc := "交卷。**这是子 agent 唯一的正常退出方式**：turn 里不调用它，就等于没有结论。\n" +
		"outcome 必须是 confirmed / ruled_out / open_proof_gap 之一：\n" +
		"- confirmed：有可复现证据（命令 + 关键输出，或请求 id）；\n" +
		"- ruled_out：能说出具体哪个控制点、在哪个位置、挡住了哪条攻击路径；\n" +
		"- open_proof_gap：合理但没证实，且你说不出控制点。\n" +
		"**「我换方向了」不是一种收尾状态。** 既不能确认也不能排除的东西必须进 open_items；" +
		"把不确定的候选静默丢掉、再报 ruled_out，就是漏报。\n" +
		"open_items 留空是一个强断言：你声称这条分支已经全部干净。"
	return t.writeObjTool(ToolSubmitReport, desc,
		obj(map[string]any{
			"outcome":      str("confirmed | ruled_out | open_proof_gap"),
			"summary":      str("一到三句结论。先给判断，再给依据，不要复述过程。"),
			"evidence":     strArr("可复核的证据：命令、输出片段、请求 id、文件路径。"),
			"open_items":   strArr("既未确认也未排除的东西（可空，空=断言已全部收口）。"),
			"counter_view": str("反证核查：你试着推翻自己的结论时，最强的一条反驳是什么、为什么不成立。"),
			"facts":        strArr("值得跨 agent 复用的事实，每条一句（可空）。"),
		}, "outcome", "summary"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Outcome     string   `json:"outcome"`
				Summary     string   `json:"summary"`
				Evidence    []string `json:"evidence"`
				OpenItems   []string `json:"open_items"`
				CounterView string   `json:"counter_view"`
				Facts       []string `json:"facts"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			sp, err := SpawnerFrom(ctx)
			if err != nil {
				return actool.Errorf("当前没有可用的调度层"), nil
			}
			me := AgentIDFrom(ctx)
			outcome := strings.ToLower(strings.TrimSpace(a.Outcome))
			switch outcome {
			case "confirmed", "ruled_out", "open_proof_gap":
			default:
				return actool.Errorf(fmt.Sprintf("outcome 必须是 confirmed / ruled_out / open_proof_gap，收到 %q", a.Outcome)), nil
			}
			b := Brief{
				State:       StateCompleted,
				Outcome:     outcome,
				Summary:     strings.TrimSpace(a.Summary),
				Evidence:    a.Evidence,
				OpenItems:   a.OpenItems,
				CounterView: a.CounterView,
			}
			for _, f := range a.Facts {
				if strings.TrimSpace(f) != "" {
					b.Facts = append(b.Facts, BriefFact{Summary: strings.TrimSpace(f)})
				}
			}
			if err := sp.Coord.Finish(me, b); err != nil {
				return actool.Errorf(fmt.Sprintf("交卷被拒：%v。补齐字段后再调一次", err)), nil
			}
			return jsonResult(map[string]any{
				"accepted": true,
				"state":    string(StateCompleted),
				"note":     "已交卷并送达父 agent。你现在应当停止工作。",
			})
		})
}

// ---------- finish_task（根 agent 收尾） ----------

func (t *ToolSet) finishTask() actool.CoreTool {
	desc := "结束整个任务并交卷。只能在目标已达成、或明确判定无法推进时调用。\n" +
		"verdict 必须是 met / partial / blocked 之一；summary 要能让没参与的人看懂" +
		"「结论是什么、凭什么、还有什么没做」。\n" +
		"**调用前先做一次反证核查**：把最可能推翻你结论的那条理由写进 counter_view。"
	return t.writeObjTool(ToolFinishTask, desc,
		obj(map[string]any{
			"verdict":      str("met（目标达成）| partial（部分达成）| blocked（无法推进）"),
			"summary":      str("最终结论：做了什么、发现了什么、凭什么这么说。"),
			"evidence":     strArr("关键证据引用（可空）。"),
			"open_items":   strArr("未闭环事项（可空，空=断言已全部收口）。"),
			"counter_view": str("反证核查结论（可空，但强烈建议写）。"),
		}, "verdict", "summary"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Verdict     string   `json:"verdict"`
				Summary     string   `json:"summary"`
				Evidence    []string `json:"evidence"`
				OpenItems   []string `json:"open_items"`
				CounterView string   `json:"counter_view"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("参数解析失败：" + err.Error()), nil
			}
			sp, err := SpawnerFrom(ctx)
			if err != nil {
				return actool.Errorf("当前没有可用的调度层"), nil
			}
			v := strings.ToLower(strings.TrimSpace(a.Verdict))
			switch v {
			case "met", "partial", "blocked":
			default:
				return actool.Errorf(fmt.Sprintf("verdict 必须是 met / partial / blocked，收到 %q", a.Verdict)), nil
			}
			b := Brief{
				State:       StateCompleted,
				Outcome:     v,
				Summary:     strings.TrimSpace(a.Summary),
				Evidence:    a.Evidence,
				OpenItems:   a.OpenItems,
				CounterView: a.CounterView,
			}
			if err := sp.Coord.Finish(AgentIDFrom(ctx), b); err != nil {
				return actool.Errorf(fmt.Sprintf("交卷被拒：%v。补齐字段后再调一次", err)), nil
			}
			return jsonResult(map[string]any{"accepted": true, "verdict": v})
		})
}

// ---------- 共用的小工具 ----------

// readObjTool / writeObjTool 与 tools.go 的 readTool/writeTool 同构，但在这里
// 重新实现一遍的理由是：协调器工具**不依赖** *ToolSet 的探索存储（t.needExploration），
// 它们只依赖 ctx 里的 Spawner。混用会导致「没有 store 的任务也能派活，却打不开工具」。
func (t *ToolSet) readObjTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name:        name,
		Description: desc,
		Schema:      schema,
		ReadOnly:    func(json.RawMessage) bool { return true },
		Concurrent:  func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, acperm.Context) acperm.Decision { return acperm.Allowed() },
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

func (t *ToolSet) writeObjTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name:        name,
		Description: desc,
		Schema:      schema,
		Permissions: func(context.Context, json.RawMessage, acperm.Context) acperm.Decision { return acperm.Allowed() },
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

func strArr(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

func intArr(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": desc}
}

func boolp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

// renderMessages 把 mailbox 里的消息整理成父级可读的列表。刻意不复用 Brief.Render：
// 消息可能是 query 的答复，不一定带 state/outcome。
func renderMessages(msgs []Message) []map[string]any {
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, map[string]any{
			"id":   m.ID,
			"from": m.From,
			"kind": string(m.Kind),
			"body": m.Body,
		})
	}
	return out
}

// CoordinatorSummary 是给上层（引擎 / UI / 报告）用的最终摘要：
// 它把整张图收敛成"谁干了什么、结论如何"，不再需要有人去读交接单表。
func CoordinatorSummary(c *Coordinator) map[string]any {
	if c == nil {
		return nil
	}
	all := c.All()
	facts := make([]string, 0, 8)
	open := make([]string, 0, 8)
	for _, v := range all {
		if v.Brief == nil {
			continue
		}
		for _, f := range v.Brief.Facts {
			facts = append(facts, f.Summary)
		}
		open = append(open, v.Brief.OpenItems...)
	}
	return map[string]any{
		"task_id":     c.TaskID,
		"agents":      len(all),
		"active":      c.ActiveCount(),
		"facts":       facts,
		"open_items":  open,
		"snapshot_at": fmt.Sprintf("%d", time.Now().Unix()),
	}
}
