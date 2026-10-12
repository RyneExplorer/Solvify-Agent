package agent

import (
	"github.com/cloudwego/eino/schema"
)

type contextGroup struct{ Start, End int }

// completedToolGroups 只接纳完整的 assistant/tool_calls/全部对应结果。
// 最后一个完整组保留给下一次推理，活动组和含审批/失败事实的组永不摘要。
func completedToolGroups(messages []*schema.Message, protected ...map[string]bool) []contextGroup {
	var groups []contextGroup
	for i := 0; i < len(messages); i++ {
		m := messages[i]
		if m == nil || m.Role != schema.Assistant || len(m.ToolCalls) == 0 {
			continue
		}
		pending := map[string]bool{}
		valid := true
		for _, call := range m.ToolCalls {
			if call.Function.Name == clarifyToolName || isDangerousDynamicTool(call.Function.Name) {
				valid = false
			}
			for _, names := range protected {
				if names[call.Function.Name] {
					valid = false
				}
			}
			if call.ID == "" || pending[call.ID] {
				valid = false
			}
			pending[call.ID] = true
		}
		end := i + 1
		for end < len(messages) && len(pending) > 0 {
			result := messages[end]
			if result == nil || result.Role != schema.Tool || !pending[result.ToolCallID] {
				valid = false
				break
			}
			if protectedToolResult(result.Content) {
				valid = false
			}
			delete(pending, result.ToolCallID)
			end++
		}
		if valid && len(pending) == 0 {
			groups = append(groups, contextGroup{i, end})
		}
		i = max(i, end-1)
	}
	if len(groups) > 0 {
		return groups[:len(groups)-1]
	}
	return nil
}
