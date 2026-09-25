package service

import (
	"context"
	"errors"
	"strings"

	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/pkg/eventch"
	"solvify-agent/pkg/logger"
)

// sendErrorEvent 发送友好的错误事件。
// 匹配顺序：先查 err.Error()（包含底层错误详情如 503/429/timeout），再查 rawError（自定义描述）。
// ctx 已取消（客户端断连）时事件被丢弃、不阻塞，见 pkg/eventch。
func sendErrorEvent(ctx context.Context, eventCh chan<- dto.StreamEvent, err error, rawError string) {
	friendly := getFriendlyError(err, rawError)

	logger.Errorf("错误事件: title=%s, raw=%s, err=%v", friendly.Title, rawError, err)

	eventch.Send(ctx, eventCh, dto.StreamEvent{
		Type:      "error",
		Title:     friendly.Title,
		Detail:    friendly.Detail,
		Error:     rawError,
		Retryable: friendly.Retryable,
		Done:      true,
	})
}

// sendWarningEvent 发送警告事件（ctx 已取消时丢弃、不阻塞，见 pkg/eventch）
func sendWarningEvent(ctx context.Context, eventCh chan<- dto.StreamEvent, title, detail string) {
	logger.Warnf("警告事件: title=%s, detail=%s", title, detail)

	eventch.Send(ctx, eventCh, dto.StreamEvent{
		Type:   "warning",
		Title:  title,
		Detail: detail,
	})
}

// sendProgressEvent 发送进度事件（ctx 已取消时丢弃、不阻塞，见 pkg/eventch）
func sendProgressEvent(ctx context.Context, eventCh chan<- dto.StreamEvent, content string) {
	eventch.Send(ctx, eventCh, dto.StreamEvent{
		Type:    "progress",
		Content: content,
	})
}

// errEmptyAnswer 是「上游成功返回、但内容为空」的哨兵错误。
//
// 单独定义而非就地 errors.New：空回答要能被识别成一个明确的可重试失败，
// 便于测试断言（errors.Is），避免与"真·执行错误"混在一处判断。
var errEmptyAnswer = errors.New("模型未返回任何内容")

// rejectEmptyAnswer 是「上游返回空内容」的统一守卫，快速模式与深度模式共用。
//
// 两种模式此前各写各的，口径不一致，各自错一半：
//   - 快速模式：照发 done 并把空 assistant 消息落库 —— 用户看到空白气泡，
//     且空消息进入后续 history（部分厂商对空 content 直接 400），一次空回答污染整条会话；
//   - 深度模式：只做裸 return —— 不落库，但也不发任何终态事件，SSE 流就此断掉，
//     前端永远等不到 done/error，只能一直停在「正在生成」。
//
// 统一口径：发 error 终态事件（可重试）并让调用方立刻返回，绝不落库。
// 返回 true 表示本次就是空回答、已按可重试错误收尾，调用方应直接 return。
//
// mode 取 "quick" / "deep"，同时作为指标标签和日志前缀；
// detail 追加本次的上下文计数（如 retrievedDocs=3 / sources=2, steps=4），只用于日志。
func rejectEmptyAnswer(
	ctx context.Context,
	eventCh chan<- dto.StreamEvent,
	mode, sessionID, modelID, assistantMsgID string,
	fullContent, detail string,
) bool {
	// 全是空白的回答同样不可用：部分厂商会返回纯换行/空格充当"有内容"
	if strings.TrimSpace(fullContent) != "" {
		return false
	}

	logger.Warnf("[%s] 收到空回答，已拦截（不落库）: sessionID=%s, modelID=%s, assistantMsgID=%s, %s",
		mode, sessionID, modelID, assistantMsgID, detail)

	eventch.Send(ctx, eventCh, dto.StreamEvent{
		Type:      "error",
		Title:     "AI 未返回内容",
		Detail:    "模型本次没有返回任何内容，请重试或切换其他模型",
		Retryable: true,
		Done:      true,
	})
	return true
}
