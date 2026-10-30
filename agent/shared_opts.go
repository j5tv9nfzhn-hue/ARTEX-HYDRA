package agent

// 本文件收纳跨角色共享的基础设施类型。
//
// 为什么要单独成文：WebSearchOpts 与 WorkerSessionID 原先定义在 worker.go 里，
// 但使用它们的是 chat / sync / engine 等**与 Worker 角色无关**的代码。P4 删除
// worker.go 时如果连带删掉这两个符号，编译会在十几个不相干的地方断掉——
// "定义放在谁的文件里"不等于"谁拥有它"。把归属理清，删除才是可控的。

import "fmt"

// WebSearchOpts 是 web_search 工具的后端选择。零值 = 关闭。
//
// 它属于**会话能力**而不是某个角色：ChatAgent、独立 agent、子 agent 都可能需要
// 联网搜索，各自的开关由调用方按 agent key 决定（见 server 的 webSearchFor）。
type WebSearchOpts struct {
	Enabled   bool
	Backend   string
	BraveKey  string
	TavilyKey string
	Proxy     string
	// DeepSeek* 来自当前激活的 LLM 配置(仅 anthropic 格式的 DeepSeek 官方端点)，
	// 不单独配置，随 LLM 配置切换而变。
	DeepSeekBaseURL string
	DeepSeekAPIKey  string
	DeepSeekModel   string
}

// WorkerSessionID 返回一条 work 意图的稳定 transcript 键。
//
// 名字里的 Worker 是历史遗留：旧架构里 work 意图由 Worker 池执行，所以用
// worker 作前缀。coord 模式下同一个意图可以由子 agent 执行，但 transcript
// 的键必须**保持不变**——已经落盘的会话文件按这个键命名，改了前缀等于让所有
// 历史会话失联。这是兼容性约束，不是命名偏好。
func WorkerSessionID(explorationID, intentID int64) string {
	return fmt.Sprintf("exp%d-worker-i%d", explorationID, intentID)
}

// settleWrapUpPrompt 是 SDK settlement 阶段注入的收尾词：步数/时长预算耗尽时，
// 停止探测、把已识别的结果落库、再用一句纯文本收尾。
//
// 原先定义在 worker.go。它服务的语义在 coord 下**完全一样**——子 agent 撞到
// 预算上限时同样需要"先交卷再停"，所以它不是 Worker 的私有文本，而是所有
// 执行型角色的公共收尾词。随 worker.go 一起删掉会让 wrapup.go 的
// wrapupDefaults["coord_child"] 变成悬空引用。
const settleWrapUpPrompt = "本次执行即将因步数/时长上限而结束。不要再执行任何命令或探测。请按以下顺序处理：(1) 逐项记录此前已识别但尚未保存的结果：新资产使用 insert_assets，探索结论与事实使用 record_fact，已确认漏洞使用 report_finding——把还没做完的下一步、未验证的假设、已经排除的死路也写进 detail，这些是下一位接手时最值钱的东西，不写就永久丢失。(2) **最后只用一句纯文本**，以简体中文总结做了什么以及得到的核心结论。这句话会作为本次执行结果展示给用户，必须输出。"

// TriggerEvent 是一次"唤醒事件"：说明这一轮 planner 是被什么触发的。
//
// 原先定义在 planner.go。planner 下线后，`Task.Notify*` 系列（server/manager.go）
// 仍在往 pendingTriggers 里追加它——那是一份**平台级**的事件记录，描述"任务态势
// 变了"，与由谁来消费无关。coord 下这份记录目前没有消费者（根 agent 靠
// delegate_task 的返回值感知子 agent 结束，而不是从队列里领事件）。
//
// 保留它有两个理由：
//  1. 编译需要——server/manager.go 的 NotifyDone/Notify 等 20+ 处调用点都构造它；
//  2. 它有独立的诊断价值——pendingTriggers 是"这一轮为什么被唤醒"的唯一记录，
//     将来若给根 agent 加"态势变化时主动 review"的能力，这里是现成的入口。
//
// 字段语义保持不变（Kind 的取值见 server 侧的各 Notify* 方法）：
//
//	"done"         — 某个意图跑完了（IntentID + 它的输出结论）
//	"finding"      — 新发现了漏洞
//	"goal"         — 人类新增了目标（Goals = 本次新增的目标文本，1 条或多条）
//	"goal_deleted" — 人类删除了一个目标（Detail = 被删目标文本）
//	"goal_edited"  — 人类修改了一个目标（OldGoal→NewGoal）
//	"cancelled"    — 人类删除了某条意图（Detail = 删除原因）
//	"hint"         — 人类新增了提示（Hints = 本次新增的提示文本）
type TriggerEvent struct {
	Kind     string
	IntentID int64
	Detail   string
	Summary  string   // Kind=="cancelled" 专用：删除前捕获的意图摘要
	Goals    []string // Kind=="goal" 专用：本次新增的目标文本（1 条或多条）
	OldGoal  string   // Kind=="goal_edited" 专用：修改前的目标文本
	NewGoal  string   // Kind=="goal_edited" 专用：修改后的目标文本
	Hints    []string // Kind=="hint" 专用：本次新增的提示文本（1 条或多条）
}
