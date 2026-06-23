package service

import (
	"context"

	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
)

// SyncServiceInterface 定义同步业务用例能力
type SyncServiceInterface interface {
	CreateSource(ctx context.Context, userID string, req requestdto.CreateSyncSourceRequest) (dto.SyncSourceResponse, error)
	ListSources(ctx context.Context, userID string) ([]dto.SyncSourceResponse, error)
	SourceDetail(ctx context.Context, userID, sourceID string) (dto.SyncSourceResponse, error)
	UpdateSource(ctx context.Context, userID, sourceID string, req requestdto.UpdateSyncSourceRequest) (dto.SyncSourceResponse, error)
	DeleteSource(ctx context.Context, userID, sourceID string) error
	CreateJob(ctx context.Context, userID, sourceID string) (dto.SyncJobResponse, error)
	ListJobs(ctx context.Context, userID, sourceID string) ([]dto.SyncJobResponse, error)
	JobDetail(ctx context.Context, userID, jobID string) (dto.SyncJobResponse, error)
	ListItems(ctx context.Context, userID, sourceID string) ([]dto.SyncItemResponse, error)
	ImportItem(ctx context.Context, userID, itemID string) (dto.DocumentResponse, error)
}
