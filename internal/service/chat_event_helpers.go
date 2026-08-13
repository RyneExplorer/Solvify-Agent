package service

import (
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/pkg/logger"
)

// sendErrorEvent 发送友好的错误事件
// 匹配顺序：先查 err.Error()（包含底层错误详情如 503/429/timeout），再查 rawError（自定义描述）
func sendErrorEvent(eventCh chan<- dto.StreamEvent, err error, rawError string) {
	friendly := getFriendlyError(err, rawError)

	logger.Errorf("错误事件: title=%s, raw=%s, err=%v", friendly.Title, rawError, err)

	eventCh <- dto.StreamEvent{
		Type:      "error",
		Title:     friendly.Title,
		Detail:    friendly.Detail,
		Error:     rawError,
		Retryable: friendly.Retryable,
		Done:      true,
	}
}

// sendWarningEvent 发送警告事件
func sendWarningEvent(eventCh chan<- dto.StreamEvent, title, detail string) {
	logger.Warnf("警告事件: title=%s, detail=%s", title, detail)

	eventCh <- dto.StreamEvent{
		Type:   "warning",
		Title:  title,
		Detail: detail,
	}
}

// sendProgressEvent 发送进度事件
func sendProgressEvent(eventCh chan<- dto.StreamEvent, content string) {
	eventCh <- dto.StreamEvent{
		Type:    "progress",
		Content: content,
	}
}
