package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

type mcpRepository struct{ db *gorm.DB }

func NewMCPRepository(db *gorm.DB) MCPRepository { return &mcpRepository{db: db} }
func (r *mcpRepository) Create(ctx context.Context, c *entity.MCPConnection) error {
	err := dbFor(ctx, r.db).Omit("Template").Create(c).Error
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return gorm.ErrDuplicatedKey
	}
	return err
}
func (r *mcpRepository) Update(ctx context.Context, c *entity.MCPConnection) error {
	result := dbFor(ctx, r.db).Model(&entity.MCPConnection{}).Where("id = ? AND user_id = ?", c.ID, c.UserID).Select("display_name", "connection", "config", "is_enabled", "updated_at").Updates(c)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
func (r *mcpRepository) Delete(ctx context.Context, userID, id string) error {
	return dbFor(ctx, r.db).Where("user_id = ? AND id = ?", userID, id).Delete(&entity.MCPConnection{}).Error
}
func (r *mcpRepository) Get(ctx context.Context, userID, id string) (*entity.MCPConnection, error) {
	var c entity.MCPConnection
	err := dbFor(ctx, r.db).Preload("Template.ToolType").Where("user_id = ? AND id = ?", userID, id).First(&c).Error
	return &c, err
}
func (r *mcpRepository) List(ctx context.Context, userID string) ([]entity.MCPConnection, error) {
	var list []entity.MCPConnection
	err := dbFor(ctx, r.db).Preload("Template.ToolType").Where("user_id = ?", userID).Order("created_at, id").Find(&list).Error
	return list, err
}
func (r *mcpRepository) ListEnabledByUserID(ctx context.Context, userID string) ([]entity.MCPConnection, error) {
	var list []entity.MCPConnection
	err := dbFor(ctx, r.db).Preload("Template.ToolType").Where("user_id = ? AND is_enabled = ?", userID, true).Order("created_at, id").Find(&list).Error
	return list, err
}
