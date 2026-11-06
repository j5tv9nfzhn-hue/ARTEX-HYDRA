package server

// 子代理实时事件中心。
//
// 设计取舍（对齐 OpenHands 的「事件流是唯一真相源」与 LangGraph 的「状态事件驱动 UI」）：
//
//  1. **独立于对话流**：这里是一条单独的 SSE。子代理刷日志不会挤占主聊天的连接与
//     渲染，反过来主聊天卡住也不影响抽屉刷新。两条流各自重连、各自续传。
//  2. **只读投影**：hub 只订阅 Coordinator 的 OnChange 并转发，绝不回调调度层。
//     观察者一旦能改状态，就成了第二套控制面。
//  3. **有界内存**：全局环形缓冲 + 每订阅者带缓冲的通道，满了就丢最旧的并置
//     overflow 标记。子代理可能一秒几十条，无限队列会先把服务端内存吃光。
//  4. **可重放**：每条事件带单调 seq，断线重连带 Last-Event-ID/since 补发；
//     补不到（超出环形缓冲）就回落到一次全量 snapshot，界面不会停在旧状态。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Autumn-27/artex/agent"
)

const (
	// agentHubRing 是事件环形缓冲容量。够覆盖一次断线重连，又不至于把长任务的
	// 全部日志留在内存里（真正的历史在库与转录里，这里只服务实时视图）。
	agentHubRing = 2000
	// agentSubBuffer 是单个订阅者的积压上限：超了就丢帧并让它重新同步。
	agentSubBuffer = 256
	// agentSlotsTick 是槽位快照的推送间隔。槽位变化不一定伴随 agent 事件
	// （僵死回收、排队进出），所以用低频快照兜底，而不是高频轮询。
	agentSlotsTick = 8 * time.Second
)

// AgentEvent 是发给前端的一帧。字段刻意扁平：前端按 agent_id 归拢即可，
// 不需要理解协程/chan 这些服务端概念。
type AgentEvent struct {
	Seq      int64     `json:"seq"`
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	TaskID   string    `json:"task_id,omitempty"`
	AgentID  string    `json:"agent_id,omitempty"`
	ParentID string    `json:"parent_id,omitempty"`
	State    string    `json:"state,omitempty"`
	Summary  string    `json:"summary,omitempty"`
}

// AgentSnapshot 是全量状态：首次连上、断线补不齐、以及定期兜底都用它。
type AgentSnapshot struct {
	Seq    int64                       `json:"seq"`
	At     time.Time                   `json:"at"`
	Agents []agent.AgentView           `json:"agents"`
	Slots  map[string]agent.SlotStatus `json:"slots"`
}

type agentHub struct {
	mu     sync.Mutex
	seq    int64
	ring   []AgentEvent
	subs   map[int]chan AgentEvent
	nextID int
	coords map[*agent.Coordinator]struct{}
}

func newAgentHub() *agentHub {
	return &agentHub{
		ring:   make([]AgentEvent, 0, agentHubRing),
		subs:   make(map[int]chan AgentEvent),
		coords: make(map[*agent.Coordinator]struct{}),
	}
}

// attach 把一个协调器接进 hub。重复 attach 是幂等的：同一个任务可能被
// 多次装配（重跑、恢复），重复订阅会让每条事件被放大成多份。
func (h *agentHub) attach(c *agent.Coordinator) {
	if h == nil || c == nil {
		return
	}
	h.mu.Lock()
	if _, ok := h.coords[c]; ok {
		h.mu.Unlock()
		return
	}
	h.coords[c] = struct{}{}
	h.mu.Unlock()

	taskID := c.TaskID
	c.OnChange(func(ev agent.Event) {
		h.publish(AgentEvent{
			At:       ev.At,
			Kind:     ev.Kind,
			TaskID:   taskID,
			AgentID:  ev.AgentID,
			ParentID: ev.ParentID,
			State:    string(ev.State),
			Summary:  ev.Summary,
		})
	})
}

// publish 写入环形缓冲并扇出。扇出是非阻塞的：慢订阅者丢帧，不拖累调度层。
func (h *agentHub) publish(ev AgentEvent) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.seq++
	ev.Seq = h.seq
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	if len(h.ring) >= agentHubRing {
		h.ring = h.ring[1:]
	}
	h.ring = append(h.ring, ev)
	for id, ch := range h.subs {
		select {
		case ch <- ev:
		default:
			// 积压过深：断开这个订阅者，让它重连后走 since 补发/全量快照，
			// 好过在服务端无限堆积。
			delete(h.subs, id)
			close(ch)
		}
	}
	h.mu.Unlock()
}

// subscribe 返回 since 之后的历史事件、一个实时通道，以及取消函数。
// replay 为 false 表示历史已被环形缓冲淘汰，调用方应先发一次全量 snapshot。
func (h *agentHub) subscribe(since int64) (replay []AgentEvent, live <-chan AgentEvent, ok bool, cancel func()) {
	if h == nil {
		ch := make(chan AgentEvent)
		return nil, ch, true, func() {}
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	oldest := h.seq + 1
	if len(h.ring) > 0 {
		oldest = h.ring[0].Seq
	}
	ok = since == 0 || since+1 >= oldest
	if ok {
		for _, ev := range h.ring {
			if ev.Seq > since {
				replay = append(replay, ev)
			}
		}
	}
	ch := make(chan AgentEvent, agentSubBuffer)
	id := h.nextID
	h.nextID++
	h.subs[id] = ch
	return replay, ch, ok, func() {
		h.mu.Lock()
		if cur, exists := h.subs[id]; exists {
			delete(h.subs, id)
			close(cur)
		}
		h.mu.Unlock()
	}
}

// snapshot 汇总当前所有协调器的 agent 与槽位状态。
func (h *agentHub) snapshot() AgentSnapshot {
	out := AgentSnapshot{At: time.Now(), Agents: []agent.AgentView{}, Slots: map[string]agent.SlotStatus{}}
	if h == nil {
		return out
	}
	h.mu.Lock()
	coords := make([]*agent.Coordinator, 0, len(h.coords))
	for c := range h.coords {
		coords = append(coords, c)
	}
	out.Seq = h.seq
	h.mu.Unlock()

	for _, c := range coords {
		for _, v := range c.All() {
			if v.TaskID == "" {
				v.TaskID = c.TaskID
			}
			out.Agents = append(out.Agents, v)
		}
		out.Slots[c.TaskID] = c.SlotsStatus()
	}
	return out
}

// filterSnapshot 按任务过滤快照：前端的停靠栏只该显示「当前会话所属任务」的
// 子代理，其他后台任务的运行情况不该混进来（否则用户会看到一堆与当前对话无关
// 的 agent，反而分不清谁是谁）。
func filterSnapshot(snap AgentSnapshot, taskID string) AgentSnapshot {
	if taskID == "" {
		return snap
	}
	agents := make([]agent.AgentView, 0, len(snap.Agents))
	for _, a := range snap.Agents {
		if a.TaskID == taskID {
			agents = append(agents, a)
		}
	}
	slots := map[string]agent.SlotStatus{}
	if st, ok := snap.Slots[taskID]; ok {
		slots[taskID] = st
	}
	return AgentSnapshot{Seq: snap.Seq, At: snap.At, Agents: agents, Slots: slots}
}

// streamAgents 是子代理事件流：GET /api/agents/stream?since=<seq>&task_id=<id>
//
// 首帧固定是 snapshot（前端据此建树），随后是增量 event；每 8s 补一帧 slots，
// 每 20s 一行注释心跳（防止中间代理把空闲连接掐掉）。
func (s *Server) streamAgents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	since := int64(atoiDefault(r.URL.Query().Get("since"), 0))
	if le := r.Header.Get("Last-Event-ID"); le != "" {
		if n, err := strconv.ParseInt(le, 10, 64); err == nil {
			since = n
		}
	}
	taskFilter := strings.TrimSpace(r.URL.Query().Get("task_id"))

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	send := func(event string, id int64, v any) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return true
		}
		if id > 0 {
			fmt.Fprintf(w, "id: %d\n", id)
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
		return true
	}

	replay, live, ok, cancel := s.agentHub.subscribe(since)
	defer cancel()

	if !ok {
		send("resync", 0, map[string]any{"reason": "cursor_expired"})
	}
	send("snapshot", 0, filterSnapshot(s.agentHub.snapshot(), taskFilter))
	for _, ev := range replay {
		if taskFilter != "" && ev.TaskID != "" && ev.TaskID != taskFilter {
			continue
		}
		send("agent", ev.Seq, ev)
	}

	slots := time.NewTicker(agentSlotsTick)
	defer slots.Stop()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-live:
			if !open {
				// 积压过深被 hub 断开：让浏览器按 Last-Event-ID 重连，
				// 重连时 since 若已过期会自动收到 resync + 全量快照。
				return
			}
			if taskFilter != "" && ev.TaskID != "" && ev.TaskID != taskFilter {
				continue // 别的任务的事件不推给这个会话
			}
			send("agent", ev.Seq, ev)
		case <-slots.C:
			send("slots", 0, filterSnapshot(s.agentHub.snapshot(), taskFilter).Slots)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
