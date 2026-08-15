package service

import (
	"context"

	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
)

const (
	chatModeQuick = "quick"
	chatModeDeep  = "smart-reasoning"
)

// chatMode 对话模式处理器接口。
// 新增对话模式只需：1) 实现 chatMode 接口；2) 在 modeRegistry 注册一行。
type chatMode interface {
	Name() string
	Handle(ctx context.Context, s *chatService, userID, sessionID, userMsgID string,
		req requestdto.SendMessageRequest, eventCh chan<- dto.StreamEvent)
}

// modeRegistry 对话模式注册表：搜索模式字符串 → 处理器实例。
// 默认值 "" 和未知值都回退到快速检索模式。
var modeRegistry = map[string]chatMode{
	chatModeQuick: &quickModeHandler{},
	"":            &quickModeHandler{},
	chatModeDeep:  &deepModeHandler{},
}

// getModeHandler 根据 searchMode 查找处理器，找不到回退到快速模式。
func getModeHandler(searchMode string) chatMode {
	if h, ok := modeRegistry[searchMode]; ok {
		return h
	}
	return modeRegistry[chatModeQuick]
}

// ─── 快速检索模式 ──────────────────────────────────────────

type quickModeHandler struct{}

func (h *quickModeHandler) Name() string { return chatModeQuick }

func (h *quickModeHandler) Handle(ctx context.Context, s *chatService, userID, sessionID, userMsgID string,
	req requestdto.SendMessageRequest, eventCh chan<- dto.StreamEvent) {
	s.processMessageGraphQuick(ctx, userID, sessionID, userMsgID, req, eventCh)
}

// ─── 深度思考模式 ──────────────────────────────────────────

type deepModeHandler struct{}

func (h *deepModeHandler) Name() string { return chatModeDeep }

func (h *deepModeHandler) Handle(ctx context.Context, s *chatService, userID, sessionID, userMsgID string,
	req requestdto.SendMessageRequest, eventCh chan<- dto.StreamEvent) {
	s.processDeepMode(ctx, userID, sessionID, userMsgID, req, eventCh)
}
