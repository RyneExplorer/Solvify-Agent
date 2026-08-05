package entity

import (
	"encoding/json"
	"time"
)

type MessageFeedback struct {
	ID         string          `gorm:"primaryKey;type:varchar(64)" json:"id"`
	MessageID  string          `gorm:"index;type:varchar(64);not null" json:"message_id"`
	UserID     string          `gorm:"index;type:varchar(64);not null" json:"user_id"`
	SessionID  string          `gorm:"index;type:varchar(64)" json:"session_id,omitempty"`
	Rating     int             `gorm:"not null;default:0" json:"rating"`
	ReasonTag  string          `gorm:"type:varchar(64)" json:"reason_tag,omitempty"`
	ReasonsRaw json.RawMessage `gorm:"column:reasons;type:jsonb" json:"-"`
	Comment    string          `gorm:"type:text" json:"comment,omitempty"`
	IsQuick    bool            `gorm:"column:is_quick;default:false" json:"is_quick_reply"`
	TraceID    string          `gorm:"index;type:varchar(128)" json:"trace_id,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

func (MessageFeedback) TableName() string { return "message_feedback" }

func (m *MessageFeedback) Reasons() []string {
	if len(m.ReasonsRaw) == 0 {
		return nil
	}
	var out []string
	_ = json.Unmarshal(m.ReasonsRaw, &out)
	return out
}

func (m *MessageFeedback) SetReasons(rs []string) {
	if len(rs) == 0 {
		m.ReasonsRaw = nil
		return
	}
	b, _ := json.Marshal(rs)
	m.ReasonsRaw = b
}
