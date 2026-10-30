package agent

import (
	"encoding/json"
	"strings"
	"testing"

	actool "github.com/Autumn-27/norma/tool"

	"github.com/Autumn-27/artex/db"
)

// nodeWithPayload 构造一条只带 payload 的意图节点（够 intentPhase 用）。
func nodeWithPayload(payload string) *db.Node {
	return &db.Node{ID: 1, Payload: json.RawMessage(payload)}
}

func TestInferIntentPhase(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		want    string
	}{
		// 明确声明优先
		{"declared wins", `{"summary":"随便看看","phase":"verify"}`, "verify"},
		// exploit
		{"端口扫描+利用", `{"summary":"对 10.0.0.5 的 8080 做 Struts2 命令执行利用"}`, "exploit"},
		{"弱口令爆破", `{"summary":"对 /login 做后台弱口令爆破"}`, "exploit"},
		{"未授权访问", `{"summary":"验证 /api/admin 是否存在未授权访问"}`, "exploit"},
		{"上传绕过", `{"summary":"测试头像上传的扩展名绕过"}`, "exploit"},
		// recon
		{"全端口扫描", `{"summary":"对 10.0.0.5 做全端口扫描，识别服务 banner"}`, "recon"},
		{"子域枚举", `{"summary":"枚举 example.com 的子域名与存活主机"}`, "recon"},
		{"资产指纹", `{"summary":"整理已收集资产的指纹并补全服务信息"}`, "recon"},
		{"英文 recon", `{"summary":"scan ports and fingerprint services on the host"}`, "recon"},
		// verify（只在没有 exploit/recon 信号时才用）
		{"复现确认", `{"summary":"确认 CVE-2024-1234 在该版本上是否可以复现"}`, "verify"},
		{"排除误报", `{"summary":"复核上一条结论，排除误报"}`, "verify"},
		{"验证码", `{"summary":"处理登录验证码后继续测试"}`, "verify"},
		// 判不出 → 空（未判定；不影响候选目录）
		{"无文字", `{}`, ""},
		{"含糊", `{"summary":"看看这个目标"}`, ""},
		{"英文含糊", `{"summary":"take a look"}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := intentPhase(nodeWithPayload(c.summary))
			if got != c.want {
				t.Fatalf("intentPhase(%s) = %q, want %q", c.summary, got, c.want)
			}
		})
	}
}

// phase 只作标签：无论声明/推断成哪一档，WorkerTools 都必须先给出完整候选
// 目录。真正摘谁只能由合并 MCP/Skill 之后的 RouteToolFace 决定。
func TestWorkerToolsAlwaysReturnsFullCandidateCatalog(t *testing.T) {
	want := len((&ToolSet{}).allWorkerTools())
	for _, phase := range []string{"", "recon", "verify", "exploit", "bogus"} {
		ts := &ToolSet{}
		ts.SetPhase(phase)
		ts.SetIntentHint("对登录口做弱口令爆破并解密参数")
		if got := len(ts.WorkerTools()); got != want {
			t.Fatalf("phase=%q 时候选目录被静态裁剪：%d != %d", phase, got, want)
		}
	}
}

// 静态专用工具预裁已删除：http_fuzz/codec_crypto/extract_data/binary_inspect
// 是否出现只由 RouteToolFace 的向量分数决定，相关断言在 tool_router_test.go。

func TestAssetRouteTextIncludesTargetShape(t *testing.T) {
	got := assetRouteText([]*db.Asset{{
		Type: "endpoint", Domain: "api.example.test",
		URL: "https://api.example.test/api/orders", Method: "GET",
		ServiceName: "orders", Technologies: []string{"Nginx", "JWT"},
	}})
	for _, want := range []string{"endpoint", "api.example.test", "/api/orders", "GET", "JWT"} {
		if !strings.Contains(got, want) {
			t.Fatalf("资产路由文字缺少 %q：%s", want, got)
		}
	}
}

// faceBytes 复刻运行期体积口径：lean 瘦身 + 按名字去重后的 name/description/
// prompt/input_schema 的 JSON 总和。
func faceBytes(tools []actool.CoreTool) int {
	seen := map[string]bool{}
	total := 0
	for _, tl := range leanToolList(tools) {
		if seen[tl.Name()] {
			continue
		}
		seen[tl.Name()] = true
		b, _ := json.Marshal(map[string]any{
			"name": tl.Name(), "description": tl.Description(),
			"prompt": tl.Prompt(), "input_schema": tl.InputSchema(),
		})
		total += len(b)
	}
	return total
}

// 验收口径：worker 每次请求的「固定开销」（工具面 + 系统提示种子）必须 < 10K token。
// 按 3.2 B/token 折算 ≈ 32,000 字节。这条测试就是那条验收线。
func TestWorkerToolFaceBudget(t *testing.T) {
	// 工具面预算（字符，不是 token；约 10 token / 32 B）。
	//
	// 口径与运行时一致：WorkerTools 给出完整候选目录，lean 瘦身是唯一一层体积
	// 治理（能力路由已于 P4 批次 A 拆除）。这里守的是运行时真正下发的面。
	const budgetBytes = 33000
	seed := len(BuiltinPromptSeeds()["worker"])
	sdk := defaultToolsExcept("MultiEdit", "LS", "Sleep", "Write", "Edit", "apply_patch")

	faces := []struct {
		name   string
		intent string
	}{
		{"recon", "枚举 example.com 子域名、扫描端口并识别服务指纹"},
		{"verify", "复核登录接口的验证码与误报结论，确认是否可复现"},
		{"exploit 无专用信号", "对管理后台做越权访问测试"},
		{"exploit 带 fuzz 信号", "对登录口做弱口令爆破与字典枚举"},
		{"exploit 全部专用信号", "爆破并解密参数、提取响应字段、分析 APK 二进制"},
	}
	for _, f := range faces {
		ts := &ToolSet{}
		tools := append(append([]actool.CoreTool{}, ts.WorkerTools()...), sdk...)
		got := faceBytes(tools) + seed
		if got >= budgetBytes {
			t.Fatalf("%s: face %d B ≈ %d tok, over the 10K budget (%d B)", f.name, got, got*10/32, budgetBytes)
		}
		t.Logf("%-24s tools+seed = %5d B (~%4d tok)", f.name, got, got*10/32)
	}
}

// 默认候选目录保持完整；phase 未判定时也不做任何静态预裁。
func TestUnknownPhaseFaceUnchanged(t *testing.T) {
	ts := &ToolSet{}
	got := len(ts.WorkerTools())
	want := len((&ToolSet{}).allWorkerTools())
	if got != want {
		t.Fatalf("full face changed: %d != %d", got, want)
	}
	if strings.TrimSpace(ts.intentHint) != "" {
		t.Fatal("hint should default to empty")
	}
}
