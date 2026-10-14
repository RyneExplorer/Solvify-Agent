package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/config"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/tokenutil"
)

type budgetIdentityKey struct{}
type budgetIdentity struct {
	modelID  string
	declared int
}

// WithBudgetIdentity 绑定业务模型配置，避免最终供应商保护遗漏运行时窗口修正。
func WithBudgetIdentity(ctx context.Context, modelID string, declared int) context.Context {
	return context.WithValue(ctx, budgetIdentityKey{}, budgetIdentity{modelID, declared})
}

var ErrContextBudget = errors.New("上下文预算不足")
var ErrContextConfig = errors.New("上下文配置无效")

func init() {
	apperrors.RegisterKindSentinel(apperrors.KindContextBudget, ErrContextBudget)
	apperrors.RegisterKindSentinel(apperrors.KindModelConfig, ErrContextConfig)
}

// ContextBudget 只统计实际发送内容；软水位不改变硬上限。
type ContextBudget struct{ Limit, Output, Margin, Hard, Soft, Target int }

func ResolveContextBudget(modelID, modelName string, declared, output int, quick bool, policy config.ContextPolicy) (ContextBudget, error) {
	if err := policy.Validate(); err != nil {
		return ContextBudget{}, fmt.Errorf("%w: %v", ErrContextConfig, err)
	}
	limit := declared
	if limit <= 0 || output <= 0 {
		return ContextBudget{}, fmt.Errorf("%w: 窗口=%d 输出=%d", ErrContextConfig, limit, output)
	}
	margin := max(int(float64(limit)*policy.MarginRatio), policy.MarginFloor)
	hard := limit - output - margin
	if hard <= 0 {
		return ContextBudget{}, fmt.Errorf("%w: 窗口=%d 输出=%d 裕量=%d", ErrContextBudget, limit, output, margin)
	}
	soft, target := policy.DeepSoftRatio, policy.DeepTargetRatio
	if quick {
		soft, target = policy.QuickSoftRatio, policy.QuickTargetRatio
	}
	return ContextBudget{limit, output, margin, hard, int(float64(hard) * soft), int(float64(hard) * target)}, nil
}

// WithInputLimit 将模型独立输入限制同步到硬预算与软水位。
func (b ContextBudget) WithInputLimit(limit int) ContextBudget {
	if limit > 0 && limit < b.Hard {
		b.Soft = int(float64(b.Soft) * float64(limit) / float64(b.Hard))
		b.Target = int(float64(b.Target) * float64(limit) / float64(b.Hard))
		b.Hard = limit
	}
	return b
}

func (b ContextBudget) Check(used int) error {
	if used > b.Hard {
		return fmt.Errorf("%w: 输入=%d 上限=%d 输出=%d", ErrContextBudget, used, b.Hard, b.Output)
	}
	return nil
}

func MessageTokens(m *schema.Message, name string) int {
	if m == nil {
		return 0
	}
	total := 4 + tokenutil.CountTokens(m.Content, name) + tokenutil.CountTokens(m.Name, name) + tokenutil.CountTokens(m.ToolCallID, name) + tokenutil.CountTokens(m.ToolName, name)
	for _, call := range m.ToolCalls {
		total += tokenutil.CountTokens(call.ID, name) + tokenutil.CountTokens(call.Function.Name, name) + tokenutil.CountTokens(call.Function.Arguments, name)
	}
	// 当前接口只支持文本；多模态内容不能以 URL 字符长度假装图像 token。
	return total
}
func MessagesTokens(messages []*schema.Message, name string) int {
	total := 0
	for _, m := range messages {
		total += MessageTokens(m, name)
	}
	return total
}
func ToolsTokens(tools []*schema.ToolInfo, name string) int {
	total := 0
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		raw, err := json.Marshal(tool)
		if err != nil {
			total += tokenutil.CountTokens(tool.Name+"\n"+tool.Desc, name)
		} else {
			total += tokenutil.CountTokens(string(raw), name)
		}
	}
	return total
}
