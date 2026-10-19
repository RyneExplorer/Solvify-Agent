package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	einoOpenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	openai "github.com/meguminnnnnnnnn/go-openai"

	"solvify-agent/internal/llm"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/logger"
)

// 专业任务将额外模型调用记入同一预算；普通控制节点每次最多恢复一次。
type modelRecoveryBudgetKey struct{}

var errModelContextRejected = errors.New("模型端点拒绝上下文额度")

func init() {
	apperrors.RegisterKindSentinel(apperrors.KindContextLength, errModelContextRejected)
}

func recoverableContextError(err error) bool {
	if errors.Is(err, llm.ErrContextBudget) {
		return true
	}
	var api *openai.APIError
	var einoAPI *einoOpenai.APIError
	var status int
	var rawCode any
	var message string
	switch {
	case errors.As(err, &einoAPI):
		status, rawCode, message = einoAPI.HTTPStatusCode, einoAPI.Code, einoAPI.Message
	case errors.As(err, &api):
		status, rawCode, message = api.HTTPStatusCode, api.Code, api.Message
	default:
		return false
	}
	if status != 400 {
		return false
	}
	code, _ := rawCode.(string)
	return code == "context_length_exceeded" || code == "max_context_length_exceeded" ||
		strings.Contains(strings.ToLower(message), "maximum context length") ||
		strings.Contains(strings.ToLower(message), "prompt is too long")
}

// 保留结构化端点原因，避免只有code、没有特征文案时退化为未知错误。
func normalizeContextError(err error) error {
	if errors.Is(err, llm.ErrContextBudget) || errors.Is(err, errModelContextRejected) || !recoverableContextError(err) {
		return err
	}
	return fmt.Errorf("%w: %w", errModelContextRejected, err)
}

func (m *budgetModel) recovery(ctx context.Context, input []*schema.Message, opts []model.Option, cause error) (context.Context, []*schema.Message, []model.Option, error) {
	if ctx.Err() != nil {
		return ctx, nil, nil, ctx.Err()
	}
	output := m.guard.outputLimit
	if output == 0 {
		output = m.guard.policy.OutputLimit
	}
	if requested := model.GetCommonOptions(nil, opts...).MaxTokens; requested != nil {
		output = *requested
	}
	next := max(llm.MinimumAdaptiveOutput, output/2)
	if next >= output {
		return ctx, nil, nil, cause
	}
	options := append(append([]model.Option(nil), opts...), model.WithMaxTokens(next))
	ctx, messages, options, err := m.prepare(ctx, input, options)
	if err != nil {
		return ctx, nil, nil, err
	}
	if reserve, ok := ctx.Value(modelRecoveryBudgetKey{}).(func() error); ok {
		if err := reserve(); err != nil {
			return ctx, nil, nil, err
		}
	}
	logger.Infof("模型上下文超限，调整后重试一次：原输出=%d 本次输出=%d", output, next)
	return ctx, messages, options, nil
}

func (m *budgetModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	ctx, input, options, err := m.prepare(ctx, messages, opts)
	if err != nil {
		return nil, err
	}
	msg, err := m.inner.Generate(ctx, input, options...)
	err = normalizeContextError(err)
	// 仅恢复被拒绝且未产生任何输出的请求，不重跑业务工具。
	if err == nil || modelRetryHasOutput(msg) || !recoverableContextError(err) {
		return msg, err
	}
	ctx, input, options, err = m.recovery(ctx, input, options, err)
	if err != nil {
		return nil, err
	}
	msg, err = m.inner.Generate(ctx, input, options...)
	if err != nil {
		return msg, fmt.Errorf("上下文调整后调用失败: %w", normalizeContextError(err))
	}
	return msg, nil
}

func (m *budgetModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	ctx, input, options, err := m.prepare(ctx, messages, opts)
	if err != nil {
		return nil, err
	}
	stream, err := m.inner.Stream(ctx, input, options...)
	err = normalizeContextError(err)
	// 只在建立流失败时恢复；流已交给调用方后不再重试，避免正文重复。
	if err == nil || stream != nil || !recoverableContextError(err) {
		return stream, err
	}
	ctx, input, options, err = m.recovery(ctx, input, options, err)
	if err != nil {
		return nil, err
	}
	stream, err = m.inner.Stream(ctx, input, options...)
	if err != nil {
		return stream, fmt.Errorf("上下文调整后建立流失败: %w", normalizeContextError(err))
	}
	return stream, nil
}
