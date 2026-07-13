package entity

import "time"

// User 用户实体
type User struct {
	ID        string    `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	Username  string    `gorm:"type:varchar(50);not null;comment:用户名" json:"username"`
	Password  string    `gorm:"type:varchar(255);not null;comment:密码哈希" json:"-"`
	Email     string    `gorm:"type:varchar(100);comment:邮箱" json:"email"`
	Avatar    string    `gorm:"type:varchar(255);comment:头像" json:"avatar"`
	Status    int       `gorm:"type:smallint;default:1;comment:状态:1正常, 2禁用, 3注销, 4待验证" json:"status"`
	Role      int       `gorm:"type:smallint;default:1;comment:角色:1普通用户, 2管理员" json:"role"`
	LastModel string    `gorm:"type:varchar(255);comment:上次使用的模型" json:"lastModel"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName 指定表名
func (User) TableName() string {
	return "users"
}
