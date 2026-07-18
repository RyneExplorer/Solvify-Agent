package response

import "time"

// UserModelConfigInfo 描述用户模型配置信息
type UserModelConfigInfo struct {
	ID               string      `json:"id"`
	DisplayName      string      `json:"display_name"`
	APIFormat        string      `json:"api_format"`
	ModelID          string      `json:"model_id"`
	BaseURL          string      `json:"base_url"`
	APIKey           string      `json:"api_key"` // 脱敏后的 APIKey
	Config           interface{} `json:"config,omitempty"`
	MaxContextLength int         `json:"max_context_length"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

// ListUserModelConfigsResponse 描述用户模型配置列表响应
type ListUserModelConfigsResponse struct {
	Models []UserModelConfigInfo `json:"models"`
}
