package repository

import (
	"context"
	"errors"
	"solvify-agent/internal/model/entity"
	"time"
)

var ErrArtifactUnavailable = errors.New("上下文产物不存在、已失效或无权访问")
var ErrArtifactQuota = errors.New("上下文产物存储额度不足")

type ArtifactScope struct{ UserID, SessionID, RunID, AgentID, TaskID string }
type ArtifactLimits struct{ ArtifactBytes, RunBytes, UserBytes int64 }

type ContextArtifactRepo interface {
	SaveImmutable(ctx context.Context, artifact *entity.ContextArtifact, limits ArtifactLimits) error
	FindScoped(ctx context.Context, scope ArtifactScope, id string) (*entity.ContextArtifact, error)
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}
