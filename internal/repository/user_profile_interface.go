package repository

import (
	"solvify-agent/internal/model/entity"
)

// UserProfileUpdate 用户画像字段更新（由 Service 组装）
type UserProfileUpdate struct {
	Department        *string
	Position          *string
	Expertise         *string
	PreferredLanguage *string
	Timezone          *string
}

// UserPreferenceRepository 用户偏好仓储接口
type UserPreferenceRepository interface {
	FindByUserID(userID string) (*entity.UserPreference, error)
	Upsert(pref *entity.UserPreference) error
	Update(userID string, updates map[string]interface{}) error
	DeleteByUserID(userID string) error
}
