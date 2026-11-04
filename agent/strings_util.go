package agent

import (
	"strings"
	"unicode/utf8"
)

// clipUTF8 按字节上限截断，并保证结果仍是合法 UTF-8。
func clipUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	b := []byte(s[:maxBytes])
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b)
}

// firstNonEmpty 返回第一个非空（去空格后）的字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
