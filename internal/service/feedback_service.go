package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"solvify-agent/internal/model/entity"
)

// ─── 消息反馈 ────────────────────────────────────────────────────────────────

// SubmitFeedback 提交消息反馈
func (s *chatService) SubmitFeedback(ctx context.Context, userID, messageID string, req FeedbackRequest) error {
	if req.Rating != 1 && req.Rating != -1 {
		return fmt.Errorf("rating 必须为 1 或 -1")
	}
	if messageID == "" || userID == "" {
		return fmt.Errorf("message_id / user_id 不能为空")
	}
	msg, err := s.messageRepo.FindByID(ctx, messageID)
	if err != nil {
		return fmt.Errorf("查询消息失败: %w", err)
	}
	if msg == nil {
		return fmt.Errorf("消息不存在或无权限")
	}
	if msg.SessionID != "" {
		if vErr := s.validateSession(ctx, userID, msg.SessionID); vErr != nil {
			return fmt.Errorf("消息不存在或无权限")
		}
	}
	primaryTag := ""
	if len(req.Reasons) > 0 {
		primaryTag = req.Reasons[0]
	}
	fb := &entity.MessageFeedback{
		ID:        uuid.New().String(),
		MessageID: messageID,
		UserID:    userID,
		SessionID: msg.SessionID,
		Rating:    req.Rating,
		ReasonTag: primaryTag,
		Comment:   req.Comment,
		IsQuick:   req.IsQuick,
	}
	fb.SetReasons(req.Reasons)
	if s.feedbackRepo != nil {
		if e := s.feedbackRepo.CreateFeedback(ctx, fb); e != nil {
			return fmt.Errorf("保存反馈失败: %w", e)
		}
	}
	return nil
}

// ListFeedbacks 分页查询用户反馈列表
func (s *chatService) ListFeedbacks(ctx context.Context, userID string, offset, limit int) (FeedbackListResponse, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	if s.feedbackRepo == nil {
		return FeedbackListResponse{Total: 0, Feedbacks: []any{}}, nil
	}
	list, total, err := s.feedbackRepo.ListByUser(ctx, userID, offset, limit)
	if err != nil {
		return FeedbackListResponse{}, err
	}
	type out struct {
		entity.MessageFeedback
		Reasons []string `json:"reasons"`
	}
	items := make([]any, 0, len(list))
	for _, f := range list {
		items = append(items, out{MessageFeedback: f, Reasons: f.Reasons()})
	}
	return FeedbackListResponse{Total: total, Feedbacks: items}, nil
}
