package agent

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	openai "github.com/meguminnnnnnnnn/go-openai"

	"solvify-agent/pkg/logger"
)

func (engine *Engine) modelRetryConfig() *adk.ModelRetryConfig {
	if engine.cfg.ModelMaxRetries <= 0 {
		return nil
	}
	return &adk.ModelRetryConfig{
		MaxRetries: engine.cfg.ModelMaxRetries,
		ShouldRetry: func(ctx context.Context, retry *adk.RetryContext) *adk.RetryDecision {
			allowed := !modelRetryHasOutput(retry.OutputMessage) && retryableModelError(ctx, retry.Err)
			if allowed && retry.RetryAttempt <= engine.cfg.ModelMaxRetries {
				logger.Infof("模型调用临时失败，准备重试：次数=%d 上限=%d", retry.RetryAttempt, engine.cfg.ModelMaxRetries)
			}
			return &adk.RetryDecision{Retry: allowed}
		},
	}
}

func modelRetryHasOutput(message *schema.Message) bool {
	return message != nil && (message.Role != "" || message.Content != "" || message.ReasoningContent != "" ||
		message.Name != "" || message.ToolCallID != "" || message.ToolName != "" || message.ResponseMeta != nil ||
		len(message.ToolCalls) > 0 || len(message.MultiContent) > 0 || len(message.UserInputMultiContent) > 0 ||
		len(message.AssistantGenMultiContent) > 0 || len(message.Extra) > 0)
}

func retryableModelError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return retryableModelStatus(apiErr.HTTPStatusCode)
	}
	var requestErr *openai.RequestError
	if errors.As(err, &requestErr) {
		return retryableModelStatus(requestErr.HTTPStatusCode)
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		return true
	}
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED)
}

func retryableModelStatus(status int) bool {
	switch status {
	case 408, 429, 500, 502, 503, 504:
		return true
	default:
		return false
	}
}

func isModelRetryNotification(err error) bool {
	var retry *adk.WillRetryError
	return errors.As(err, &retry)
}
