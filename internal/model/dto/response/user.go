package response

import "time"

// UserResponse 用户基本信息响应（含画像字段）
type UserResponse struct {
	ID                string    `json:"id"`
	Username          string    `json:"username"`
	Email             string    `json:"email"`
	Avatar            string    `json:"avatar"`
	Status            int       `json:"status"`
	Role              int       `json:"role"`
	LastModel         string    `json:"lastModel,omitempty"`
	Department        string    `json:"department,omitempty"`
	Position          string    `json:"position,omitempty"`
	Expertise         string    `json:"expertise,omitempty"`
	PreferredLanguage string    `json:"preferred_language,omitempty"`
	Timezone          string    `json:"timezone,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// UserPreferenceResponse 用户偏好响应
type UserPreferenceResponse struct {
	UserID            string   `json:"user_id"`
	DefaultModelID    string   `json:"default_model_id,omitempty"`
	PreferredKBIDs    []string `json:"preferred_kb_ids,omitempty"`
	AnswerStyle       string   `json:"answer_style"`
	AutoDeepMode      bool     `json:"auto_deep_mode"`
	AutoDeepThreshold int      `json:"auto_deep_threshold"`
	UseMarkdownTable  bool     `json:"use_markdown_table"`
	CitationStyle     string   `json:"citation_style"`
	UpdatedAt         string   `json:"updated_at"`
}

// ProfileResponse 合并返回 基本信息 + 偏好
type ProfileResponse struct {
	User       UserResponse           `json:"user"`
	Preference UserPreferenceResponse `json:"preference"`
}

// AdminUserListItem 管理员用户列表项
type AdminUserListItem struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	Avatar    string    `json:"avatar"`
	Role      int       `json:"role"`
	Status    int       `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// LoginResponse 登录响应
type LoginResponse struct {
	Token string       `json:"token"`
	User  UserResponse `json:"user"`
}

