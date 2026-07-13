package repository

import (
	"errors"
	"strings"

	"solvify-agent/internal/model/entity"

	"gorm.io/gorm"
)

// userRepository 用户仓储实现
type userRepository struct {
	db *gorm.DB
}

// NewUserRepository 创建用户仓储
func NewUserRepository(db *gorm.DB) UserRepository {
	return &userRepository{db: db}
}

// FindByID 根据 ID 查找用户
func (r *userRepository) FindByID(id string) (*entity.User, error) {
	var user entity.User
	err := r.db.Where("id = ?", id).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

// FindByUsername 根据用户名查找用户
func (r *userRepository) FindByUsername(username string) (*entity.User, error) {
	var user entity.User
	err := r.db.Where("username = ?", username).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

// FindByEmail 根据邮箱查找用户
func (r *userRepository) FindByEmail(email string) (*entity.User, error) {
	var user entity.User
	err := r.db.Where("email = ?", email).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

// Create 创建用户
func (r *userRepository) Create(user *entity.User) error {
	return r.db.Create(user).Error
}

// Update 更新用户
func (r *userRepository) Update(id string, updates map[string]interface{}) error {
	if len(updates) == 0 {
		return nil
	}

	return r.db.Model(&entity.User{}).
		Where("id = ?", id).
		Updates(updates).Error
}

// Delete 删除用户
func (r *userRepository) Delete(id string) error {
	return r.db.Where("id = ?", id).Delete(&entity.User{}).Error
}

// AdminList 管理员分页获取用户列表
func (r *userRepository) AdminList(offset, limit int, filter *UserListFilter) ([]*entity.User, int64, error) {
	var (
		users []*entity.User
		total int64
	)

	q := r.db.Model(&entity.User{})
	if filter != nil {
		if username := strings.TrimSpace(filter.Username); username != "" {
			q = q.Where("username LIKE ?", "%"+username+"%")
		}
		if email := strings.TrimSpace(filter.Email); email != "" {
			q = q.Where("email LIKE ?", "%"+email+"%")
		}
		if filter.Status != nil {
			q = q.Where("status = ?", *filter.Status)
		}
		if filter.Role != nil {
			q = q.Where("role = ?", *filter.Role)
		}
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&users).Error; err != nil {
		return nil, 0, err
	}

	return users, total, nil
}

// ExistsByUsername 检查用户名是否存在
func (r *userRepository) ExistsByUsername(username string) (bool, error) {
	var count int64
	err := r.db.Model(&entity.User{}).Where("username = ?", username).Count(&count).Error
	return count > 0, err
}

// ExistsByEmail 检查邮箱是否存在
func (r *userRepository) ExistsByEmail(email string) (bool, error) {
	var count int64
	err := r.db.Model(&entity.User{}).Where("email = ?", email).Count(&count).Error
	return count > 0, err
}
