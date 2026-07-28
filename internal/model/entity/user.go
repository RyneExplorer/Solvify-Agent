package entity

import "time"

// User 用户实体
type User struct {
	ID                string    `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Username          string    `gorm:"type:varchar(50);not null;comment:用户名" json:"username"`
	Password          string    `gorm:"type:varchar(255);not null;comment:密码哈希" json:"-"`
	Email             string    `gorm:"type:varchar(100);comment:邮箱" json:"email"`
	Avatar            string    `gorm:"type:varchar(255);comment:头像" json:"avatar"`
	Status            int       `gorm:"type:smallint;default:1;comment:状态:1正常, 2禁用, 3注销, 4待验证" json:"status"`
	Role              int       `gorm:"type:smallint;default:1;comment:角色:1普通用户, 2管理员" json:"role"`
	LastModel         string    `gorm:"type:varchar(255);comment:上次使用的模型" json:"lastModel"`
	Department        string    `gorm:"type:varchar(100);comment:部门" json:"department"`
	Position          string    `gorm:"type:varchar(100);comment:职位" json:"position"`
	Expertise         string    `gorm:"type:varchar(255);comment:擅长领域/业务方向，逗号分隔" json:"expertise"`
	PreferredLanguage string    `gorm:"type:varchar(20);default:zh-CN;comment:偏好回答语言：zh-CN/en-US/ja-JP 等" json:"preferred_language"`
	Timezone          string    `gorm:"type:varchar(50);default:Asia/Shanghai;comment:时区 IANA，如 Asia/Shanghai" json:"timezone"`
	CreatedAt         time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName 指定表名
func (User) TableName() string {
	return "users"
}
