package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"gorm.io/gorm"
	"solvify-agent/internal/model/entity"
	"time"
)

type contextArtifactRepository struct{ db *gorm.DB }

func NewContextArtifactRepository(db *gorm.DB) ContextArtifactRepo {
	return &contextArtifactRepository{db}
}

func (r *contextArtifactRepository) SaveImmutable(ctx context.Context, a *entity.ContextArtifact, limits ArtifactLimits) error {
	if a == nil {
		return ErrArtifactUnavailable
	}
	sum := sha256.Sum256([]byte(a.Content))
	if a.Version != 1 || a.Bytes != int64(len(a.Content)) || a.Hash != hex.EncodeToString(sum[:]) || a.UserID == "" || a.SessionID == "" || a.RunID == "" || !a.ExpiredAt.After(a.CreatedAt) {
		return ErrArtifactUnavailable
	}
	if limits.ArtifactBytes <= 0 || limits.RunBytes < limits.ArtifactBytes || limits.UserBytes < limits.RunBytes || a.Bytes > limits.ArtifactBytes {
		return ErrArtifactQuota
	}
	return dbFor(ctx, r.db).Transaction(func(tx *gorm.DB) error {
		// 同一用户串行配额检查和写入，跨进程也不能超卖。
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "context-artifact:"+a.UserID).Error; err != nil {
			return err
		}
		var owned int64
		if err := tx.Table("chat_sessions").Where("id = ? AND user_id = ?", a.SessionID, a.UserID).Count(&owned).Error; err != nil {
			return err
		}
		if owned != 1 {
			return ErrArtifactUnavailable
		}
		var existing entity.ContextArtifact
		err := tx.Where("id = ?", a.ID).Take(&existing).Error
		if err == nil {
			if existing.UserID == a.UserID && existing.SessionID == a.SessionID && existing.RunID == a.RunID && existing.AgentID == a.AgentID && existing.TaskID == a.TaskID && existing.Hash == a.Hash {
				return nil
			}
			return ErrArtifactUnavailable
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var usage struct{ UserBytes, RunBytes int64 }
		if err = tx.Raw("SELECT COALESCE(SUM(bytes),0) AS user_bytes, COALESCE(SUM(CASE WHEN run_id = ? THEN bytes ELSE 0 END),0) AS run_bytes FROM context_artifacts WHERE user_id = ?", a.RunID, a.UserID).Scan(&usage).Error; err != nil {
			return err
		}
		if usage.UserBytes > limits.UserBytes-a.Bytes || usage.RunBytes > limits.RunBytes-a.Bytes {
			return ErrArtifactQuota
		}
		return tx.Create(a).Error
	})
}

func (r *contextArtifactRepository) FindScoped(ctx context.Context, s ArtifactScope, id string) (*entity.ContextArtifact, error) {
	var a entity.ContextArtifact
	err := dbFor(ctx, r.db).Table("context_artifacts AS a").Select("a.*").Joins("JOIN chat_sessions s ON s.id = a.session_id AND s.user_id::text = a.user_id").Where("a.id = ? AND a.user_id = ? AND a.session_id = ? AND a.run_id = ? AND a.agent_id = ? AND a.task_id = ?", id, s.UserID, s.SessionID, s.RunID, s.AgentID, s.TaskID).Take(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrArtifactUnavailable
	}
	if err != nil {
		return nil, err
	}
	if !a.ExpiredAt.After(time.Now()) {
		var active int64
		if err = dbFor(ctx, r.db).Table("agent_checkpoints").Where("session_id = ? AND expired_at > ?", s.SessionID, time.Now()).Count(&active).Error; err != nil {
			return nil, err
		}
		if active == 0 {
			return nil, ErrArtifactUnavailable
		}
	}
	return &a, nil
}

func (r *contextArtifactRepository) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	result := dbFor(ctx, r.db).Exec("DELETE FROM context_artifacts a WHERE a.expired_at <= ? AND NOT EXISTS (SELECT 1 FROM agent_checkpoints c WHERE c.session_id = a.session_id AND c.expired_at > ?)", now, now)
	return result.RowsAffected, result.Error
}
