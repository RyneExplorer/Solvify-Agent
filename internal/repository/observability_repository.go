package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/observability"
	"solvify-agent/pkg/logger"
)

type observabilityRepository struct {
	db *gorm.DB
}

func NewObservabilityRepository(db *gorm.DB) ObservabilityRepo {
	return &observabilityRepository{db: db}
}

func (r *observabilityRepository) CreateFeedback(ctx context.Context, fb *entity.MessageFeedback) error {
	if fb.ID == "" {
		fb.ID = uuid.New().String()
	}
	if fb.CreatedAt.IsZero() {
		fb.CreatedAt = time.Now()
	}
	return r.db.WithContext(ctx).Create(fb).Error
}

func (r *observabilityRepository) ListByMessage(ctx context.Context, messageID, userID string) ([]entity.MessageFeedback, error) {
	var rows []entity.MessageFeedback
	err := r.db.WithContext(ctx).
		Where("message_id = ? AND user_id = ?", messageID, userID).
		Order("created_at DESC").
		Find(&rows).Error
	return rows, err
}

func (r *observabilityRepository) ListByUser(ctx context.Context, userID string, offset, limit int) ([]entity.MessageFeedback, int64, error) {
	var total int64
	q := r.db.WithContext(ctx).Model(&entity.MessageFeedback{}).Where("user_id = ?", userID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []entity.MessageFeedback
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

func (r *observabilityRepository) CreateChatTrace(ctx context.Context, trace *entity.ChatTrace) error {
	if trace.ID == "" {
		trace.ID = uuid.New().String()
	}
	if trace.CreatedAt.IsZero() {
		trace.CreatedAt = time.Now()
	}
	return r.db.WithContext(ctx).Create(trace).Error
}

func (r *observabilityRepository) FindByID(ctx context.Context, id string) (*entity.ChatTrace, error) {
	var t entity.ChatTrace
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *observabilityRepository) ListBySession(ctx context.Context, sessionID, userID string, offset, limit int) ([]entity.ChatTrace, int64, error) {
	var total int64
	q := r.db.WithContext(ctx).Model(&entity.ChatTrace{}).
		Where("session_id = ? AND user_id = ?", sessionID, userID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []entity.ChatTrace
	err := q.Select("id, request_id, user_id, session_id, duration_ms, status, error, attrs, created_at").
		Order("created_at DESC").
		Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

func (r *observabilityRepository) DeleteOlderThan(ctx context.Context, before time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("created_at < ?", before).Delete(&entity.ChatTrace{})
	return res.RowsAffected, res.Error
}

func (r *observabilityRepository) CreateAgentTask(ctx context.Context, task *entity.AgentTask) error {
	if task.ID == "" {
		task.ID = uuid.New().String()
	}
	return r.db.WithContext(ctx).Create(task).Error
}

func (r *observabilityRepository) AppendStep(ctx context.Context, step *entity.AgentTaskStep) error {
	if step.ID == "" {
		step.ID = uuid.New().String()
	}
	return r.db.WithContext(ctx).Create(step).Error
}

func (r *observabilityRepository) MarkEnded(ctx context.Context, taskID string, status, abortReason, errorSummary string, tokensPrompt, tokensCompletion int, cost float64, rating *int) error {
	now := time.Now()
	updates := map[string]any{
		"ended_at":          &now,
		"status":            status,
		"tokens_prompt":     tokensPrompt,
		"tokens_completion": tokensCompletion,
		"total_cost":        cost,
	}
	if abortReason != "" {
		updates["abort_reason"] = abortReason
	}
	if errorSummary != "" {
		updates["error_summary"] = errorSummary
	}
	if rating != nil {
		updates["feedback_rating"] = *rating
	}
	return r.db.WithContext(ctx).Model(&entity.AgentTask{}).
		Where("id = ?", taskID).Updates(updates).Error
}

func (r *observabilityRepository) FindByTraceID(ctx context.Context, traceID string) (*entity.AgentTask, []entity.AgentTaskStep, error) {
	var task entity.AgentTask
	err := r.db.WithContext(ctx).Where("trace_id = ?", traceID).First(&task).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var steps []entity.AgentTaskStep
	if err := r.db.WithContext(ctx).Where("task_id = ?", task.ID).Order("step_index ASC").Find(&steps).Error; err != nil {
		return &task, nil, err
	}
	return &task, steps, nil
}

func (r *observabilityRepository) WriteTraces(ctx context.Context, traces []*observability.Trace) error {
	if len(traces) == 0 {
		return nil
	}
	var firstErr error
	for _, t := range traces {
		if t == nil || t.Root == nil {
			continue
		}
		spanTree, err := json.Marshal(t.Root)
		if err != nil {
			logger.Warnf("trace span_tree marshal 失败: %v", err)
			continue
		}
		status := string(t.Root.Status)
		duration := t.Root.DurationMs
		attrs := AttrsFromRoot(t.Root)
		attrsJSON, _ := json.Marshal(attrs)
		row := &entity.ChatTrace{
			ID:         t.ID,
			RequestID:  t.RequestID,
			UserID:     t.UserID,
			SessionID:  t.SessionID,
			SampleRate: t.SampleRate,
			Sampled:    t.Sampled,
			DurationMs: duration,
			Status:     status,
			Error:      t.Root.Error,
			Attrs:      datatypes.JSON(attrsJSON),
			SpanTree:   datatypes.JSON(spanTree),
		}
		if err := r.db.WithContext(ctx).Save(row).Error; err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (r *observabilityRepository) WriteFeedbacks(ctx context.Context, fs []*observability.Feedback) error {
	if len(fs) == 0 {
		return nil
	}
	var firstErr error
	for _, f := range fs {
		if f == nil {
			continue
		}
		row := &entity.MessageFeedback{
			MessageID: f.MessageID,
			UserID:    f.UserID,
			SessionID: f.SessionID,
			Rating:    f.Rating,
			ReasonTag: f.ReasonTag,
			Comment:   f.Comment,
			TraceID:   f.TraceID,
			CreatedAt: f.CreatedAt,
		}
		if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (r *observabilityRepository) WriteAgentSteps(ctx context.Context, steps []*observability.AgentStep) error {
	if len(steps) == 0 {
		return nil
	}
	var firstErr error
	for _, s := range steps {
		if s == nil {
			continue
		}
		row := &entity.AgentTaskStep{
			TaskID:            s.TaskID,
			StepIndex:         s.StepIndex,
			StartedAt:         s.StartedAt,
			ThinkingSummary:   s.ThinkingSummary,
			ToolName:          s.ToolName,
			ToolInputMasked:   s.ToolInputMasked,
			ToolResultSummary: s.ToolResultSummary,
			ToolStatus:        s.ToolStatus,
			ToolError:         s.ToolError,
			LatencyMs:         s.LatencyMs,
			TokensDelta:       s.TokensDelta,
		}
		endedAt := s.EndedAt
		if !endedAt.IsZero() {
			row.EndedAt = &endedAt
		}
		if err := r.db.WithContext(ctx).Create(row).Error; err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (r *observabilityRepository) Write(ctx context.Context, rec *observability.SinkRecord) error {
	if rec == nil {
		return nil
	}
	switch rec.Kind {
	case "trace":
		if rec.Trace != nil {
			return r.WriteTraces(ctx, []*observability.Trace{rec.Trace})
		}
	case "feedback":
		if rec.Feedback != nil {
			return r.WriteFeedbacks(ctx, []*observability.Feedback{rec.Feedback})
		}
	case "agent_step":
		if rec.AgentStep != nil {
			return r.WriteAgentSteps(ctx, []*observability.AgentStep{rec.AgentStep})
		}
	}
	return nil
}

func (r *observabilityRepository) Shutdown(_ context.Context) error { return nil }

func AttrsFromRoot(root *observability.Span) map[string]any {
	if root == nil {
		return nil
	}
	m := map[string]any{}
	if root.Attrs != nil {
		for k, v := range root.Attrs {
			m[k] = v
		}
	}
	if len(root.Children) > 0 {
		components := map[string]int64{}
		for _, c := range root.Children {
			if c == nil {
				continue
			}
			components[string(c.Component)] += 1
		}
		m["child_span_counts"] = components
	}
	return m
}
