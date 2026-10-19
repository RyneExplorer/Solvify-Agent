package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	apperrors "solvify-agent/pkg/errors"
)

// Eino在此钩子之后才路由到ToolsNode；异常工具请求不能执行。
type completionGuard struct {
	adk.BaseChatModelAgentMiddleware
}

func (*completionGuard) AfterModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if state == nil || len(state.Messages) == 0 {
		return ctx, state, errModelOutputInvalid
	}
	return ctx, state, validateModelCompletion(state.Messages[len(state.Messages)-1])
}

var (
	errModelOutputLimit    = errors.New("模型达到输出上限，内容尚未完成")
	errModelOutputFiltered = errors.New("模型输出被过滤，内容尚未完成")
	errModelOutputInvalid  = errors.New("模型未返回有效的结束状态")
)

func init() {
	apperrors.RegisterKindSentinel(apperrors.KindOutputLimit, errModelOutputLimit)
	apperrors.RegisterKindSentinel(apperrors.KindOutputFiltered, errModelOutputFiltered)
	apperrors.RegisterKindSentinel(apperrors.KindModelOutputInvalid, errModelOutputInvalid)
}

// 结束类型和实际消息形态必须一致；EOF 或非空正文不能证明正常完成。
func validateModelCompletion(msg *schema.Message) error {
	if msg == nil || msg.ResponseMeta == nil {
		return errModelOutputInvalid
	}
	reason := msg.ResponseMeta.FinishReason
	switch reason {
	case "length":
		return errModelOutputLimit
	case "content_filter":
		return errModelOutputFiltered
	case "stop":
		if len(msg.ToolCalls) == 0 {
			return nil
		}
	case "tool_calls":
		if len(msg.ToolCalls) > 0 {
			return nil
		}
	}
	return fmt.Errorf("模型结束类型与输出不匹配（%q）: %w", reason, errModelOutputInvalid)
}
