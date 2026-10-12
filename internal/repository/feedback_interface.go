package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
)

// FeedbackRepo 是「消息点赞/点踩」这一产品能力的持久化接口。
//
// ⚠️ 它曾经挂在 ObservabilityRepo 里，与 chat_traces / agent_tasks 同居一个仓库。
// 那个组合是错的：反馈是**用户可见的功能**（提交、列表、按消息查），
// trace / task 是**运维台账**。两者生命周期、保留策略、删除权都不一样 ——
// 混在一个接口里，删台账就会连带把功能删掉（本次重构正是踩在这个坑口上）。
type FeedbackRepo interface {
	CreateFeedback(ctx context.Context, fb *entity.MessageFeedback) error
	ListByMessage(ctx context.Context, messageID, userID string) ([]entity.MessageFeedback, error)
	ListByUser(ctx context.Context, userID string, offset, limit int) ([]entity.MessageFeedback, int64, error)
}
