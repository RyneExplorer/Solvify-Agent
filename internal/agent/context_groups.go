package agent

import (
	"strings"

	"github.com/cloudwego/eino/schema"
)

type contextGroup struct{ Start, End int }

// completedConversationGroups 选取当前问题之前的完整普通问答。
// 最近一组保留原文，摘要、系统消息、工具轨迹及执行事实不作为普通问答压缩。
func completedConversationGroups(messages []*schema.Message) []contextGroup {
	lastUser := -1
	for i, message := range messages {
		if message != nil && message.Role == schema.User && !strings.HasPrefix(message.Content, runtimeSummaryPrefix) {
			lastUser = i
		}
	}
	var groups []contextGroup
	for i := 0; i+1 < lastUser; i++ {
		user, answer := messages[i], messages[i+1]
		if user == nil || answer == nil || user.Role != schema.User || answer.Role != schema.Assistant ||
			strings.HasPrefix(user.Content, runtimeSummaryPrefix) || len(answer.ToolCalls) != 0 || protectedToolResult(answer.Content) {
			continue
		}
		if i+2 < len(messages) && messages[i+2] != nil && messages[i+2].Role == schema.User {
			groups = append(groups, contextGroup{i, i + 2})
			i++
		}
	}
	if len(groups) > 0 {
		return groups[:len(groups)-1]
	}
	return nil
}

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
