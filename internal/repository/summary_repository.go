package repository

import (
	"context"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

// summaryRepository 提供会话摘要数据访问实现
type summaryRepository struct {
	db *gorm.DB
}

// NewSummaryRepository 创建摘要仓库
func NewSummaryRepository(db *gorm.DB) SummaryRepo {
	return &summaryRepository{db: db}
}

// GetBySessionID 获取会话摘要
func (r *summaryRepository) GetBySessionID(ctx context.Context, sessionID string) (*entity.ChatSummary, error) {
	var summary entity.ChatSummary
	err := dbFor(ctx, r.db).
		Where("session_id = ?", sessionID).
		First(&summary).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

// Upsert 更新或创建摘要。
//
// ⚠️ 覆盖是**只前进**的（monotonic），不是无条件覆盖。原因是并发：
// SummarizeSession 由 refreshContextAsync 在每次答完后异步触发，同一会话的两个并发
// 请求会同时「读 existing → 各自算区间 → 写回」。无条件覆盖时后写的赢，前一个算好的
// 区间被整段丢弃 —— 现象是「摘要越聊越短 / 覆盖范围倒退」。
//
// 这里用 GREATEST(covered_count, 新值) 让 covered_count 只增不减，
// 并在 SQL 里对 summary / last_message_id 也取「较新」的那一份：
// 更新条件是 covered_count 真的变大了（EXCLUDED.covered_count > 当前值），
// 于是两个并发里覆盖范围大的那个一定赢，与写入顺序无关。
//
// ⚠️ 不在进程内加锁：多实例部署时进程内锁挡不住另一个实例的并发，
// 而这里的判据放在数据库侧，实例数多少都一样成立。
func (r *summaryRepository) Upsert(ctx context.Context, summary *entity.ChatSummary) error {
	return dbFor(ctx, r.db).Exec(`
		INSERT INTO chat_summaries (id, session_id, summary, covered_count, last_message_id, created_at, updated_at)
		VALUES (gen_random_uuid(), ?, ?, ?, ?, now(), now())
		ON CONFLICT (session_id) DO UPDATE
		   SET summary         = EXCLUDED.summary,
		       covered_count   = GREATEST(chat_summaries.covered_count, EXCLUDED.covered_count),
		       last_message_id = EXCLUDED.last_message_id,
		       updated_at      = now()
		 WHERE EXCLUDED.covered_count > chat_summaries.covered_count
	`, summary.SessionID, summary.Summary, summary.CoveredCount, summary.LastMessageID).Error
}
