package agent

// 意图阶段推断 + 路由信号（自适应工具路由）。
//
// phase 现在只回答「这条意图大致偏侦察/验证/利用哪一侧」，供日志与观测使用；
// planner 显式写下的 phase 会进入 workerRouteText，作为能力路由的一段弱信号。
// 它不再决定工具清单：WorkerTools 恒定返回完整候选目录，AugmentTools 在合并
// MCP/Skill 并执行 ToolResolve 后，再由 RouteToolFace 按工具自述做 Top-N 选择。
// 这样 planner 写错/漏写 phase，最多损失一个弱信号，不会让动态路由永远看不见
// 已被静态阶段面提前删掉的工具。
//
// 推断的优先级仍是 exploit > recon > verify：这组关键词只做粗粒度打标，
// 推断不出来就保持未判定，最终工具面仍由意图全文与工具向量的重叠度决定。

import (
	"encoding/json"
	"log"
	"sort"
	"strings"
	"sync"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// intentSignalText 把一条意图里能说明「它要干什么」的文字拼成一段小写文本。
//
// 只取 summary 与 expected_evidence：payload 里其余字段（asset_ids / parent_ids /
// priority）是结构性引用，不携带动作语义。两者都空 = 无从判断。
func intentSignalText(n *db.Node) string {
	if n == nil || len(n.Payload) == 0 {
		return ""
	}
	var p struct {
		Summary          string `json:"summary"`
		ExpectedEvidence string `json:"expected_evidence"`
	}
	if err := json.Unmarshal(n.Payload, &p); err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(p.Summary + " " + p.ExpectedEvidence))
}

// workerRouteText 拼出「这条意图到底要干什么」的完整文本，供能力路由打分。
//
// 比 intentSignalText 多两样：planner 显式声明的 phase（模型自己写的阶段词是
// 强信号），以及意图挂的资产类型/名称（"扫描 piax-api 的登录接口"里的主语同样
// 决定该挂 net_probe 还是 http_sequence）。仍然只是「意图自己的文字」——不引入
// 任何按目标/按网站的规则表。
func workerRouteText(n *db.Node) string {
	base := intentSignalText(n)
	if n == nil || len(n.Payload) == 0 {
		return base
	}
	var p struct {
		Phase      string   `json:"phase"`
		Target     string   `json:"target"`
		URL        string   `json:"url"`
		Endpoints  []string `json:"endpoints"`
		AssetNames []string `json:"asset_names"`
	}
	if err := json.Unmarshal(n.Payload, &p); err != nil {
		return base
	}
	parts := []string{base, p.Phase, p.Target, p.URL}
	parts = append(parts, p.Endpoints...)
	parts = append(parts, p.AssetNames...)
	return strings.ToLower(strings.TrimSpace(strings.Join(parts, " ")))
}

// 关键词表。中文按「动词/动作名词」取，英文只取足够独特的词（避免 pe 命中 spec
// 这类子串误判）。命中即计数，不看位置。
var (
	// exploitSignals：一旦出现，说明这条意图要「打」而不是「看」。
	exploitSignals = []string{
		"利用", "攻击", "打点", "拿下", "拿权限", "getshell", "get shell", "落shell",
		"提权", "注入", "sqli", "sql 注入", "sql注入", "命令执行", "rce", "反序列化",
		"上传", "绕过", "越权", "idor", "ssrf", "xxe", "csrf", "爆破", "暴力破解",
		"弱口令", "撞库", "破解", "payload", "exploit", "poc", "横向", "打穿",
		"未授权访问", "任意文件", "文件读取", "jndi", "log4j", "fastjson", "shiro",
		"jwt 伪造", "伪造 token", "拿 flag", "拿flag", "提数据", "拖库",
	}
	// verifySignals：复核/复现/判断真伪。
	verifySignals = []string{
		"验证", "复核", "复测", "二次确认", "二次验证", "确认是否", "核实",
		"误报", "排除", "证实", "证伪", "确认漏洞", "复现", "验证码", "打码",
		"是否成立", "是否真实", "真伪",
	}
	// reconSignals：面测绘/枚举/收集。
	reconSignals = []string{
		"侦察", "信息收集", "收集", "枚举", "扫描", "探测", "子域", "端口",
		"目录", "指纹", "存活", "资产", "爬取", "抓取", "测绘", "banner",
		"服务识别", "whois", "dns 记录", "旁站", "c 段", "c段", "inventory",
		"recon", "scan", "enumerate", "discover", "fingerprint", "subdomain",
	}
)

func countSignals(text string, words []string) int {
	if text == "" {
		return 0
	}
	n := 0
	for _, w := range words {
		if strings.Contains(text, w) {
			n++
		}
	}
	return n
}

// inferIntentPhase 得出意图的阶段标签：**先看 planner 有没有自己声明**，
// 没有才从意图文字推断。
//
// 声明优先不是可有可无的细节：planner 写着 `"phase":"verify"` 而 summary 里
// 恰好出现"利用"二字时，按文字推断会把它归到 exploit —— 于是日志、观测与
// workerRouteText 的路由信号全都与意图作者的本意相反。旧实现（已随 worker.go
// 下线的 intentPhase）就是这个顺序，重构时把第一步丢了，测试随即变红。
//
// 返回空串表示「判断不了」。阶段标签不再裁工具：WorkerTools 恒定给完整候选
// 目录，最终由 RouteToolFace 选择。这里仍是**宁可判不出，也不要判错**，避免
// 错误标签污染日志与后续观测。
func inferIntentPhase(n *db.Node) string {
	if n != nil && len(n.Payload) > 0 {
		var p struct {
			Phase string `json:"phase"`
		}
		if err := json.Unmarshal(n.Payload, &p); err == nil {
			if declared := normalizeWorkerPhase(p.Phase); declared != "" {
				return declared
			}
		}
	}
	text := intentSignalText(n)
	if text == "" {
		return ""
	}
	// 优先级见文件头：能力覆盖从宽到窄。
	if countSignals(text, exploitSignals) > 0 {
		return workerPhaseExploit
	}
	if countSignals(text, reconSignals) > 0 {
		return workerPhaseRecon
	}
	if countSignals(text, verifySignals) > 0 {
		return workerPhaseVerify
	}
	return ""
}

// 专用工具不再按 phase/关键词静态预裁。完整候选目录先进入 AugmentTools，
// 最终由 RouteToolFace 按工具自述统一选择，避免上层先删、下层永远选不回来。

// ---- 可观测性 ----

// toolFaceLogged 让每个「不同的工具面」只打一次日志：同一组合可能被多个意图
// 复用，每个意图都打会刷屏，但对账时又必须能看见到底挂了哪些工具。
var toolFaceLogged sync.Map

// logToolFace 打印一次某阶段实际下发的工具清单（同一组合只打一次）。
func logToolFace(phase string, tools []actool.CoreTool) {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name())
	}
	sort.Strings(names)
	label := phase
	if label == "" {
		label = "all(未判定)"
	}
	key := label + "#" + strings.Join(names, ",")
	if _, loaded := toolFaceLogged.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	log.Printf("[tools] worker 工具面 phase=%s n=%d: %s", label, len(names), strings.Join(names, ","))
}
