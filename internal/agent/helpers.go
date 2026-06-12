package agent

import "encoding/json"

// parseFinalAnswerWithConfidence 从 final_answer 工具参数中提取答案和置信度
func parseFinalAnswerWithConfidence(args string) (string, float64) {
	var params struct {
		Answer     string  `json:"answer"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(args), &params); err == nil {
		if params.Answer != "" {
			return params.Answer, params.Confidence
		}
	}
	return args, 0
}

// truncate 截断字符串
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
