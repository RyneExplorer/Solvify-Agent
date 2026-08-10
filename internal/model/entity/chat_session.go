package entity

import (
	"encoding/json"
	"time"

	"gorm.io/datatypes"
)

// ChatSession 映射聊天会话表
type ChatSession struct {
	ID                string         `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	UserID            string         `gorm:"column:user_id;type:uuid;not null"`
	Title             string         `gorm:"column:title;size:200;not null;default:''"`
	ModelID           string         `gorm:"column:model_id;type:varchar(36);not null"`
	Status            string         `gorm:"column:status;type:varchar(20);not null;default:'active'"`
	PendingClarify    datatypes.JSON `gorm:"column:pending_clarify;type:jsonb;not null;default:'{}'::jsonb"`
	PendingCheckpoint datatypes.JSON `gorm:"column:pending_checkpoint;type:jsonb;not null;default:'{}'::jsonb"`
	CreatedAt         time.Time      `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt         time.Time      `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName 返回聊天会话表名
func (ChatSession) TableName() string {
	return "chat_sessions"
}

// PendingClarifyData 待澄清追问信息
type PendingClarifyData struct {
	Question string    `json:"question"`
	Options  []string  `json:"options,omitempty"`
	SetAt    time.Time `json:"set_at"`
}

// HasPendingClarify 检查是否有待处理的澄清追问
func (s *ChatSession) HasPendingClarify() bool {
	if len(s.PendingClarify) == 0 {
		return false
	}
	var pc PendingClarifyData
	if err := json.Unmarshal(s.PendingClarify, &pc); err != nil {
		return false
	}
	return pc.Question != ""
}

// GetPendingClarify 解析并返回待澄清信息
func (s *ChatSession) GetPendingClarify() (*PendingClarifyData, error) {
	if len(s.PendingClarify) == 0 {
		return nil, nil
	}
	var pc PendingClarifyData
	if err := json.Unmarshal(s.PendingClarify, &pc); err != nil {
		return nil, err
	}
	if pc.Question == "" {
		return nil, nil
	}
	return &pc, nil
}

// IsExpired 检查澄清追问是否超时
func (pc *PendingClarifyData) IsExpired(timeout time.Duration) bool {
	if pc == nil || pc.SetAt.IsZero() {
		return false
	}
	return time.Since(pc.SetAt) > timeout
}

const ClarifyDefaultTimeout = 10 * time.Minute

// PendingCheckpointData 待恢复的 Agent checkpoint 信息
type PendingCheckpointData struct {
	CheckpointID string    `json:"checkpoint_id"`
	InterruptID  string    `json:"interrupt_id"`
	Question     string    `json:"question,omitempty"`
	ToolName     string    `json:"tool_name,omitempty"`
	IsClarify    bool      `json:"is_clarify,omitempty"`
	Options      []string  `json:"options,omitempty"`
	SetAt        time.Time `json:"set_at"`
}

// HasPendingCheckpoint 检查 session 是否有待恢复的 checkpoint
func (s *ChatSession) HasPendingCheckpoint() bool {
	if len(s.PendingCheckpoint) == 0 {
		return false
	}
	var pc PendingCheckpointData
	if err := json.Unmarshal(s.PendingCheckpoint, &pc); err != nil {
		return false
	}
	return pc.CheckpointID != ""
}

// GetPendingCheckpoint 解析并返回待恢复的 checkpoint 信息
func (s *ChatSession) GetPendingCheckpoint() (*PendingCheckpointData, error) {
	if len(s.PendingCheckpoint) == 0 {
		return nil, nil
	}
	var pc PendingCheckpointData
	if err := json.Unmarshal(s.PendingCheckpoint, &pc); err != nil {
		return nil, err
	}
	if pc.CheckpointID == "" {
		return nil, nil
	}
	return &pc, nil
}

// IsExpired 检查 checkpoint 是否已超过存活时长
func (pc *PendingCheckpointData) IsExpired(timeout time.Duration) bool {
	if pc == nil || pc.SetAt.IsZero() {
		return false
	}
	return time.Since(pc.SetAt) > timeout
}

const CheckpointDefaultTimeout = 24 * time.Hour
