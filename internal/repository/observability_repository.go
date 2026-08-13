package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/observability"
	"solvify-agent/pkg/logger"
)

type observabilityRepository struct {
	db *gorm.DB
}

// NewObservabilityRepository 创建可观测性仓库
func NewObservabilityRepository(db *gorm.DB) ObservabilityRepo {
	return &observabilityRepository{db: db}
}

// CreateFeedback 创建消息反馈记录
func (r *observabilityRepository) CreateFeedback(ctx context.Context, fb *entity.MessageFeedback) error {
	if fb.ID == "" {
		fb.ID = uuid.New().String()
	}
	if fb.CreatedAt.IsZero() {
		fb.CreatedAt = time.Now()
	}
	return r.db.WithContext(ctx).Create(fb).Error
}

// ListByMessage 按消息 ID 和用户 ID 查询反馈列表
func (r *observabilityRepository) ListByMessage(ctx context.Context, messageID, userID string) ([]entity.MessageFeedback, error) {
	var rows []entity.MessageFeedback
	err := r.db.WithContext(ctx).
		Where("message_id = ? AND user_id = ?", messageID, userID).
		Order("created_at DESC").
		Find(&rows).Error
	return rows, err
}

// ListByUser 分页查询指定用户的反馈列表
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

// CreateChatTrace 创建对话追踪记录
func (r *observabilityRepository) CreateChatTrace(ctx context.Context, trace *entity.ChatTrace) error {
	if trace.ID == "" {
		trace.ID = uuid.New().String()
	}
	if trace.CreatedAt.IsZero() {
		trace.CreatedAt = time.Now()
	}
	return r.db.WithContext(ctx).Create(trace).Error
}

// FindByID 根据 ID 查询对话追踪记录
func (r *observabilityRepository) FindByID(ctx context.Context, id string) (*entity.ChatTrace, error) {
	var t entity.ChatTrace
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListBySession 按会话 ID 分页查询对话追踪记录
func (r *observabilityRepository) ListBySession(ctx context.Context, sessionID, userID string, offset, limit int) ([]entity.ChatTrace, int64, error) {
	var total int64
	q := r.db.WithContext(ctx).Model(&entity.ChatTrace{}).
		Where("session_id = ? AND user_id = ?", sessionID, userID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []entity.ChatTrace
	err := q.Select("id, request_id, user_id, session_id, sample_rate, sampled, duration_ms, status, error, attrs, created_at").
		Order("created_at DESC").
		Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

// ListAll 分页查询全部对话追踪记录，支持按会话 ID 和状态过滤
func (r *observabilityRepository) ListAll(ctx context.Context, sessionID string, status string, offset, limit int) ([]entity.ChatTrace, int64, error) {
	var total int64
	q := r.db.WithContext(ctx).Model(&entity.ChatTrace{})
	if sessionID != "" {
		q = q.Where("session_id = ?", sessionID)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []entity.ChatTrace
	err := q.Select("id, request_id, user_id, session_id, sample_rate, sampled, duration_ms, status, error, attrs, created_at").
		Order("created_at DESC").
		Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

// DeleteOlderThan 删除早于指定时间的对话追踪记录
func (r *observabilityRepository) DeleteOlderThan(ctx context.Context, before time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("created_at < ?", before).Delete(&entity.ChatTrace{})
	return res.RowsAffected, res.Error
}

// CreateAgentTask 创建 Agent 任务记录
func (r *observabilityRepository) CreateAgentTask(ctx context.Context, task *entity.AgentTask) error {
	if task.ID == "" {
		task.ID = uuid.New().String()
	}
	return r.db.WithContext(ctx).Create(task).Error
}

// AppendStep 追加 Agent 任务步骤记录
func (r *observabilityRepository) AppendStep(ctx context.Context, step *entity.AgentTaskStep) error {
	if step.ID == "" {
		step.ID = uuid.New().String()
	}
	return r.db.WithContext(ctx).Create(step).Error
}

// MarkEnded 标记 Agent 任务结束，写入状态、token 用量和费用等信息
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

// FindByTraceID 根据追踪 ID 查询 Agent 任务及其步骤列表
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

// WriteTraces 批量写入对话追踪记录（upsert 方式）
func (r *observabilityRepository) WriteTraces(ctx context.Context, traces []*observability.Trace) error {
	if len(traces) == 0 {
		return nil
	}
	var firstErr error
	for _, t := range traces {
		if t == nil || t.Root == nil {
			continue
		}
		// 写库前清理内部 attrs，避免 __chat_intermediate_root 等内部标记进入 span_tree
		stripInternalSpanAttrs(t.Root)
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
			CreatedAt:  time.Now(),
		}
		// 用 upsert 代替 Save：Save 在主键有值时只走 UPDATE，行不存在时静默失败（rows:0）
		if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(row).Error; err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// WriteFeedbacks 批量写入消息反馈记录
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

// WriteAgentSteps 批量写入 Agent 步骤记录
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

// Write 根据 SinkRecord 类型分发写入对应的可观测性数据
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

// Shutdown 关闭仓库，释放资源（当前无操作）
func (r *observabilityRepository) Shutdown(_ context.Context) error { return nil }

// AttrsFromRoot 从根 span 提取 attrs 并汇总子 span 计数
func AttrsFromRoot(root *observability.Span) map[string]any {
	if root == nil {
		return nil
	}
	m := map[string]any{}
	if root.Attrs != nil {
		for k, v := range root.Attrs {
			// 跳过内部标记（__chat_intermediate_root 等），不落到 DB attrs JSON
			if strings.HasPrefix(k, "__") {
				continue
			}
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

// stripInternalSpanAttrs 递归清理 span 树中 __ 开头的内部 attrs（写 span_tree JSON 前调用）。
//
// 并发安全说明：
//   - 此函数可能在 FlushTrace 的后台 goroutine 执行时，与主请求 goroutine 里尚未结束的
//     SetSpanAttrs 并发读写同一份 span.Attrs。Go 的原生 map 不支持并发读写，任何一边的
//     delete/写都会触发 fatal error: concurrent map iteration and map write。
//   - 修复方式：不原地 delete，而是先拷贝一份 attrs，在副本上过滤，再赋回 s.Attrs。
//     这样迭代读的是独立副本，就算主 goroutine 同时在写，最坏情况是 strip 拿到的是
//     旧快照，不会 crash——FlushTrace 完后 span 对象就丢了，不影响业务。
func stripInternalSpanAttrs(s *observability.Span) {
	if s == nil {
		return
	}
	if len(s.Attrs) > 0 {
		clean := make(observability.Attrs, len(s.Attrs))
		for k, v := range s.Attrs { // 迭代期间无任何 delete/写，只是读
			if !strings.HasPrefix(k, "__") {
				clean[k] = v
			}
		}
		s.Attrs = clean
	}
	for i := range s.Children {
		stripInternalSpanAttrs(s.Children[i])
	}
	for i := range s.Events {
		if s.Events[i] == nil || len(s.Events[i].Attrs) == 0 {
			continue
		}
		clean := make(observability.Attrs, len(s.Events[i].Attrs))
		for k, v := range s.Events[i].Attrs {
			if !strings.HasPrefix(k, "__") {
				clean[k] = v
			}
		}
		s.Events[i].Attrs = clean
	}
}
