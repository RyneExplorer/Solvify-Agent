package service

import (
	"context"
	"mime/multipart"

	dto "solvify-agent/internal/model/dto/response"
)

// DocumentServiceInterface 定义文档服务接口
type DocumentServiceInterface interface {
	Upload(ctx context.Context, userID, kbID string, fileHeader *multipart.FileHeader) (dto.DocumentResponse, error)
	List(ctx context.Context, userID, kbID string) ([]dto.DocumentResponse, error)
	Detail(ctx context.Context, userID, documentID string) (dto.DocumentResponse, error)
	Delete(ctx context.Context, userID, documentID string) error
	Process(ctx context.Context, userID, documentID string) (dto.DocumentProcessingJobResponse, error)
	ListJobs(ctx context.Context, userID, documentID string) ([]dto.DocumentProcessingJobResponse, error)
	JobDetail(ctx context.Context, userID, jobID string) (dto.DocumentProcessingJobResponse, error)
}
