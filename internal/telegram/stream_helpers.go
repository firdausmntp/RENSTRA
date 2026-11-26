package telegram

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// hasMeaningfulStreamText avoids flushing lone markdown prefixes like ">" or "**"
// before actual alphanumeric content arrives.
func hasMeaningfulStreamText(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	if len([]rune(trimmed)) >= 16 {
		return true
	}
	for _, r := range trimmed {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func formatToolNote(name, args string) string {
	detail := summarizeToolArg(name, args)
	if detail != "" {
		return fmt.Sprintf("⏳ Menjalankan `%s`: `%s`...", name, detail)
	}
	return fmt.Sprintf("⏳ Menjalankan `%s`...", name)
}

func summarizeToolArg(name, rawArgs string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &m); err != nil {
		return ""
	}
	var val string
	switch name {
	case "terminal":
		val, _ = m["cmd"].(string)
	case "read_file", "write_file", "patch", "send_file":
		val, _ = m["path"].(string)
	case "web_fetch":
		val, _ = m["url"].(string)
	case "web_search":
		val, _ = m["query"].(string)
	case "delegate":
		val, _ = m["task"].(string)
	case "send_telegram":
		if tid, ok := m["topic_id"].(float64); ok && tid > 0 {
			val = fmt.Sprintf("topik %d", int64(tid))
		}
	}
	val = strings.Join(strings.Fields(val), " ")
	if val == "" {
		return ""
	}
	val = strings.ReplaceAll(val, "`", "'")
	return truncate(val, 60)
}
