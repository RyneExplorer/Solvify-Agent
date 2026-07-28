package repository

import (
	"solvify-agent/internal/model/entity"
)

// UserListFilter 用户列表筛选条件
type UserListFilter struct {
	Username string
	Email    string
	Status   *int
	Role     *int
}

// UserRepository 用户仓储接口
type UserRepository interface {
	FindByID(id string) (*entity.User, error)
	FindByUsername(username string) (*entity.User, error)
	FindByEmail(email string) (*entity.User, error)
	Create(user *entity.User) error
	Update(id string, updates map[string]interface{}) error
	UpdateProfile(id string, upd *UserProfileUpdate) error
	Delete(id string) error
	AdminList(offset, limit int, filter *UserListFilter) ([]*entity.User, int64, error)
	ExistsByUsername(username string) (bool, error)
	ExistsByEmail(email string) (bool, error)
}
