package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

func workflowCall(t *testing.T, ctx context.Context, tool actool.CoreTool, input any, wantError bool) string {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	r, err := tool.Call(ctx, raw, nil)
	if err != nil || r.IsError != wantError {
		t.Fatalf("%s: error=%v result=%s", tool.Name(), err, r.Flatten())
	}
	return r.Flatten()
}

func workflowTool(t *testing.T, tools []actool.CoreTool, name string) actool.CoreTool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name() == name {
			return tool
		}
	}
	t.Fatalf("missing tool: %s", name)
	return nil
}

func TestFindingWorkflowAutoHintToPlannerAndSetting(t *testing.T) {
	s, initial, request := trafficEvidenceServer(t)
	pg := s.m.pg
	ctx := context.Background()
	if _, err := pg.Exec(`DELETE FROM settings WHERE key=$1`, settingAgentTrafficBinding); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.SetBool(settingAgentTrafficBinding, false) })
	if s.settingsPayload()[settingAgentTrafficBinding] != false {
		t.Fatal("missing setting must default off")
	}
	seedServerEvidenceFlow(t, s, "handoff-proof", []byte("verified local proof"))
	seedServerEvidenceFlow(t, s, "handoff-baseline", []byte("local baseline"))
	seedServerEvidenceFlow(t, s, "handoff-verification", []byte{0, 1, 255})
	// Manual binding is independent of the Agent switch.
	if r := request("POST", fmt.Sprintf("/api/exploration/findings/%d/traffic", initial.FindingID), `{"traffic_refs":[{"traffic_id":"handoff-proof"}]}`); r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	bind := s.toolBindFindingTraffic()
	input := map[string]any{"finding_id": initial.FindingID, "traffic_refs": []db.TrafficRef{{TrafficID: "handoff-baseline"}}}
	workflowCall(t, ctx, bind, input, true)
	if r := request("PUT", "/api/settings", `{"agent_traffic_binding":true}`); r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	if !pg.GetBool(settingAgentTrafficBinding, false) {
		t.Fatal("switch was not persisted")
	}
	f, _ := pg.GetFinding(initial.FindingID)
	task := s.m.ResolveTask(fmt.Sprint(*f.TaskID))
	refs := []db.TrafficRef{{TrafficID: "handoff-baseline", Role: "baseline", Note: "normal response"}, {TrafficID: "handoff-proof", Role: "proof", Note: "proves the finding"}, {TrafficID: "handoff-verification", Role: "verification", Note: "binary verification"}}
	// Real cross-task Auto hint handler -> persisted graph -> Planner tool.
	hintResult := workflowCall(t, ctx, s.toolAddHint(), map[string]any{"task_id": task.ID, "hints": []any{map[string]any{"text": "Report the confirmed local finding with its verified evidence", "traffic_refs": refs}}}, false)
	var hints struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.Unmarshal([]byte(hintResult), &hints); err != nil || len(hints.IDs) != 1 || hints.IDs[0] == 0 {
		t.Fatal(hintResult, err)
	}
	graph := workflowCall(t, ctx, s.toolGetTaskGraph(), map[string]any{"task_id": task.ID}, false)
	for _, ref := range refs {
		if !strings.Contains(graph, ref.TrafficID) {
			t.Fatal("handoff lost reference", ref.TrafficID)
		}
	}
	for range 6 {
		if _, err := task.Store.AddNode(db.KindFact, map[string]any{"summary": "ID separation"}, 0, "confirmed", "test", nil); err != nil {
			t.Fatal(err)
		}
	}
	ts := agent.NewToolSet(task.Store, "planner")
	ts.SetTaskID(*f.TaskID)
	ts.SetFindingRecorder(s.evidenceStore())
	notices := 0
	ts.SetNotifyFinding(func(int64, string) { notices++ })
	tools, def, cleanup := agent.AugmentTools(ctx, "planner", ts.PlannerTools())
	defer cleanup()
	if !strings.Contains(def.FindingGuidance, "evidence_hint_id") || !strings.Contains(def.FindingGuidance, "取消 Worker") {
		t.Fatal("Planner missed runtime guidance")
	}
	report := workflowTool(t, tools, "report_finding")
	// report_finding 的必填面（反证闭环四字段 + CVSS 八项，见 agent/tools.go）：这条用例
	// 测的是「hint → Planner → 入库并绑定流量」的交接链路，所以补一份合格样本。
	// 逐字段赋值而不是写一个字面量：字面量的对齐由 gofmt 按最长键算，手写容易差一格。
	payload := map[string]any{"vulnclass": "TEST", "severity": "low", "summary": "Planner handoff fixture", "evidence_hint_id": hints.IDs[0]}
	payload["counterevidence"] = "该路径未见输入校验、WAF 或鉴权检查；已分别以已登录/未登录测过"
	payload["confidence"] = "medium"
	payload["confidence_rationale"] = "仅有静态链路，没能起服务复现"
	payload["severity_change_conditions"] = "确认该路由暴露在未认证公网流量下则升为 critical"
	// 3.0 分（AV:L/AC:H/PR:H/UI:N/S:U/C:L/I:L/A:N）→ low 档，与上面的 severity 一致。
	payload["cvss_breakdown"] = map[string]any{
		"attack_vector": "L", "attack_complexity": "H", "privileges_required": "H", "user_interaction": "N",
		"scope": "U", "confidentiality": "L", "integrity": "L", "availability": "N",
	}
	result := workflowCall(t, ctx, report, payload, false)
	var recorded db.RecordedFinding
	if err := json.Unmarshal([]byte(strings.SplitN(result, "\n", 2)[1]), &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.FindingID == recorded.NodeID || len(recorded.Traffic.Bindings) != 3 || notices != 1 {
		t.Fatal("bad finding/traffic result", result)
	}
	for i, b := range recorded.Traffic.Bindings {
		if b.Snapshot.SourceTrafficID != refs[i].TrafficID || b.Role != refs[i].Role || b.Note != refs[i].Note {
			t.Fatalf("handoff mismatch: %+v", b)
		}
	}
	list := workflowCall(t, ctx, s.toolListTaskFindings(), map[string]any{"task_id": task.ID}, false)
	var nodes []struct {
		ID            int64 `json:"id"`
		FindingID     int64 `json:"finding_id"`
		FindingNodeID int64 `json:"finding_node_id"`
		Count         int   `json:"traffic_count"`
	}
	if err := json.Unmarshal([]byte(list), &nodes); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range nodes {
		if n.ID == recorded.NodeID {
			found = n.FindingID == recorded.FindingID && n.FindingNodeID == recorded.NodeID && n.Count == 3
		}
	}
	if !found {
		t.Fatal("canonical IDs/count missing", list)
	}
	workflowCall(t, ctx, s.toolGetFindingTraffic(), map[string]any{"finding_id": recorded.FindingID}, false)
	wrong := workflowCall(t, ctx, s.toolGetFindingTraffic(), map[string]any{"finding_id": int64(900000000000000000)}, true)
	if !strings.Contains(wrong, "独立漏洞记录 ID") {
		t.Fatal("ambiguous ID error", wrong)
	}
	input["finding_id"] = recorded.FindingID
	// Duplicate append preserves metadata and version.
	workflowCall(t, ctx, bind, input, false)
	after, err := pg.GetFindingTraffic(ctx, recorded.FindingID)
	if err != nil || len(after.Bindings) != 3 || after.Version != recorded.Traffic.Version || after.Bindings[0].Note != refs[0].Note {
		t.Fatal("duplicate changed evidence", after, err)
	}
	// A malformed handoff must not create a partial finding or notify Planner.
	badHint := workflowCall(t, ctx, s.toolAddHint(), map[string]any{"task_id": task.ID, "text": "missing packet", "traffic_refs": []db.TrafficRef{{TrafficID: "missing"}}}, false)
	var badID int64
	fmt.Sscanf(badHint, "hint added: %d", &badID)
	workflowCall(t, ctx, report, map[string]any{"vulnclass": "TEST", "severity": "low", "summary": "must fail", "evidence_hint_id": badID}, true)
	if notices != 1 {
		t.Fatal("failed handoff notified before commit")
	}
	child, err := s.m.CreateTaskWithOptions("inherited handoff", "fixture", db.TaskCreateOptions{SourceTaskIDs: []int64{*f.TaskID}})
	if err != nil {
		t.Fatal(err)
	}
	childID, _ := strconv.ParseInt(child.ID, 10, 64)
	childRow, _ := pg.GetTask(childID)
	t.Cleanup(func() { pg.DeleteTask(childRow.ID) })
	childCtx := agent.WithRunInfo(ctx, agent.RunInfo{TaskID: childRow.ID})
	workflowCall(t, childCtx, bind, input, true)
	workflowCall(t, childCtx, s.toolGetFindingTraffic(), map[string]any{"finding_id": recorded.FindingID}, false)
	// Existing, already assembled tools must honor a later switch-off.
	if r := request("PUT", "/api/settings", `{"agent_traffic_binding":false}`); r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	workflowCall(t, ctx, bind, input, true)
	// Turning binding off must still save a confirmed finding from an already
	// assembled tool, even if the model sends the old optional evidence fields.
	// 关掉自动绑定只是不再要求 traffic_refs，反证闭环四字段与 CVSS 八项仍是
	// report_finding 的必填面，不会随开关一起消失。这里复用上面那份合格样本，
	// 只把 summary 换掉以便与上一份区分 —— 继续传「旧的四个可选字段」会被
	// stage=tool_validate 拦下，与本用例要验证的「关开关仍能入库」不是一回事。
	offPayload := make(map[string]any, len(payload)+2)
	for k, v := range payload {
		offPayload[k] = v
	}
	offPayload["summary"] = "off"
	offPayload["evidence_hint_id"] = hints.IDs[0]
	offResult := workflowCall(t, ctx, report, offPayload, false)
	var offRecord struct {
		db.RecordedFinding
		EvidenceStatus string `json:"evidence_status"`
		EvidenceNote   string `json:"evidence_note"`
	}
	if err := json.Unmarshal([]byte(strings.SplitN(offResult, "\n", 2)[1]), &offRecord); err != nil {
		t.Fatal(err)
	}
	if offRecord.FindingID <= 0 || len(offRecord.Traffic.Bindings) != 0 || offRecord.EvidenceStatus != "not_bound" || !strings.Contains(offRecord.EvidenceNote, "已关闭") {
		t.Fatal("disabled binding discarded finding or bound evidence", offResult)
	}
	// 同上：无包场景只豁免流量引用，不豁免反证闭环必填面。
	noPacket := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		noPacket[k] = v
	}
	noPacket["vulnclass"] = "TCP"
	noPacket["summary"] = "no packet needed"
	delete(noPacket, "evidence_hint_id") // 无包场景本就不该带引用
	workflowCall(t, ctx, report, noPacket, false)
	if notices != 3 {
		t.Fatal("optional no-packet report failed")
	}
	workflowCall(t, ctx, s.toolGetFindingTraffic(), map[string]any{"finding_id": recorded.FindingID}, false)
	tools, def, closeTools := agent.AugmentTools(ctx, "planner", ts.PlannerTools())
	defer closeTools()
	if def.FindingGuidance != "" {
		t.Fatal("disabled feature still injects guidance")
	}
	for _, tool := range tools {
		if tool.Name() == "bind_finding_traffic" {
			t.Fatal("disabled binding tool exposed")
		}
	}
}

func TestFindingWorkflowMigrationPreservesUserConfiguration(t *testing.T) {
	s, _, _ := trafficEvidenceServer(t)
	pg := s.m.pg
	// 只列 tools 表里**确实有行**的工具。两处已经不存在，原因不同：
	//   · add_task_hint：第六阶段（626bda05）从模型可见工具面上摘掉，既不在
	//     BuiltinToolSeeds 也不在白名单，表里不会有它的行；
	//   · bind_finding_traffic / get_finding_traffic：走内存注册
	//     （AllDomainTools + AugmentTools），不经 tools 表 seed。
	//     ToolResolve 对「表里没有行」的工具原样放行、不由表控制，所以本用例
	//     要验证的「迁移不覆盖用户配置」对它们根本无从谈起。
	// traffic_search / traffic_get / traffic_blob 由 traffic.SeedToolMetas()
	// 正常 seed，是这条迁移链路真正作用的对象，必须保留。
	for _, key := range []string{"report_finding", "add_hint", "traffic_search", "traffic_get", "traffic_blob"} {
		old, err := pg.GetTool(key)
		if err != nil || old == nil {
			t.Fatal("missing tool", key, err)
		}
		t.Cleanup(func() {
			bindings, _ := json.Marshal(old.Agents)
			pg.UpdateTool(key, old.Description, old.Schema, bindings, old.Enabled)
		})
	}
	custom := json.RawMessage(`{"type":"object","properties":{"text":{"type":"string","description":"USER TEXT"},"hints":{"type":"array","items":{"type":"object","properties":{"text":{"type":"string","description":"USER ITEM"}}}}},"required":["text"]}`)
	for _, key := range []string{"report_finding", "add_hint"} {
		if err := pg.UpdateTool(key, "USER DESCRIPTION", custom, json.RawMessage(`["custom-agent"]`), false); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"traffic_search", "traffic_get"} {
		row, _ := pg.GetTool(key)
		bindings := json.RawMessage(`["custom-agent"]`)
		if key == "traffic_search" {
			bindings = json.RawMessage(`["worker"]`)
		}
		if err := pg.UpdateTool(key, row.Description, row.Schema, bindings, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := pg.SetSetting("finding_workflow_tools_v2_reporter", "false"); err != nil {
		t.Fatal(err)
	}
	s.seedFindingWorkflowTools()
	for _, key := range []string{"report_finding", "add_hint"} {
		row, _ := pg.GetTool(key)
		if row.Enabled || row.Description != "USER DESCRIPTION" || len(row.Agents) != 1 || row.Agents[0] != "custom-agent" {
			t.Fatal("changed user tool configuration", key)
		}
		if !strings.Contains(string(row.Schema), "USER TEXT") || !strings.Contains(string(row.Schema), "USER ITEM") {
			t.Fatal("changed user schema", key)
		}
		field := "traffic_refs"
		if key == "report_finding" {
			field = "evidence_hint_id"
		}
		if !strings.Contains(string(row.Schema), field) {
			t.Fatal("missing optional field", key)
		}
	}
	search, _ := pg.GetTool("traffic_search")
	get, _ := pg.GetTool("traffic_get")
	if !strings.Contains(search.Description, "支持裸主机、主机:端口或完整 URL") {
		t.Fatal("traffic_search description migration missing host/port guidance")
	}
	if search.Enabled || !contains(search.Agents, "reporter") || get.Enabled || len(get.Agents) != 1 || get.Agents[0] != "custom-agent" {
		t.Fatal("default/custom reader binding migration incorrect")
	}
	if err := pg.RemoveAgentFromTool("reporter", "traffic_search"); err != nil {
		t.Fatal(err)
	}
	s.seedFindingWorkflowTools()
	search, _ = pg.GetTool("traffic_search")
	if contains(search.Agents, "reporter") {
		t.Fatal("one-time migration undid later unbinding")
	}
}

func TestFindingWorkflowReporterBindsBeforeWritingReport(t *testing.T) {
	s, f, request := trafficEvidenceServer(t)
	pg := s.m.pg
	ctx := context.Background()
	if err := pg.SetBool(settingAgentTrafficBinding, true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.SetBool(settingAgentTrafficBinding, false); s.m.SetTrafficEnabled(false) })
	if err := s.m.SetTrafficEnabled(true); err != nil {
		t.Fatal(err)
	}
	seedServerEvidenceFlow(t, s, "reporter-proof", []byte("local proof payload"))
	seedServerEvidenceFlow(t, s, "reporter-baseline", []byte("local normal response"))
	// 两处与现状对齐的改动：
	//
	// 1) 不再用 "reporter"。预置「报告撰写」agent 的 seedReporterAgent() 调用被
	//    第六阶段（626bda05）删除，库里不再有 reporter 这个 agent，传它的 key
	//    只会拿到 0 个工具和空引导。流量绑定工作流本身仍然活着
	//    （findingWorkflowTools 对每个带 report_finding 的 agent 都注入），
	//    所以改用仍然存在的 planner 来守这条注入。
	//
	// 2) 基础工具列表要显式带上宿主工具。traffic_* / bind_finding_traffic /
	//    get_finding_traffic / update_finding_report 都不在任何 agent 的
	//    ToolSet 里，AugmentTools 传 nil 时一个也拿不到（实测 0 个工具）。
	//    这里按 C1 的惯例显式拼进去，本用例要跑的证据→绑定→写报告链路才成立。
	//
	// 用 worker 而不是 planner：traffic_search / traffic_get / traffic_blob 由
	// traffic.SeedToolMetas() 以 agents=["worker"] 落库，ToolResolve 对有库行的
	// 工具按 agents 过滤，换成别的 agent 会先把它们挡掉，后面的链路根本跑不到。
	ts := agent.NewToolSet(nil, "worker")
	base := append(ts.WorkerTools(), s.m.traffic.Tools()...)
	base = append(base, s.toolGetTaskNodeDetail(), s.toolBindFindingTraffic(),
		s.toolGetFindingTraffic(), s.toolUpdateFindingReport())
	tools, def, cleanup := agent.AugmentTools(ctx, "worker", base)
	defer cleanup()
	// 这里原先断言的两串文案（"报告前自动关联流量" / "绑定成功后重新调用"）在全仓
	// 只出现在本文件里——实现侧从来没产出过它们，断言是对着一份不存在的文案写的。
	// 真正由 findingWorkflowTools 注入、且承载同样意图的是下面这两条：
	//   1. 自动绑定由报告 Agent 在「漏洞入库后、编写报告前」完成（即"报告前关联流量"）；
	//   2. 引用只接受**已核实**的 ID（防止模型拿域名/时间猜 ID 后宣称绑定成功）。
	// 断言改成实际注入的口径，守的仍是"reporter 必须拿到绑定工作流"这件事。
	if !strings.Contains(def.FindingGuidance, "自动绑定默认由报告 Agent 在漏洞入库后、编写报告前完成") ||
		!strings.Contains(def.FindingGuidance, "只接受**已核实**的引用") {
		t.Fatalf("reporter did not receive binding workflow, got: %q", def.FindingGuidance)
	}
	// get_task_worker_trace 不再列在这里：它在官方白名单内有库行，agents 绑定的是
	// planner / mainagent，worker 解析不到；而本用例跑的证据链路也用不到它。
	for _, name := range []string{"traffic_search", "traffic_get", "get_task_node_detail", "bind_finding_traffic", "get_finding_traffic", "update_finding_report"} {
		workflowTool(t, tools, name)
	}
	finding, _ := pg.GetFinding(f.FindingID)
	detail := workflowCall(t, ctx, workflowTool(t, tools, "get_task_node_detail"), map[string]any{"task_id": fmt.Sprint(*finding.TaskID), "id": f.NodeID}, false)
	if !strings.Contains(detail, `"finding_id"`) || !strings.Contains(detail, `"finding_node_id"`) {
		t.Fatal("reporter lacks explicit ID mapping")
	}
	workflowCall(t, ctx, workflowTool(t, tools, "traffic_search"), map[string]any{"host": "evidence.local"}, false)
	for _, id := range []string{"reporter-baseline", "reporter-proof"} {
		packet := workflowCall(t, ctx, workflowTool(t, tools, "traffic_get"), map[string]any{"id": id}, false)
		if !strings.Contains(packet, "local") {
			t.Fatal("reporter cannot inspect packet", packet)
		}
	}
	refs := []db.TrafficRef{{TrafficID: "reporter-baseline", Role: "baseline"}, {TrafficID: "reporter-proof", Role: "proof", Note: "confirmed from recorded response"}}
	workflowCall(t, ctx, workflowTool(t, tools, "bind_finding_traffic"), map[string]any{"finding_id": f.FindingID, "traffic_refs": refs}, false)
	list := workflowCall(t, ctx, workflowTool(t, tools, "get_finding_traffic"), map[string]any{"finding_id": f.FindingID}, false)
	var summary struct {
		Version int64 `json:"version"`
	}
	if err := json.Unmarshal([]byte(list), &summary); err != nil || summary.Version != 1 {
		t.Fatal(list, err)
	}
	workflowCall(t, ctx, workflowTool(t, tools, "update_finding_report"), map[string]any{"finding_id": f.NodeID, "evidence_version": summary.Version, "report": "## Local report\n\nVerified baseline and proof using saved evidence."}, false)
	updated, err := pg.GetFinding(f.FindingID)
	if err != nil || updated.ReportEvidenceVersion != summary.Version || updated.EvidenceVersion != summary.Version {
		t.Fatal("report did not cover post-binding version", updated, err)
	}
	// Disabling automatic binding still lets Reporter read manually bound snapshots.
	if r := request("PUT", "/api/settings", `{"agent_traffic_binding":false}`); r.Code != 200 {
		t.Fatal(r.Code, r.Body)
	}
	off, offDef, closeOff := agent.AugmentTools(ctx, "planner", base)
	defer closeOff()
	if offDef.FindingGuidance != "" {
		t.Fatal("off reporter still receives auto-binding guidance")
	}
	for _, tool := range off {
		if tool.Name() == "traffic_search" || tool.Name() == "traffic_get" || tool.Name() == "traffic_blob" || tool.Name() == "bind_finding_traffic" {
			t.Fatal("auto-binding tool exposed while off", tool.Name())
		}
	}
	workflowCall(t, ctx, workflowTool(t, off, "get_finding_traffic"), map[string]any{"finding_id": f.FindingID}, false)
	workflowTool(t, off, "update_finding_report")
}
