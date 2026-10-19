package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"solvify-agent/internal/model/entity"
)

type messageEmbeddingRepository struct{ db *gorm.DB }

func NewMessageEmbeddingRepository(db *gorm.DB) MessageEmbeddingRepository {
	return &messageEmbeddingRepository{db: db}
}

func (r *messageEmbeddingRepository) Claim(ctx context.Context, maxAttempts int, lease time.Duration) (*ClaimedMessageEmbedding, error) {
	db := dbFor(ctx, r.db)
	// 最后一次尝试期间进程崩溃，租约过期后明确终止，避免永远停留running。
	if err := db.Exec(`UPDATE chat_messages SET embedding_status='failed', embedding_error='attempts_exhausted',
 embedding_lease_until=NULL,embedding_lease_token=NULL
 WHERE embedding_attempts>=? AND (embedding_status='queued' OR (embedding_status='running' AND embedding_lease_until<=now()))`, maxAttempts).Error; err != nil {
		return nil, err
	}
	var jobs []ClaimedMessageEmbedding
	err := db.Raw(`WITH candidate AS (
 SELECT id FROM chat_messages WHERE embedding IS NULL AND embedding_attempts<? AND
 ((embedding_status='queued' AND embedding_available_at<=now()) OR (embedding_status='running' AND embedding_lease_until<=now()))
 ORDER BY embedding_available_at,id LIMIT 1 FOR UPDATE SKIP LOCKED
 ) UPDATE chat_messages m SET embedding_status='running',embedding_attempts=m.embedding_attempts+1,
 embedding_lease_token=?::uuid,embedding_lease_until=now()+(? * interval '1 millisecond'),embedding_error=''
 FROM candidate c WHERE m.id=c.id RETURNING m.id,m.content,m.embedding_attempts,m.embedding_lease_token`, maxAttempts, uuid.NewString(), lease.Milliseconds()).Scan(&jobs).Error
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, nil
	}
	return &jobs[0], nil
}

func (r *messageEmbeddingRepository) Complete(ctx context.Context, job ClaimedMessageEmbedding, vector entity.FloatVector) (bool, error) {
	result := dbFor(ctx, r.db).Exec(`UPDATE chat_messages SET embedding=?,embedding_status='succeeded',embedding_error='',
 embedding_lease_until=NULL,embedding_lease_token=NULL WHERE id=? AND embedding_status='running'
 AND embedding_lease_token=?::uuid AND embedding_lease_until>now() AND content=? AND embedding IS NULL`, vector, job.ID, job.EmbeddingLeaseToken, job.Content)
	return result.RowsAffected == 1, result.Error
}

func (r *messageEmbeddingRepository) Fail(ctx context.Context, job ClaimedMessageEmbedding, retry bool, delay time.Duration, code string) (bool, error) {
	status := "failed"
	if retry {
		status = "queued"
	}
	result := dbFor(ctx, r.db).Exec(`UPDATE chat_messages SET embedding_status=?,embedding_error=?,
 embedding_available_at=now()+(? * interval '1 millisecond'),embedding_lease_until=NULL,embedding_lease_token=NULL
 WHERE id=? AND embedding_status='running' AND embedding_lease_token=?::uuid AND embedding_lease_until>now()`, status, code, delay.Milliseconds(), job.ID, job.EmbeddingLeaseToken)
	return result.RowsAffected == 1, result.Error
}

func (r *messageEmbeddingRepository) Release(ctx context.Context, job ClaimedMessageEmbedding) (bool, error) {
	// 正常停机取消尚未提交的只读请求，不消耗一次失败额度；崩溃由租约回收并消耗额度。
	result := dbFor(ctx, r.db).Exec(`UPDATE chat_messages SET embedding_status='queued',embedding_attempts=embedding_attempts-1,
 embedding_available_at=now(),embedding_error='',embedding_lease_until=NULL,embedding_lease_token=NULL
 WHERE id=? AND embedding_status='running' AND embedding_lease_token=?::uuid AND embedding_lease_until>now()`, job.ID, job.EmbeddingLeaseToken)
	return result.RowsAffected == 1, result.Error
}

func (r *messageEmbeddingRepository) QueueMissing(ctx context.Context, limit int, retryFailed bool) (int64, error) {
	result := dbFor(ctx, r.db).Exec(`WITH candidate AS (
 SELECT id FROM chat_messages WHERE embedding IS NULL AND btrim(content)<>''
 AND (embedding_status='none' OR (? AND embedding_status='failed'))
 ORDER BY id LIMIT ? FOR UPDATE SKIP LOCKED
 ) UPDATE chat_messages m SET embedding_status='queued',embedding_attempts=0,embedding_available_at=now(),embedding_error=''
 FROM candidate c WHERE m.id=c.id`, retryFailed, limit)
	return result.RowsAffected, result.Error
}
