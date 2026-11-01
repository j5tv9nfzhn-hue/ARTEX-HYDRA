package agent

import "strings"

// CanonicalSkillAlias handles only explicit historical aliases. Unknown custom
// names are preserved, never matched by substring or an ambiguous prefix.
func CanonicalSkillAlias(name string) string {
	// 这些别名指向的是 M15 之前内置的老技能名，那一批技能已整体下线。
	// 内置库换成 usestrix/strix 之后粒度更细（一个漏洞类一个技能），旧名没有
	// 一一对应的新技能，这里按场景就近映射，仅用于解析历史名字，不代表新库的分类。
	aliases := map[string]string{
		"api":                          "api_spec_testing",
		"apirecon":                     "api_spec_testing",
		"api-recon":                    "api_spec_testing",
		"api-inventory":                "api_spec_testing",
		"api-inventory-and-discovery":  "api_spec_testing",
		"web-vuln":                     "sql-injection",
		"web":                          "sql-injection",
		"web-vuln-tactics":             "sql-injection",
		"reverse":                      "source-aware-sast",
		"re":                           "source-aware-sast",
		"reverse-engineering":          "source-aware-sast",
		"exploit":                      "dependency-cve-scanning",
		"exploit-kb":                   "dependency-cve-scanning",
		"exploit-kb-patterns":          "dependency-cve-scanning",
		"triage":                       "counterevidence",
		"pivot":                        "counterevidence",
		"expert-triage-pivot":          "counterevidence",
		"sandbox":                      "supabase",
		"ai-sandbox":                   "supabase",
		"ai-coding":                    "supabase",
		"nextjs-sandbox":               "supabase",
		"wasm":                         "aws",
		"wasm-saas":                    "aws",
		"cloud-saas":                   "aws",
		"wasm-cloud":                   "aws",
	}
	name = strings.TrimSpace(name)
	if v, ok := aliases[strings.ToLower(name)]; ok {
		return v
	}
	return name
}
