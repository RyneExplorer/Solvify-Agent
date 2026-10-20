package agent

import (
	"encoding/json"
	"sort"
	"strings"

	bizerrors "solvify-agent/pkg/errors"
)

// 只识别工具协议中的状态字段，不从正文或data中的自然语言猜测执行结果。
type toolResultOutcome struct {
	Failed  bool
	Blocked bool
	Message string
}

func classifyToolResult(raw string) toolResultOutcome {
	return classifyToolEnvelope(raw, 0)
}

func classifyToolEnvelope(raw string, depth int) toolResultOutcome {
	var result struct {
		Success *bool  `json:"success"`
		IsError bool   `json:"isError"`
		Message string `json:"message"`
		Code    int    `json:"code"`
		Meta    struct {
			RateLimited bool `json:"ai.exa/rateLimited"`
		} `json:"_meta"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal([]byte(raw), &result) != nil {
		return toolResultOutcome{}
	}
	outcome := toolResultOutcome{Failed: result.Success != nil && !*result.Success || result.IsError || result.Meta.RateLimited}
	if outcome.Failed {
		outcome.Blocked = result.Code == bizerrors.CodeForbidden || result.Code == bizerrors.CodeUnauthorized
		outcome.Message = strings.TrimSpace(result.Message)
		if outcome.Message == "" && result.Meta.RateLimited {
			outcome.Message = "联网搜索已被限流，请稍后重试或调整工具配置"
		}
		if outcome.Message == "" {
			for _, content := range result.Content {
				if content.Type == "text" && strings.TrimSpace(content.Text) != "" {
					outcome.Message = content.Text
					break
				}
			}
		}
		if outcome.Message == "" {
			outcome.Message = "工具未返回成功结果"
		}
		outcome.Message = truncateTaskSummary(outcome.Message, 200)
		return outcome
	}
	// MCP的text可以携带内部工具的JSON响应；只展开有限协议层，不检查任意data。
	if depth < 2 {
		for _, content := range result.Content {
			if content.Type == "text" {
				if nested := classifyToolEnvelope(content.Text, depth+1); nested.Failed {
					return nested
				}
			}
		}
	}
	return outcome
}

func failedToolOutput(message string, code int) string {
	raw, _ := json.Marshal(struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Code    int    `json:"code,omitempty"`
	}{Message: message, Code: code})
	return string(raw)
}

func peToolResultEvent(toolName string, event Event, terminal bool) Event {
	outcome := classifyToolResult(event.ToolResult)
	if outcome.Failed {
		event.Status, event.Detail = TaskError, outcome.Message
		event.Retryable = !terminal && !outcome.Blocked
	}
	if message, recoverable := artifactArgumentFailure(toolName, event.ToolResult); recoverable && !terminal {
		event.Status, event.Retryable, event.Detail = TaskError, true, message
	}
	return event
}

// 与不可清除的Failures分开持久保存，只读纠错不重置审批、权限和副作用事实。
type peToolProgress struct {
	Succeeded int
	Failures  map[string]string
}

func recordPEToolResult(s *peState, taskID, toolName string, outcome toolResultOutcome) {
	if s.ToolProgress == nil {
		s.ToolProgress = map[string]peToolProgress{}
	}
	progress := s.ToolProgress[taskID]
	if outcome.Failed {
		if progress.Failures == nil {
			progress.Failures = map[string]string{}
		}
		progress.Failures[toolName] = outcome.Message
	} else {
		progress.Succeeded++
		// 相同工具的新参数可能指向另一文档，成功不能抹去此前失败的查询事实。
	}
	s.ToolProgress[taskID] = progress
}

func applyPEToolProgress(s *peState, result *TaskResult) {
	progress := s.ToolProgress[result.TaskID]
	if result.Status != TaskSuccess || len(progress.Failures) == 0 {
		return
	}
	names := make([]string, 0, len(progress.Failures))
	for name := range progress.Failures {
		names = append(names, name)
	}
	sort.Strings(names)
	limitations := make([]string, 0, len(names))
	for _, name := range names {
		limitations = append(limitations, name+"："+progress.Failures[name])
	}
	message := strings.Join(limitations, "；")
	if progress.Succeeded == 0 {
		result.Status, result.Error = TaskError, message
		return
	}
	// 限制放在预览前部，研究结果压缩时不能先丢掉未完成事实。
	result.Summary = "执行限制：曾有工具调用失败（" + message + "）。请以成功返回的证据为准，未取得的资料不能视为已验证。\n\n" + result.Summary
}
