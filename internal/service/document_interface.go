package service

import (
	"context"
	"mime/multipart"

	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
)

// DocumentServiceInterface 定义文档服务接口
type DocumentServiceInterface interface {
	Upload(ctx context.Context, userID, kbID string, fileHeader *multipart.FileHeader) (dto.UploadDocumentResponse, error)
	CreateNote(ctx context.Context, userID, kbID string, req requestdto.CreateNoteRequest) (dto.DocumentResponse, error)
	List(ctx context.Context, userID, kbID string) ([]dto.DocumentResponse, error)
	Detail(ctx context.Context, userID, documentID string) (dto.DocumentResponse, error)
	Preview(ctx context.Context, userID, documentID string) (DocumentPreview, error)
	Delete(ctx context.Context, userID, documentID string) error
	Process(ctx context.Context, userID, documentID string) (dto.DocumentProcessingJobResponse, error)
	ListJobs(ctx context.Context, userID, documentID string) ([]dto.DocumentProcessingJobResponse, error)
	JobDetail(ctx context.Context, userID, jobID string) (dto.DocumentProcessingJobResponse, error)
	ListVersions(ctx context.Context, userID, documentID string) ([]dto.DocumentVersionListItemResponse, error)
	VersionDetail(ctx context.Context, userID, documentID, versionID string) (dto.DocumentVersionDetailResponse, error)
	CreateVersion(ctx context.Context, userID, documentID string, req requestdto.CreateDocumentVersionRequest) (dto.DocumentProcessingJobResponse, error)
	Reindex(ctx context.Context, userID, documentID string) (dto.DocumentProcessingJobResponse, error)
}

// DocumentPreview 描述原始文件预览信息
type DocumentPreview struct {
	Path     string
	FileName string
}
