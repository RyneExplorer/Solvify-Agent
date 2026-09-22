package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"solvify-agent/internal/model/entity"
)

type agentCheckpointRepository struct {
	db *gorm.DB
}

func NewAgentCheckpointRepository(db *gorm.DB) AgentCheckpointRepo {
	return &agentCheckpointRepository{db: db}
}

func (r *agentCheckpointRepository) Save(ctx context.Context, checkpointID, sessionID string, data []byte, expiredAt time.Time) error {
	cp := entity.AgentCheckpoint{
		ID:         checkpointID,
		SessionID:  sessionID,
		Checkpoint: data,
		ExpiredAt:  expiredAt,
	}
	return dbFor(ctx, r.db).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{"checkpoint", "session_id", "expired_at", "updated_at"}),
		}).
		Create(&cp).Error
}

func (r *agentCheckpointRepository) Find(ctx context.Context, checkpointID string) ([]byte, bool, error) {
	var cp entity.AgentCheckpoint
	err := dbFor(ctx, r.db).Where("id = ?", checkpointID).First(&cp).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return cp.Checkpoint, true, nil
}

func (r *agentCheckpointRepository) Delete(ctx context.Context, checkpointID string) error {
	return dbFor(ctx, r.db).Where("id = ?", checkpointID).Delete(&entity.AgentCheckpoint{}).Error
}

func (r *agentCheckpointRepository) DeleteBySessionID(ctx context.Context, sessionID string) error {
	return dbFor(ctx, r.db).Where("session_id = ?", sessionID).Delete(&entity.AgentCheckpoint{}).Error
}

func (r *agentCheckpointRepository) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	res := dbFor(ctx, r.db).
		Where("expired_at IS NOT NULL AND expired_at < ?", now).
		Delete(&entity.AgentCheckpoint{})
	return res.RowsAffected, res.Error
}
