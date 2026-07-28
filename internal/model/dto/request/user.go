package request

// RegisterRequest 用户注册请求
type RegisterRequest struct {
	Username        string `json:"username" binding:"required,min=3,max=50"`
	Password        string `json:"password" binding:"required,min=6,max=300"`
	ConfirmPassword string `json:"confirm_password" binding:"required"`
	Email           string `json:"email" binding:"required,email"`
	EmailCaptcha    string `json:"captcha" binding:"required,len=6"`
}

// LoginRequest 用户登录请求
type LoginRequest struct {
	Username  string `json:"username" binding:"required"`
	Password  string `json:"password" binding:"required"`
	CaptchaID string `json:"captcha_id" binding:"required"`
	Captcha   string `json:"captcha" binding:"required,len=4"`
}

// UpdateUserRequest 更新用户基本信息请求（头像/邮箱）
type UpdateUserRequest struct {
	Avatar string `json:"avatar" binding:"omitempty,url,max=255"`
	Email  string `json:"email" binding:"omitempty,email,max=100"`
}

// UpdateProfileRequest 更新用户画像请求（部门/职位/擅长/语言/时区）
type UpdateProfileRequest struct {
	Department        string `json:"department" binding:"omitempty,max=100"`
	Position          string `json:"position" binding:"omitempty,max=100"`
	Expertise         string `json:"expertise" binding:"omitempty,max=255"`
	PreferredLanguage string `json:"preferred_language" binding:"omitempty,oneof=zh-CN en-US ja-JP ko-KR fr-FR de-DE es-ES"`
	Timezone          string `json:"timezone" binding:"omitempty,max=50"`
}

// UpdateUserPreferenceRequest 更新用户偏好请求
type UpdateUserPreferenceRequest struct {
	DefaultModelID    *string  `json:"default_model_id" binding:"omitempty,max=36"`
	PreferredKBIDs    []string `json:"preferred_kb_ids" binding:"omitempty,max=50"`
	AnswerStyle       string   `json:"answer_style" binding:"omitempty,oneof=concise balanced detailed step_by_step"`
	AutoDeepMode      *bool    `json:"auto_deep_mode"`
	AutoDeepThreshold *int     `json:"auto_deep_threshold" binding:"omitempty,min=1,max=5"`
	UseMarkdownTable  *bool    `json:"use_markdown_table"`
	CitationStyle     string   `json:"citation_style" binding:"omitempty,oneof=none section_title doc_title_only"`
}

// ChangePasswordRequest 修改密码请求
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6,max=50"`
}

// AdminUserListRequest 管理员用户列表请求
type AdminUserListRequest struct {
	Page     int    `form:"page" binding:"required,min=1"`
	PageSize int    `form:"pageSize" binding:"required,min=1,max=100"`
	Username string `form:"username"`
	Email    string `form:"email"`
	Status   *int   `form:"status"`
	Role     *int   `form:"role"`
}

// AdminCreateUserRequest 管理员创建用户请求
type AdminCreateUserRequest struct {
	Username string `json:"username" binding:"required,min=3,max=50"`
	Email    string `json:"email" binding:"required,email,max=100"`
	Password string `json:"password" binding:"required,min=6,max=50"`
	Status   int    `json:"status" binding:"oneof=1 2 3 4"`
	Role     int    `json:"role" binding:"oneof=1 2"`
}

// AdminUpdateUserRequest 管理员更新用户请求
type AdminUpdateUserRequest struct {
	Username string `json:"username" binding:"omitempty,min=3,max=50"`
	Email    string `json:"email" binding:"omitempty,email,max=100"`
	Status   *int   `json:"status" binding:"omitempty,oneof=1 2 3 4"`
	Role     *int   `json:"role" binding:"omitempty,oneof=1 2"`
}

// AdminResetPasswordRequest 管理员重置密码请求
type AdminResetPasswordRequest struct {
	Password string `json:"password" binding:"required,min=6,max=50"`
}

