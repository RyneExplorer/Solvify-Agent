package response

import "time"

// AdminSessionListItem 管理员会话列表项
type AdminSessionListItem struct {
	ID           string    `json:"id"`
	UserID       string    `json:"user_id"`
	Username     string    `json:"username"`
	Title        string    `json:"title"`
	ModelID      string    `json:"model_id"`
	Status       string    `json:"status"`
	MessageCount int64     `json:"message_count"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
