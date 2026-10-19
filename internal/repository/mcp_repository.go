package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"

	"solvify-agent/internal/model/entity"
)

type mcpRepository struct {
	db     *gorm.DB
	logger gormlogger.Interface
}

func NewMCPRepository(db *gorm.DB) MCPRepository {
	return &mcpRepository{db: db, logger: db.Logger.LogMode(gormlogger.Silent)}
}
func (r *mcpRepository) Create(ctx context.Context, c *entity.MCPConnection) error {
	err := dbFor(ctx, r.db).Session(&gorm.Session{Logger: r.logger}).Omit("Template").Create(c).Error
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return gorm.ErrDuplicatedKey
	}
	return err
}
func (r *mcpRepository) Update(ctx context.Context, c *entity.MCPConnection) error {
	result := dbFor(ctx, r.db).Session(&gorm.Session{Logger: r.logger}).Model(c).
		Clauses(clause.Returning{Columns: []clause.Column{{Name: "updated_at"}}}).
		Where("id = ? AND user_id = ? AND updated_at = ?", c.ID, c.UserID, c.UpdatedAt).
		Select("display_name", "connection", "config", "is_enabled", "updated_at").Updates(c)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrMCPConnectionChanged
	}
	return nil
}

// 由数据库原子修改单个工具偏好，避免并行开关丢失或覆盖用户凭据。
func (r *mcpRepository) SetToolEnabled(ctx context.Context, userID, id, name string, enabled bool) error {
	array := "COALESCE(NULLIF(config->'disabled_tools', 'null'::jsonb), '[]'::jsonb)"
	expression := "jsonb_set(config, '{disabled_tools}', " + array + " - ?::text, true)"
	args := []interface{}{name}
	if !enabled {
		expression = "jsonb_set(config, '{disabled_tools}', (" + array + " - ?::text) || jsonb_build_array(?::text), true)"
		args = append(args, name)
	}
	query := dbFor(ctx, r.db).Session(&gorm.Session{Logger: r.logger}).Model(&entity.MCPConnection{}).Where("id = ? AND user_id = ?", id, userID)
	if !enabled {
		query = query.Where("jsonb_array_length("+array+") < 128 OR jsonb_exists("+array+", ?)", name)
	}
	result := query.Updates(map[string]interface{}{"config": gorm.Expr(expression, args...), "updated_at": gorm.Expr("clock_timestamp()")})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrMCPToolStateChanged
	}
	return nil
}
func (r *mcpRepository) Delete(ctx context.Context, userID, id string) error {
	return dbFor(ctx, r.db).Session(&gorm.Session{Logger: r.logger}).Where("user_id = ? AND id = ?", userID, id).Delete(&entity.MCPConnection{}).Error
}
func (r *mcpRepository) Get(ctx context.Context, userID, id string) (*entity.MCPConnection, error) {
	var c entity.MCPConnection
	err := dbFor(ctx, r.db).Session(&gorm.Session{Logger: r.logger}).Preload("Template").Where("user_id = ? AND id = ?", userID, id).First(&c).Error
	return &c, err
}
func (r *mcpRepository) List(ctx context.Context, userID string) ([]entity.MCPConnection, error) {
	var list []entity.MCPConnection
	err := dbFor(ctx, r.db).Session(&gorm.Session{Logger: r.logger}).Preload("Template").Where("user_id = ?", userID).Order("created_at, id").Find(&list).Error
	return list, err
}
func (r *mcpRepository) ListEnabledByUserID(ctx context.Context, userID string) ([]entity.MCPConnection, error) {
	var list []entity.MCPConnection
	err := dbFor(ctx, r.db).Session(&gorm.Session{Logger: r.logger}).Preload("Template").Where("user_id = ? AND is_enabled = ?", userID, true).Order("created_at, id").Find(&list).Error
	return list, err
}
