package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"solvify-agent/internal/model/entity"
)

type mcpTemplateRepository struct {
	db     *gorm.DB
	logger gormlogger.Interface
}

func NewMCPTemplateRepository(db *gorm.DB) MCPTemplateRepository {
	// 凭据可能出现在SQL参数中，由业务边界记录脱敏错误。
	return &mcpTemplateRepository{db: db, logger: db.Logger.LogMode(gormlogger.Silent)}
}
func (r *mcpTemplateRepository) Create(ctx context.Context, t *entity.MCPTemplate) error {
	err := dbFor(ctx, r.db).Session(&gorm.Session{Logger: r.logger}).Create(t).Error
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return gorm.ErrDuplicatedKey
	}
	return err
}
func (r *mcpTemplateRepository) Update(ctx context.Context, t *entity.MCPTemplate) error {
	// 比较读取时的版本，过期编辑不能覆盖并发修改。
	result := dbFor(ctx, r.db).Session(&gorm.Session{Logger: r.logger}).Model(&entity.MCPTemplate{}).Where("id = ? AND updated_at = ?", t.ID, t.UpdatedAt).
		Select("name", "description", "connection", "config_schema", "is_enabled", "updated_at").Updates(t)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("MCP预置已更新或删除，请刷新后重试")
	}
	return nil
}
func (r *mcpTemplateRepository) Delete(ctx context.Context, id string) error {
	result := dbFor(ctx, r.db).Session(&gorm.Session{Logger: r.logger}).Delete(&entity.MCPTemplate{}, "id = ? AND is_system = ?", id, false)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("MCP预置已更新或不可删除，请刷新后重试")
	}
	return nil
}
func (r *mcpTemplateRepository) GetByID(ctx context.Context, id string) (*entity.MCPTemplate, error) {
	var t entity.MCPTemplate
	err := dbFor(ctx, r.db).Session(&gorm.Session{Logger: r.logger}).First(&t, "id = ?", id).Error
	return &t, err
}
func (r *mcpTemplateRepository) List(ctx context.Context) ([]entity.MCPTemplate, error) {
	var rows []entity.MCPTemplate
	err := dbFor(ctx, r.db).Session(&gorm.Session{Logger: r.logger}).Order("display_order, name, id").Find(&rows).Error
	return rows, err
}

// 发现期间连接已变化时不覆盖清单，也不改动配置的编辑版本。
func (r *mcpTemplateRepository) UpdateManifest(ctx context.Context, id string, connection, manifest []byte) error {
	return dbFor(ctx, r.db).Session(&gorm.Session{Logger: r.logger}).Model(&entity.MCPTemplate{}).Where("id = ? AND connection = ?::jsonb", id, string(connection)).UpdateColumn("mcp_tool_manifest", datatypes.JSON(manifest)).Error
}
