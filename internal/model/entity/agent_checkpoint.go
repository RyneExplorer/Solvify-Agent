package entity

import (
	"time"
)

// AgentCheckpoint 持久化 Agent Graph 的 checkpoint 原始字节。
// checkpoint_id 由 compose Graph 内部生成，和 session.pending_checkpoint.checkpoint_id 对应。
type AgentCheckpoint struct {
	ID          string    `gorm:"column:id;type:varchar(256);primaryKey"`
	SessionID   string    `gorm:"column:session_id;type:uuid;index"`
	Checkpoint  []byte    `gorm:"column:checkpoint;type:bytea;not null"`
	ExpiredAt   time.Time `gorm:"column:expired_at;index"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

func (AgentCheckpoint) TableName() string {
	return "agent_checkpoints"
}
