package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
)

type MCPTemplateRepository interface {
	Create(ctx context.Context, template *entity.MCPTemplate) error
	Update(ctx context.Context, template *entity.MCPTemplate) error
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (*entity.MCPTemplate, error)
	List(ctx context.Context) ([]entity.MCPTemplate, error)
	UpdateManifest(ctx context.Context, id string, connection, manifest []byte) error
}
