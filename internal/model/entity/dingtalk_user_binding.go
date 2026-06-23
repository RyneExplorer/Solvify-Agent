package entity

import "time"

// DingTalkUserBinding 映射钉钉用户绑定表
type DingTalkUserBinding struct {
	ID          string    `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	UserID      string    `gorm:"column:user_id;type:uuid;not null;uniqueIndex"`
	DingOpenID  string    `gorm:"column:ding_open_id;type:varchar(128);not null;default:''"`
	DingUnionID string    `gorm:"column:ding_union_id;type:varchar(128);not null;uniqueIndex"`
	CorpID      string    `gorm:"column:corp_id;type:varchar(128);not null;default:''"`
	Nickname    string    `gorm:"column:nickname;type:varchar(128);not null;default:''"`
	Avatar      string    `gorm:"column:avatar;type:text;not null;default:''"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

// TableName 返回钉钉用户绑定表名
func (DingTalkUserBinding) TableName() string {
	return "dingtalk_user_bindings"
}
