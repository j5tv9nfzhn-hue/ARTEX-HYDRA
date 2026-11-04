package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Autumn-27/norma/llm"
	actool "github.com/Autumn-27/norma/tool"
)

const loopStrikeLimit = 3

type loopSlot struct {
	gate      chan struct{}
	lastSig   string
	streak    int
	blocked   bool
	totalHits int
	// hint 是本槽位「首次触发止损」时生成的一次性线索提示（见 clue_hint.go）。
	// 缓存起来有两个原因：每个被拦下的后续调用都能看到同一份提示（否则模型只
	// 在第一次看到、之后又忘），以及不重复打 DB。
	hint string
}

// loopBreaker is local to one AugmentTools invocation, not persistent across
// runs. It keys on exact tool name + normalized input; Result errors and Call
// errors count. This does not try to interpret opaque successful output.
//
// Bash, control-plane tools and meta tools are excluded. Traffic inside one
// tool call remains outside its scope: this is not a network sandbox.
// Same-key calls are serialized so concurrent calls cannot pass a tripped gate.
// There is no model-callable reset; a new run starts with a new instance.
type loopBreaker struct {
	mu    sync.Mutex
	slots map[string]*loopSlot
}

func newLoopBreaker() *loopBreaker { return &loopBreaker{slots: make(map[string]*loopSlot)} }

func (lb *loopBreaker) slot(key string) *loopSlot {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	s := lb.slots[key]
	if s == nil {
		s = &loopSlot{gate: make(chan struct{}, 1)}
		lb.slots[key] = s
	}
	return s
}

// before returns a release function or an error result without invoking the tool.
func (lb *loopBreaker) before(ctx context.Context, name string, in json.RawMessage) (func(), *actool.Result) {
	noop := func() {}
	if lb == nil {
		return noop, nil
	}
	key := loopCallKey(name, in)
	if key == "" {
		return noop, nil
	}
	s := lb.slot(key)
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		res := actool.Errorf("等待同目标执行槽位已取消：" + ctx.Err().Error() +
			".同一目标同一时刻只允许一次调用，本次在排队期间等待超时（通常是 run 时间上限，或同目标并发请求太多）。请减少同目标并发或改为顺序执行。目标标识：" + key)
		return noop, &res
	}
	var once sync.Once
	release := func() { once.Do(func() { <-s.gate }) }
	if err := ctx.Err(); err != nil {
		release()
		res := actool.Errorf("同目标调用已取消：" + err.Error() + "。本次未执行外部调用。目标标识：" + key)
		return noop, &res
	}
	lb.mu.Lock()
	blocked := s.blocked
	streak := s.streak
	lb.mu.Unlock()
	if blocked {
		release()
		msg := fmt.Sprintf("[执行层止损 / loop_blocked] 本轮同一工具、方法、完整 URL 与请求上下文已连续 %d 次收到相同负反馈。本次未执行外部调用。请记录观察与限制并结束该尝试，或在授权范围内选择不同方法；不要改无关参数绕过止损。新执行轮会重建本轮状态，这不是跨轮安全隔离。目标标识：%s", streak, key)
		if strings.HasPrefix(key, "exact:") {
			msg = fmt.Sprintf("[执行层止损 / loop_blocked] 本轮同一工具与完全相同入参已连续 %d 次返回工具错误。本次未执行该工具。请先诊断参数/前置条件/目标状态，基于线索换一种方法或分支；不要改无关参数绕过止损。新执行轮会重建本轮状态，这不是跨轮安全隔离。调用标识：%s", streak, key)
		}
		msg += lb.hintFor(s)
		res := actool.Errorf(msg)
		return noop, &res
	}
	return release, nil
}

// hintFor 返回（并缓存）本槽位的换分支提示：止损不是终点，目的是让它换一条
// 分支继续走，而不是原地停下或原地重试。
func (lb *loopBreaker) hintFor(s *loopSlot) string {
	if s == nil {
		return ""
	}
	lb.mu.Lock()
	defer lb.mu.Unlock()
	if s.hint == "" {
		s.hint = "\n[执行层止损 / 换分支]\n不要原地重试同一调用：先诊断参数与前置条件，换一个端点/参数/身份或换一种方法；若确实走不通，记录负结果与证据后收尾。"
	}
	return s.hint
}

func (lb *loopBreaker) inspect(ctx context.Context, name string, in json.RawMessage, res actool.Result, callErr error) actool.Result {
	if lb == nil {
		return res
	}
	key := loopCallKey(name, in)
	if key == "" {
		return res
	}
	// HTTP 语义键仍只看结构化结果：Call error 里混着取消/超时等传输层噪声，
	// 不能据此推断目标负反馈。精确调用键不同：同工具+同入参连 Call 都失败，
	// 本身就是「这条路走不通」的证据，必须计入，否则 MCP 工具的传输错误可以
	// 无限原样重试。
	if callErr != nil && !strings.HasPrefix(key, "exact:") {
		return res
	}
	sig, diag := classifyNegativeOutcome(name, res)
	if strings.HasPrefix(key, "exact:") && (res.IsError || callErr != nil) {
		// 精确调用键已经钉死「同工具+同入参」；错误文案里的时间戳/请求 id
		// 不该让同一失败每次都变成新签名。
		sig = "tool_error"
		if diag == "" {
			diag = "工具返回错误；请区分参数、状态前置条件、网络和服务端错误。"
		}
	}
	s := lb.slot(key)
	lb.mu.Lock()
	s.totalHits++
	if sig == "" {
		s.streak = 0
		s.lastSig = ""
		lb.mu.Unlock()
		return res
	}
	if s.lastSig == sig {
		s.streak++
	} else {
		s.lastSig = sig
		s.streak = 1
	}
	if s.streak >= loopStrikeLimit {
		s.blocked = true
	}
	blocked, streak := s.blocked, s.streak
	lb.mu.Unlock()
	if !blocked {
		return res
	}
	if callErr != nil {
		// 状态已经记下；Call error 由上层按原样返回，这里不伪造一份 Result
		// 正文（err 非空时它多半会被丢弃）。下一次同键调用会在 before 被拦下
		// 并递送线索提示。
		return res
	}
	warning := fmt.Sprintf("\n[执行层止损 / 3-Strike]\n已观察到连续 %d 次相同负反馈；本轮后续同键调用将于执行前拒绝。%s\n这仅说明当前调用上下文与方法未获得有效结果，不能证明后端实现、参数化查询、漏洞不存在或整个目标安全。记录原始状态与证据，将解释标为推断；不要以止损代替目标达成证明。", streak, diag)
	warning += lb.hintFor(s)
	// Append one text block; preserve existing text/non-text blocks, IsError, Extra.
	res.Content = append(append([]llm.ContentBlock(nil), res.Content...), llm.TextBlock(warning))
	return res
}

// controlPlane 是协议面，不是侦察流程：没有它们，worker 就算测出了东西也交不
// 出来，或者路由漏挂时完全无法自救。
var controlPlane = map[string]string{
	"record_fact":    "写回事实",
	"add_finding":    "写回漏洞",
	"report_finding": "上报漏洞",
	"list_findings":  "查重",
	"Bash":           "通用恢复手段（路由漏挂时的兜底）",
	"Read":           "读取中间产物",
	"fetch_evidence": "反投毒取证通道（受限只读，常驻）",
}

// loopExactOptOut 是不进入精确调用止损的元工具。写回/读取类已在 controlPlane
// 里统一排除；这里只补路由器之外的会话元能力。
var loopExactOptOut = map[string]bool{
	ToolHelpToolName: true,
	"Skill":          true,
}

// loopCallKey 返回本轮止损使用的键：非控制面工具走「工具名 + 规范化完整入参」的
// 精确键。返回空串表示不纳入止损。
func loopCallKey(name string, raw json.RawMessage) string {
	if _, isControl := controlPlane[name]; isControl || loopExactOptOut[name] {
		return ""
	}
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&value) != nil {
		return ""
	}
	encoded, err := json.Marshal([]any{name, canonicalLoopValue(value)})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(encoded)
	return "exact:" + hex.EncodeToString(sum[:12])
}

// canonicalLoopValue 只去掉与目标语义无关的超时/体积字段；其余字段一字不改。
// json.Marshal 对 map 键排序，因此字段顺序不同仍会落到同一个精确键。
func canonicalLoopValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			switch strings.ToLower(k) {
			case "timeout_ms", "timeout", "max_body_bytes":
				continue
			}
			out[k] = canonicalLoopValue(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = canonicalLoopValue(val)
		}
		return out
	default:
		return v
	}
}

func classifyNegativeOutcome(name string, res actool.Result) (string, string) {
	text := res.Flatten()
	if res.IsError {
		sum := sha256.Sum256([]byte(text))
		return "tool_error:" + hex.EncodeToString(sum[:]), "工具返回错误；请区分参数、网络和服务端错误。"
	}
	return "", ""
}
