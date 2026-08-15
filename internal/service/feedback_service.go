package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/observability"
)

// ─── 可观测：反馈 / Trace / Metrics 查询接口 ───────────────────────────────────

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
	var traceID string
	if raw := msg.Metadata; len(raw) > 0 {
		if meta := metadataAsMap(raw); meta != nil {
			if v, ok := meta["trace_id"].(string); ok {
				traceID = v
			}
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
		TraceID:   traceID,
	}
	fb.SetReasons(req.Reasons)
	if s.obsRepo != nil {
		if e := s.obsRepo.CreateFeedback(ctx, fb); e != nil {
			return fmt.Errorf("保存反馈失败: %w", e)
		}
	}
	if s.obs != nil {
		s.obs.Incr(ctx, "chat_feedback_total", map[string]string{
			"rating":      ratingLabel(req.Rating),
			"reason_tag":  reasonTagOrDefault(primaryTag),
			"has_comment": boolLabel(req.Comment != ""),
		}, 1)
	}
	if s.obs != nil {
		s.obs.RecordFeedback(&observability.Feedback{
			MessageID: fb.MessageID,
			UserID:    fb.UserID,
			SessionID: fb.SessionID,
			Rating:    fb.Rating,
			Reasons:   fb.Reasons(),
			Comment:   fb.Comment,
			TraceID:   fb.TraceID,
			CreatedAt: fb.CreatedAt,
		})
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
	if s.obsRepo == nil {
		return FeedbackListResponse{Total: 0, Feedbacks: []any{}}, nil
	}
	list, total, err := s.obsRepo.ListByUser(ctx, userID, offset, limit)
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

// buildTraceResponse 构建追踪详情响应
