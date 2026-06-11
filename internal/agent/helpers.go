package agent

import "encoding/json"

// parseFinalAnswer 从 final_answer 工具参数中提取答案
func parseFinalAnswer(args string) string {
	var params struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal([]byte(args), &params); err == nil && params.Answer != "" {
		return params.Answer
	}
	return args
}

// truncate 截断字符串
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
