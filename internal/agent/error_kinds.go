package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/adk"

	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/eventch"
)

// 在 init 阶段把 eino 的哨兵错误登记到失败种类上。
//
// 这样 pkg/errors 不必依赖 eino，而「迭代超限」又有了**结构化**判据：
// 之前它只能靠文本匹配（"exceeds"），结果被上下文超长的关键词表抢先命中，
// 一次迭代超限就把模型有效窗口从 64000 砍到 8192。
//
// ⚠️ 新增「eino 有哨兵错误」的种类时登记在这里，不要在 service / controller 里
// 另写一份文本匹配。
func init() {
	apperrors.RegisterKindSentinel(apperrors.KindMaxIterations, adk.ErrExceedMaxIterations)
}

func isToolChoiceUnsupportedError(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	keywords := []string{
		"tool choice",
		"tool_choice",
		"enable-auto-tool-choice",
		"tool-call-parser",
		"tool_calls",
		"function call",
		"function_call",
		"not_supported",
		"not support",
		"unsupported tool",
	}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// emitPlanError 上报深度编排失败。
// 只报种类，不报文案 —— 此前 Detail 直接放 err.Error()，把原始英文错泄漏给用户；
// 面向用户的措辞统一由 service 层按种类 × 模式给出（见 internal/service/chat_error_messages.go）。
func (e *Engine) emitPlanError(ctx context.Context, out chan<- Event, err error) {
	kind := apperrors.Classify(err)
	if isToolChoiceUnsupportedError(err.Error()) {
		kind = apperrors.KindToolUnsupported
	} else if kind == apperrors.KindUnknown {
		kind = apperrors.KindAgentRunFailed
	}
	eventch.Send(ctx, out, Event{Type: EventError, Kind: kind, Error: err.Error(), Status: "error", Done: true})
}
