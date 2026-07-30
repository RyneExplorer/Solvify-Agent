package repository

import (
	"errors"

	"solvify-agent/internal/model/entity"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type userPreferenceRepo struct {
	db *gorm.DB
}

func NewUserPreferenceRepository(db *gorm.DB) UserPreferenceRepository {
	return &userPreferenceRepo{db: db}
}

func (r *userPreferenceRepo) FindByUserID(userID string) (*entity.UserPreference, error) {
	var p entity.UserPreference
	err := r.db.Where("user_id = ?", userID).First(&p).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (r *userPreferenceRepo) Upsert(pref *entity.UserPreference) error {
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		UpdateAll: true,
	}).Create(pref).Error
}

func (r *userPreferenceRepo) Update(userID string, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}
	return r.db.Model(&entity.UserPreference{}).Where("user_id = ?", userID).Updates(updates).Error
}

func (r *userPreferenceRepo) DeleteByUserID(userID string) error {
	return r.db.Where("user_id = ?", userID).Delete(&entity.UserPreference{}).Error
}
