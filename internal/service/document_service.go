package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	apperrors "solvify-agent/pkg/errors"
)

const (
	documentStatusUploaded   = 1
	documentStatusProcessing = 2
	documentStatusFailed     = 4
	documentStatusDeleted    = 5

	documentSourceUpload = "upload"

	documentJobTypeProcess   = "process"
	documentJobStatusPending = 1

	maxDocumentFileSize = 100 * 1024 * 1024
	defaultUploadRoot   = "data/uploads"
)

var allowedDocumentFileTypes = map[string]struct{}{
	"pdf": {}, "doc": {}, "docx": {}, "txt": {}, "md": {}, "markdown": {}, "html": {},
	"csv": {}, "xls": {}, "xlsx": {}, "ppt": {}, "pptx": {}, "json": {},
	"png": {}, "jpg": {}, "jpeg": {},
}

// DocumentService 封装文档业务用例
type DocumentService struct {
	knowledgeBaseRepo repository.KnowledgeBaseRepository
	documentRepo      repository.DocumentRepository
	documentJobRepo   repository.DocumentProcessingJobRepository
	storageQuotaRepo  repository.StorageQuotaRepository
	uploadRoot        string
}

// NewDocumentService 创建文档业务服务
func NewDocumentService(knowledgeBaseRepo repository.KnowledgeBaseRepository, documentRepo repository.DocumentRepository, documentJobRepo repository.DocumentProcessingJobRepository, storageQuotaRepo repository.StorageQuotaRepository) *DocumentService {
	return NewDocumentServiceWithUploadRoot(knowledgeBaseRepo, documentRepo, documentJobRepo, storageQuotaRepo, defaultUploadRoot)
}

// NewDocumentServiceWithUploadRoot 创建指定上传目录的文档业务服务
func NewDocumentServiceWithUploadRoot(knowledgeBaseRepo repository.KnowledgeBaseRepository, documentRepo repository.DocumentRepository, documentJobRepo repository.DocumentProcessingJobRepository, storageQuotaRepo repository.StorageQuotaRepository, uploadRoot string) *DocumentService {
	return &DocumentService{
		knowledgeBaseRepo: knowledgeBaseRepo,
		documentRepo:      documentRepo,
		documentJobRepo:   documentJobRepo,
		storageQuotaRepo:  storageQuotaRepo,
		uploadRoot:        uploadRoot,
	}
}

// Upload 上传文档并写入文档表
func (s *DocumentService) Upload(ctx context.Context, userID, kbID string, fileHeader *multipart.FileHeader) (dto.DocumentResponse, error) {
	if fileHeader == nil {
		return dto.DocumentResponse{}, apperrors.New(apperrors.CodeBadRequest, "上传文件不能为空")
	}

	// 1. 先校验知识库写入权限和文件业务约束
	kb, err := s.findWritableKnowledgeBase(ctx, userID, kbID)
	if err != nil {
		return dto.DocumentResponse{}, err
	}
	if err := s.validateFile(ctx, userID, kb.ID, fileHeader); err != nil {
		return dto.DocumentResponse{}, err
	}

	// 2. 文件保存成功后再创建文档记录，避免数据库记录指向不存在的文件
	storagePath, fileHash, err := s.saveFile(userID, kb.ID, fileHeader)
	if err != nil {
		return dto.DocumentResponse{}, err
	}

	doc := entity.Document{
		UserID:          userID,
		KnowledgeBaseID: kb.ID,
		Title:           strings.TrimSuffix(fileHeader.Filename, filepath.Ext(fileHeader.Filename)),
		FileName:        filepath.Base(fileHeader.Filename),
		FileType:        documentFileType(fileHeader.Filename),
		FileSize:        fileHeader.Size,
		StoragePath:     storagePath,
		FileHash:        fileHash,
		SourceType:      documentSourceUpload,
		Status:          documentStatusUploaded,
		ErrorMessage:    "",
	}
	if err := s.documentRepo.Create(ctx, &doc); err != nil {
		_ = os.Remove(storagePath)
		return dto.DocumentResponse{}, err
	}

	// 3. 配额是上传业务的内部副作用，不单独开放写接口
	if err := s.storageQuotaRepo.AddUsedStorage(ctx, userID, defaultMaxStorageBytes, fileHeader.Size); err != nil {
		return dto.DocumentResponse{}, err
	}
	return documentResponse(doc), nil
}

// List 查询知识库下文档列表
func (s *DocumentService) List(ctx context.Context, userID, kbID string) ([]dto.DocumentResponse, error) {
	if _, err := s.findNormalKnowledgeBase(ctx, userID, kbID); err != nil {
		return nil, err
	}
	items, err := s.documentRepo.ListByKnowledgeBase(ctx, userID, kbID, documentStatusDeleted)
	if err != nil {
		return nil, err
	}
	output := make([]dto.DocumentResponse, 0, len(items))
	for _, item := range items {
		output = append(output, documentResponse(item))
	}
	return output, nil
}

// Detail 查询文档详情
func (s *DocumentService) Detail(ctx context.Context, userID, documentID string) (dto.DocumentResponse, error) {
	doc, err := s.findDocument(ctx, userID, documentID)
	if err != nil {
		return dto.DocumentResponse{}, err
	}
	return documentResponse(doc), nil
}

// Delete 软删除文档
func (s *DocumentService) Delete(ctx context.Context, userID, documentID string) error {
	now := time.Now()
	expiredAt := now.AddDate(0, 0, deleteRetentionDays)
	ok, err := s.documentRepo.SoftDelete(ctx, userID, documentID, documentStatusDeleted, now, expiredAt)
	if err != nil {
		return err
	}
	if !ok {
		return apperrors.NewDefault(apperrors.CodeDocumentNotFound)
	}
	return nil
}

// Process 手动触发文档处理任务
func (s *DocumentService) Process(ctx context.Context, userID, documentID string) (dto.DocumentProcessingJobResponse, error) {
	doc, err := s.findDocument(ctx, userID, documentID)
	if err != nil {
		return dto.DocumentProcessingJobResponse{}, err
	}
	if doc.Status != documentStatusUploaded && doc.Status != documentStatusFailed {
		return dto.DocumentProcessingJobResponse{}, apperrors.NewDefault(apperrors.CodeDocumentStatusInvalid)
	}

	job := entity.DocumentProcessingJob{
		UserID:       userID,
		DocumentID:   documentID,
		JobType:      documentJobTypeProcess,
		Status:       documentJobStatusPending,
		ErrorMessage: "",
	}

	// 1. 创建任务和文档状态变更必须一起完成，避免任务存在但文档仍显示未处理
	ok, err := s.documentJobRepo.CreateProcessJob(ctx, &job, []int16{documentStatusUploaded, documentStatusFailed}, documentStatusProcessing)
	if err != nil {
		return dto.DocumentProcessingJobResponse{}, err
	}
	if !ok {
		return dto.DocumentProcessingJobResponse{}, apperrors.NewDefault(apperrors.CodeDocumentStatusInvalid)
	}
	return documentProcessingJobResponse(job), nil
}

// ListJobs 查询文档处理任务列表
func (s *DocumentService) ListJobs(ctx context.Context, userID, documentID string) ([]dto.DocumentProcessingJobResponse, error) {
	if _, err := s.findDocument(ctx, userID, documentID); err != nil {
		return nil, err
	}
	items, err := s.documentJobRepo.ListByDocument(ctx, userID, documentID)
	if err != nil {
		return nil, err
	}
	output := make([]dto.DocumentProcessingJobResponse, 0, len(items))
	for _, item := range items {
		output = append(output, documentProcessingJobResponse(item))
	}
	return output, nil
}

// JobDetail 查询文档处理任务详情
func (s *DocumentService) JobDetail(ctx context.Context, userID, jobID string) (dto.DocumentProcessingJobResponse, error) {
	job, ok, err := s.documentJobRepo.FindByID(ctx, userID, jobID)
	if err != nil {
		return dto.DocumentProcessingJobResponse{}, err
	}
	if !ok {
		return dto.DocumentProcessingJobResponse{}, apperrors.NewDefault(apperrors.CodeDocumentJobNotFound)
	}
	return documentProcessingJobResponse(job), nil
}

// findWritableKnowledgeBase 查询可上传的本地知识库
func (s *DocumentService) findWritableKnowledgeBase(ctx context.Context, userID, kbID string) (entity.KnowledgeBase, error) {
	kb, err := s.findNormalKnowledgeBase(ctx, userID, kbID)
	if err != nil {
		return entity.KnowledgeBase{}, err
	}
	if kb.SourceType != knowledgeBaseSourceLocal {
		return entity.KnowledgeBase{}, apperrors.NewDefault(apperrors.CodeKnowledgeBaseReadonly)
	}
	return kb, nil
}

// findNormalKnowledgeBase 查询当前用户正常状态的知识库
func (s *DocumentService) findNormalKnowledgeBase(ctx context.Context, userID, kbID string) (entity.KnowledgeBase, error) {
	kb, ok, err := s.knowledgeBaseRepo.FindNormal(ctx, userID, kbID, knowledgeBaseStatusNormal)
	if err != nil {
		return entity.KnowledgeBase{}, err
	}
	if !ok {
		return entity.KnowledgeBase{}, apperrors.NewDefault(apperrors.CodeKnowledgeBaseNotFound)
	}
	return kb, nil
}

// validateFile 校验上传文件类型、大小、同名和配额
func (s *DocumentService) validateFile(ctx context.Context, userID, kbID string, fileHeader *multipart.FileHeader) error {
	if fileHeader.Size <= 0 {
		return apperrors.New(apperrors.CodeBadRequest, "上传文件不能为空")
	}
	if fileHeader.Size > maxDocumentFileSize {
		return apperrors.NewDefault(apperrors.CodeDocumentFileTooLarge)
	}
	if _, ok := allowedDocumentFileTypes[documentFileType(fileHeader.Filename)]; !ok {
		return apperrors.NewDefault(apperrors.CodeDocumentFileTypeInvalid)
	}
	exists, err := s.documentRepo.ExistsFileName(ctx, userID, kbID, filepath.Base(fileHeader.Filename), documentStatusDeleted)
	if err != nil {
		return err
	}
	if exists {
		return apperrors.NewDefault(apperrors.CodeDocumentFileDuplicated)
	}
	quota, ok, err := s.storageQuotaRepo.FindByUserID(ctx, userID)
	if err != nil {
		return err
	}
	maxStorageBytes := defaultMaxStorageBytes
	usedStorageBytes := int64(0)
	if ok {
		maxStorageBytes = quota.MaxStorageBytes
		usedStorageBytes = quota.UsedStorageBytes
	}
	if maxStorageBytes-usedStorageBytes < fileHeader.Size {
		return apperrors.NewDefault(apperrors.CodeStorageQuotaExceeded)
	}
	return nil
}

// saveFile 保存上传文件并返回相对路径和哈希
func (s *DocumentService) saveFile(userID, kbID string, fileHeader *multipart.FileHeader) (string, string, error) {
	file, err := fileHeader.Open()
	if err != nil {
		return "", "", err
	}
	defer file.Close()

	dir := filepath.Join(s.uploadRoot, userID, kbID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", "", err
	}

	// 1. 先写临时文件并同步计算内容哈希
	hasher := sha256.New()
	ext := filepath.Ext(fileHeader.Filename)
	tempFile, err := os.CreateTemp(dir, "upload-*"+ext)
	if err != nil {
		return "", "", err
	}
	tempPath := tempFile.Name()
	if _, err := io.Copy(io.MultiWriter(tempFile, hasher), file); err != nil {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
		return "", "", err
	}
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", "", err
	}

	// 2. 用内容哈希作为最终文件名，便于后续识别重复内容
	fileHash := hex.EncodeToString(hasher.Sum(nil))
	finalPath := filepath.Join(dir, fileHash+ext)
	if err := os.Rename(tempPath, finalPath); err != nil {
		_ = os.Remove(tempPath)
		return "", "", err
	}
	return finalPath, fileHash, nil
}

// findDocument 查询当前用户未删除文档
func (s *DocumentService) findDocument(ctx context.Context, userID, documentID string) (entity.Document, error) {
	doc, ok, err := s.documentRepo.FindByID(ctx, userID, documentID, documentStatusDeleted)
	if err != nil {
		return entity.Document{}, err
	}
	if !ok {
		return entity.Document{}, apperrors.NewDefault(apperrors.CodeDocumentNotFound)
	}
	return doc, nil
}

// documentFileType 提取文档扩展名
func documentFileType(fileName string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
}

// documentResponse 转换文档响应 DTO
func documentResponse(doc entity.Document) dto.DocumentResponse {
	return dto.DocumentResponse{
		ID:              doc.ID,
		KnowledgeBaseID: doc.KnowledgeBaseID,
		Title:           doc.Title,
		FileName:        doc.FileName,
		FileType:        doc.FileType,
		FileSize:        doc.FileSize,
		StoragePath:     doc.StoragePath,
		FileHash:        doc.FileHash,
		SourceType:      doc.SourceType,
		Status:          doc.Status,
		ErrorMessage:    doc.ErrorMessage,
		ReadyAt:         doc.ReadyAt,
		CreatedAt:       doc.CreatedAt,
		UpdatedAt:       doc.UpdatedAt,
		DeletedAt:       doc.DeletedAt,
		DeleteExpiredAt: doc.DeleteExpiredAt,
	}
}

// documentProcessingJobResponse 转换文档处理任务响应 DTO
func documentProcessingJobResponse(job entity.DocumentProcessingJob) dto.DocumentProcessingJobResponse {
	return dto.DocumentProcessingJobResponse{
		ID:           job.ID,
		DocumentID:   job.DocumentID,
		JobType:      job.JobType,
		Status:       job.Status,
		ErrorMessage: job.ErrorMessage,
		StartedAt:    job.StartedAt,
		FinishedAt:   job.FinishedAt,
		CreatedAt:    job.CreatedAt,
		UpdatedAt:    job.UpdatedAt,
	}
}
