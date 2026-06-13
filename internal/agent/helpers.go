package agent

import "fmt"

// truncate 截断字符串
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// joinSteps 将计划步骤格式化为带编号的字符串
func joinSteps(steps []string) string {
	result := ""
	for i, step := range steps {
		result += fmt.Sprintf("%d. %s\n", i+1, step)
	}
	return result
}
