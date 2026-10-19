package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
)

type MCPRepository interface {
	Create(ctx context.Context, connection *entity.MCPConnection) error
	Update(ctx context.Context, connection *entity.MCPConnection) error
	Delete(ctx context.Context, userID, id string) error
	Get(ctx context.Context, userID, id string) (*entity.MCPConnection, error)
	List(ctx context.Context, userID string) ([]entity.MCPConnection, error)
	ListEnabledByUserID(ctx context.Context, userID string) ([]entity.MCPConnection, error)
}
