package agent

import (
	"strings"
	"unicode"
)

// availableSkillEntry 是 Skill 元工具说明里的一条技能摘要。
type availableSkillEntry struct {
	name        string
	description string
}

// parseAvailableSkillsFromToolDesc 从 Skill 工具说明的 <available_skills> 块里
// 解析出技能名与一行描述，供 skill_recall 做二次召回。
func parseAvailableSkillsFromToolDesc(desc string) []availableSkillEntry {
	start := strings.Index(desc, "<available_skills>")
	end := strings.Index(desc, "</available_skills>")
	if start < 0 || end <= start {
		return nil
	}
	block := desc[start+len("<available_skills>") : end]
	lines := strings.Split(block, "\n")
	var out []availableSkillEntry
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- ") {
			continue
		}
		rest := strings.TrimPrefix(line, "- ")
		colon := strings.Index(rest, ":")
		if colon <= 0 {
			continue
		}
		name := strings.TrimSpace(rest[:colon])
		summary := strings.TrimSpace(rest[colon+1:])
		if name != "" {
			out = append(out, availableSkillEntry{name: name, description: summary})
		}
	}
	return out
}

func splitSkillTokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) ||
			r == '（' || r == '）' || r == '「' || r == '」' || r == '、' || r == '，' || r == '。' || r == '：' || r == '；' || r == '[' || r == ']'
	})
}
