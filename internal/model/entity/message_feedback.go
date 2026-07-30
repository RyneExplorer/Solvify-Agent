package entity

import "time"

type MessageFeedback struct {
	ID        string    `gorm:"primaryKey;type:varchar(64)" json:"id"`
	MessageID string    `gorm:"index;type:varchar(64);not null" json:"message_id"`
	UserID    string    `gorm:"index;type:varchar(64);not null" json:"user_id"`
	SessionID string    `gorm:"index;type:varchar(64)" json:"session_id,omitempty"`
	Rating    int       `gorm:"not null;default:0" json:"rating"`
	ReasonTag string    `gorm:"type:varchar(64)" json:"reason_tag,omitempty"`
	Comment   string    `gorm:"type:text" json:"comment,omitempty"`
	TraceID   string    `gorm:"index;type:varchar(128)" json:"trace_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func (MessageFeedback) TableName() string { return "message_feedback" }
