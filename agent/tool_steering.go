package agent

import "strings"

// 只保留「模型提示块」的展示剥离逻辑。
//
// 旧的原生工具转向（http_request / http_sequence / http_fuzz / web_recon /
// net_probe / dns_probe / codec_crypto / extract_data / binary_inspect 等）已整体
// 移除；这些提示块本身仍可能出现在历史数据或 skill_recall 里，所以落库/展示前
// 继续剥掉，避免界面看到给模型看的指令。

const (
	steerOpenTag  = "<tool_steering>"
	steerCloseTag = "</tool_steering>"
	// steerJSONKey 是旧 http_* 工具把提示塞进结果 JSON 时用的字段名，保留用于清理历史数据。
	steerJSONKey = `"tool_steering"`
)

// modelHintTags 是所有「只给模型看、不该进活动流」的提示块（开标签, 闭标签）。
var modelHintTags = [][2]string{
	{steerOpenTag, steerCloseTag},
	{"<skill_recall>", "</skill_recall>"},
}

// stripModelHints 剥掉工具结果里的模型提示块，只用于**落库与界面**。
func stripModelHints(s string) string {
	if s == "" {
		return s
	}
	for _, tag := range modelHintTags {
		open, shut := tag[0], tag[1]
		for strings.Contains(s, open) {
			start := strings.Index(s, open)
			if rel := strings.Index(s[start:], shut); rel >= 0 {
				s = s[:start] + s[start+rel+len(shut):]
				continue
			}
			s = s[:start] // 截断的输出：切到结尾
			break
		}
	}
	if strings.Contains(s, steerJSONKey) {
		var b strings.Builder
		for _, line := range strings.Split(s, "\n") {
			if strings.Contains(line, steerJSONKey+":") {
				continue
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
		s = strings.ReplaceAll(b.String(), ",\n}", "\n}")
		s = strings.ReplaceAll(b.String(), ",\r\n}", "\r\n}")
	}
	return strings.TrimRight(s, "\n")
}
