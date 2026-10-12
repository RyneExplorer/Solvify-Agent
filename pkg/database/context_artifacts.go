package database

import (
	"context"
	"gorm.io/gorm"
	"solvify-agent/scripts/migrations"
)

// EnsureContextArtifactSchema uses the same idempotent SQL as an existing-database upgrade.
func EnsureContextArtifactSchema(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Exec(migrations.ContextArtifacts).Error
}
