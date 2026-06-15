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

	"github.com/google/uuid"

	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	apperrors "solvify-agent/pkg/errors"
)

const (
	documentStatusUploaded   = 1
	documentStatusProcessing = 2
	documentStatusReady      = 3
	documentStatusFailed     = 4
	documentStatusDeleted    = 5

	documentSourceUpload = "upload"

	documentJobTypeProcess   = "process"
	documentJobTypeReindex   = "reindex"
	documentJobStatusPending = 1
	documentJobStatusRunning = 2
	documentJobStatusSuccess = 3
	documentJobStatusFailed  = 4

	documentVersionInitialNo = 1

	maxDocumentFileSize = 100 * 1024 * 1024
	defaultUploadRoot   = "data/uploads"
)

var allowedDocumentFileTypes = map[string]struct{}{
	"pdf": {}, "doc": {}, "docx": {}, "txt": {}, "md": {}, "markdown": {}, "html": {},
	"csv": {}, "xls": {}, "xlsx": {}, "ppt": {}, "pptx": {}, "json": {},
	"png": {}, "jpg": {}, "jpeg": {},
}

// documentService 封装文档业务用例实现
type documentService struct {
	knowledgeBaseRepo   repository.KnowledgeBaseRepository
	documentRepo        repository.DocumentRepository
	documentVersionRepo repository.DocumentVersionRepository
	documentJobRepo     repository.DocumentProcessingJobRepository
	storageQuotaRepo    repository.StorageQuotaRepository
	chunkService        DocumentChunkServiceInterface
	uploadRoot          string
}

// NewDocumentServiceWithChunkService 创建指定分块服务的文档业务服务
func NewDocumentServiceWithChunkService(
	knowledgeBaseRepo repository.KnowledgeBaseRepository,
	documentRepo repository.DocumentRepository,
	documentVersionRepo repository.DocumentVersionRepository,
	documentJobRepo repository.DocumentProcessingJobRepository,
	storageQuotaRepo repository.StorageQuotaRepository,
	chunkService DocumentChunkServiceInterface,
	uploadRoot string,
) DocumentServiceInterface {
	return &documentService{
		knowledgeBaseRepo:   knowledgeBaseRepo,
		documentRepo:        documentRepo,
		documentVersionRepo: documentVersionRepo,
		documentJobRepo:     documentJobRepo,
		storageQuotaRepo:    storageQuotaRepo,
		chunkService:        chunkService,
		uploadRoot:          uploadRoot,
	}
}

// Upload 上传文档并自动触发异步处理
func (s *documentService) Upload(ctx context.Context, userID, kbID string, fileHeader *multipart.FileHeader) (dto.UploadDocumentResponse, error) {
	if fileHeader == nil {
		return dto.UploadDocumentResponse{}, apperrors.New(apperrors.CodeBadRequest, "上传文件不能为空")
	}

	// 1. 先校验知识库写入权限和文件业务约束
	kb, err := s.findWritableKnowledgeBase(ctx, userID, kbID)
	if err != nil {
		return dto.UploadDocumentResponse{}, err
	}
	if err := s.validateFile(ctx, userID, kb.ID, fileHeader); err != nil {
		return dto.UploadDocumentResponse{}, err
	}

	// 2. 文件保存成功后再创建文档记录，避免数据库记录指向不存在的文件
	storagePath, fileHash, err := s.saveFile(userID, kb.ID, fileHeader)
	if err != nil {
		return dto.UploadDocumentResponse{}, err
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
		return dto.UploadDocumentResponse{}, err
	}

	// 3. 配额是上传业务的内部副作用，不单独开放写接口
	if err := s.storageQuotaRepo.AddUsedStorage(ctx, userID, defaultMaxStorageBytes, fileHeader.Size); err != nil {
		return dto.UploadDocumentResponse{}, err
	}

	// 4. 上传完成后自动创建处理任务，用户无需再手动触发 process
	job, err := s.createAsyncProcessJob(ctx, doc, []int{documentStatusUploaded})
	if err != nil {
		return dto.UploadDocumentResponse{}, err
	}
	doc.Status = documentStatusProcessing
	return dto.UploadDocumentResponse{
		Document: documentResponse(doc),
		Job:      documentProcessingJobResponse(job),
	}, nil
}

// List 查询知识库下文档列表
func (s *documentService) List(ctx context.Context, userID, kbID string) ([]dto.DocumentResponse, error) {
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
func (s *documentService) Detail(ctx context.Context, userID, documentID string) (dto.DocumentResponse, error) {
	doc, err := s.findDocument(ctx, userID, documentID)
	if err != nil {
		return dto.DocumentResponse{}, err
	}
	return documentResponse(doc), nil
}

// Delete 软删除文档
func (s *documentService) Delete(ctx context.Context, userID, documentID string) error {
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
func (s *documentService) Process(ctx context.Context, userID, documentID string) (dto.DocumentProcessingJobResponse, error) {
	doc, err := s.findDocument(ctx, userID, documentID)
	if err != nil {
		return dto.DocumentProcessingJobResponse{}, err
	}
	if doc.Status != documentStatusUploaded && doc.Status != documentStatusFailed {
		return dto.DocumentProcessingJobResponse{}, apperrors.NewDefault(apperrors.CodeDocumentStatusInvalid)
	}

	job, err := s.createAsyncProcessJob(ctx, doc, []int{documentStatusUploaded, documentStatusFailed})
	if err != nil {
		return dto.DocumentProcessingJobResponse{}, err
	}
	return documentProcessingJobResponse(job), nil
}

// createAsyncProcessJob 创建异步处理任务并启动后台处理
func (s *documentService) createAsyncProcessJob(ctx context.Context, doc entity.Document, allowedDocumentStatuses []int) (entity.DocumentProcessingJob, error) {
	job := entity.DocumentProcessingJob{
		ID:           uuid.NewString(),
		UserID:       doc.UserID,
		DocumentID:   doc.ID,
		JobType:      documentJobTypeProcess,
		Status:       documentJobStatusPending,
		ErrorMessage: "",
	}

	// 1. 创建任务和文档状态变更必须一起完成，避免任务存在但文档仍显示未处理
	ok, err := s.documentJobRepo.CreateProcessJob(ctx, &job, allowedDocumentStatuses, documentStatusProcessing)
	if err != nil {
		return entity.DocumentProcessingJob{}, err
	}
	if !ok {
		return entity.DocumentProcessingJob{}, apperrors.NewDefault(apperrors.CodeDocumentStatusInvalid)
	}

	go s.runProcessJob(doc, job.ID)
	return job, nil
}

// runProcessJob 异步执行文档处理任务
func (s *documentService) runProcessJob(doc entity.Document, jobID string) {
	ctx := context.Background()
	startedAt := time.Now()

	// 1. 先把任务从待处理推进到运行中，避免后台处理状态和任务状态脱节
	ok, err := s.documentJobRepo.MarkRunning(ctx, doc.UserID, jobID, documentJobStatusPending, documentJobStatusRunning, startedAt)
	if err != nil {
		_ = s.documentRepo.MarkProcessFailed(ctx, doc.UserID, doc.ID, jobID, documentStatusFailed, documentJobStatusFailed, "文档处理任务启动失败", time.Now())
		return
	}
	if !ok {
		return
	}

	// 2. 后台处理失败时只更新文档和任务状态，HTTP 请求已提前返回
	if err := s.processDocumentContent(ctx, doc, jobID); err != nil {
		_ = s.documentRepo.MarkProcessFailed(ctx, doc.UserID, doc.ID, jobID, documentStatusFailed, documentJobStatusFailed, err.Error(), time.Now())
	}
}

// ListJobs 查询文档处理任务列表
func (s *documentService) ListJobs(ctx context.Context, userID, documentID string) ([]dto.DocumentProcessingJobResponse, error) {
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
func (s *documentService) JobDetail(ctx context.Context, userID, jobID string) (dto.DocumentProcessingJobResponse, error) {
	job, ok, err := s.documentJobRepo.FindByID(ctx, userID, jobID)
	if err != nil {
		return dto.DocumentProcessingJobResponse{}, err
	}
	if !ok {
		return dto.DocumentProcessingJobResponse{}, apperrors.NewDefault(apperrors.CodeDocumentJobNotFound)
	}
	return documentProcessingJobResponse(job), nil
}

// ListVersions 查询文档版本列表
func (s *documentService) ListVersions(ctx context.Context, userID, documentID string) ([]dto.DocumentVersionListItemResponse, error) {
	if _, err := s.findDocument(ctx, userID, documentID); err != nil {
		return nil, err
	}
	items, err := s.documentVersionRepo.ListByDocument(ctx, userID, documentID)
	if err != nil {
		return nil, err
	}
	output := make([]dto.DocumentVersionListItemResponse, 0, len(items))
	for _, item := range items {
		output = append(output, documentVersionListItemResponse(item))
	}
	return output, nil
}

// VersionDetail 查询文档版本详情
func (s *documentService) VersionDetail(ctx context.Context, userID, documentID, versionID string) (dto.DocumentVersionDetailResponse, error) {
	if _, err := s.findDocument(ctx, userID, documentID); err != nil {
		return dto.DocumentVersionDetailResponse{}, err
	}
	version, ok, err := s.documentVersionRepo.FindByID(ctx, userID, documentID, versionID)
	if err != nil {
		return dto.DocumentVersionDetailResponse{}, err
	}
	if !ok {
		return dto.DocumentVersionDetailResponse{}, apperrors.New(apperrors.CodeDocumentNotFound, "文档版本不存在")
	}
	return documentVersionDetailResponse(version), nil
}

// CreateVersion 保存文档新版本并重建索引
func (s *documentService) CreateVersion(ctx context.Context, userID, documentID string, req requestdto.CreateDocumentVersionRequest) (dto.DocumentProcessingJobResponse, error) {
	doc, err := s.findEditableDocument(ctx, userID, documentID)
	if err != nil {
		return dto.DocumentProcessingJobResponse{}, err
	}
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return dto.DocumentProcessingJobResponse{}, apperrors.New(apperrors.CodeBadRequest, "文档正文不能为空")
	}

	version := entity.DocumentVersion{
		ID:            uuid.NewString(),
		UserID:        doc.UserID,
		DocumentID:    doc.ID,
		Content:       content,
		ContentHash:   hashText(content),
		ChangeSummary: strings.TrimSpace(req.ChangeSummary),
	}
	job, chunks, finishedAt, err := s.buildReindexPayload(ctx, doc, version.ID, content)
	if err != nil {
		return dto.DocumentProcessingJobResponse{}, err
	}

	// 1. 新版本、任务和 chunks 替换必须作为一个数据库结果提交
	if err := s.documentVersionRepo.SaveVersionAndReindex(ctx, doc, &job, &version, chunks, documentStatusReady, documentJobStatusSuccess, finishedAt); err != nil {
		return dto.DocumentProcessingJobResponse{}, err
	}
	job.Status = documentJobStatusSuccess
	job.StartedAt = &finishedAt
	job.FinishedAt = &finishedAt
	return documentProcessingJobResponse(job), nil
}

// Reindex 基于最新版本重建文档分块
func (s *documentService) Reindex(ctx context.Context, userID, documentID string) (dto.DocumentProcessingJobResponse, error) {
	doc, err := s.findEditableDocument(ctx, userID, documentID)
	if err != nil {
		return dto.DocumentProcessingJobResponse{}, err
	}
	version, ok, err := s.documentVersionRepo.FindLatestByDocument(ctx, userID, documentID)
	if err != nil {
		return dto.DocumentProcessingJobResponse{}, err
	}
	if !ok {
		return dto.DocumentProcessingJobResponse{}, apperrors.New(apperrors.CodeDocumentStatusInvalid, "文档版本不存在，无法重新向量化")
	}

	job, chunks, finishedAt, err := s.buildReindexPayload(ctx, doc, version.ID, version.Content)
	if err != nil {
		return dto.DocumentProcessingJobResponse{}, err
	}

	// 1. 手动 reindex 不新增版本，只替换当前文档可检索 chunks
	if err := s.documentVersionRepo.ReindexVersion(ctx, doc, &job, version, chunks, documentStatusReady, documentJobStatusSuccess, finishedAt); err != nil {
		return dto.DocumentProcessingJobResponse{}, err
	}
	job.Status = documentJobStatusSuccess
	job.StartedAt = &finishedAt
	job.FinishedAt = &finishedAt
	return documentProcessingJobResponse(job), nil
}

// processDocumentContent 处理文档正文、版本和分块入库
func (s *documentService) processDocumentContent(ctx context.Context, doc entity.Document, jobID string) error {
	if !s.chunkService.SupportsFileType(doc.FileType) {
		return apperrors.New(apperrors.CodeDocumentStatusInvalid, "当前文件类型暂不支持自动解析")
	}

	// 1. 读取原始文件并解析为正文，当前阶段只支持文本类文件
	contentBytes, err := os.ReadFile(doc.StoragePath)
	if err != nil {
		return apperrors.NewWithErr(apperrors.CodeInternalError, "读取文档文件失败", err)
	}
	content := s.chunkService.NormalizeContent(string(contentBytes), doc.FileType)
	if content == "" {
		return apperrors.New(apperrors.CodeDocumentStatusInvalid, "文档正文为空，无法处理")
	}

	// 2. 基于正文创建首个版本，并按固定窗口切出带 overlap 的 chunks
	contentHash := hashText(content)
	version := entity.DocumentVersion{
		ID:            uuid.NewString(),
		UserID:        doc.UserID,
		DocumentID:    doc.ID,
		VersionNo:     documentVersionInitialNo,
		Content:       content,
		ContentHash:   contentHash,
		ChangeSummary: "首次解析生成版本",
	}
	chunks, err := s.chunkService.BuildChunks(ctx, doc, version.ID, s.chunkService.SplitContent(content))
	if err != nil {
		return err
	}
	if len(chunks) == 0 {
		return apperrors.New(apperrors.CodeDocumentStatusInvalid, "文档分块为空，无法处理")
	}

	// 3. 版本、分块、文档状态和任务状态必须在同一个事务中写入
	finishedAt := time.Now()
	return s.documentRepo.SaveProcessResult(ctx, doc, jobID, &version, chunks, documentStatusReady, documentJobStatusSuccess, finishedAt)
}

// hashText 计算正文哈希
func hashText(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// findWritableKnowledgeBase 查询可上传的本地知识库
func (s *documentService) findWritableKnowledgeBase(ctx context.Context, userID, kbID string) (entity.KnowledgeBase, error) {
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
func (s *documentService) findNormalKnowledgeBase(ctx context.Context, userID, kbID string) (entity.KnowledgeBase, error) {
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
func (s *documentService) validateFile(ctx context.Context, userID, kbID string, fileHeader *multipart.FileHeader) error {
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
func (s *documentService) saveFile(userID, kbID string, fileHeader *multipart.FileHeader) (string, string, error) {
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
func (s *documentService) findDocument(ctx context.Context, userID, documentID string) (entity.Document, error) {
	doc, ok, err := s.documentRepo.FindByID(ctx, userID, documentID, documentStatusDeleted)
	if err != nil {
		return entity.Document{}, err
	}
	if !ok {
		return entity.Document{}, apperrors.NewDefault(apperrors.CodeDocumentNotFound)
	}
	return doc, nil
}

// findEditableDocument 查询允许在线编辑的文档
func (s *documentService) findEditableDocument(ctx context.Context, userID, documentID string) (entity.Document, error) {
	doc, err := s.findDocument(ctx, userID, documentID)
	if err != nil {
		return entity.Document{}, err
	}
	if doc.Status != documentStatusReady {
		return entity.Document{}, apperrors.NewDefault(apperrors.CodeDocumentStatusInvalid)
	}
	if _, err := s.findWritableKnowledgeBase(ctx, userID, doc.KnowledgeBaseID); err != nil {
		return entity.Document{}, err
	}
	return doc, nil
}

// buildReindexPayload 构建 reindex 任务和分块数据
func (s *documentService) buildReindexPayload(ctx context.Context, doc entity.Document, versionID, content string) (entity.DocumentProcessingJob, []entity.DocumentChunk, time.Time, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return entity.DocumentProcessingJob{}, nil, time.Time{}, apperrors.New(apperrors.CodeBadRequest, "文档正文不能为空")
	}

	// 1. 先基于版本正文生成 chunks，失败时不写入任何版本或任务数据
	chunks, err := s.chunkService.BuildChunks(ctx, doc, versionID, s.chunkService.SplitContent(content))
	if err != nil {
		return entity.DocumentProcessingJob{}, nil, time.Time{}, err
	}
	if len(chunks) == 0 {
		return entity.DocumentProcessingJob{}, nil, time.Time{}, apperrors.New(apperrors.CodeDocumentStatusInvalid, "文档分块为空，无法重新向量化")
	}

	// 2. 任务时间使用同一时间点，保持同步处理结果一致
	finishedAt := time.Now()
	job := entity.DocumentProcessingJob{
		ID:           uuid.NewString(),
		UserID:       doc.UserID,
		DocumentID:   doc.ID,
		JobType:      documentJobTypeReindex,
		Status:       documentJobStatusPending,
		ErrorMessage: "",
		StartedAt:    &finishedAt,
		FinishedAt:   &finishedAt,
	}
	return job, chunks, finishedAt, nil
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

// documentVersionListItemResponse 转换文档版本列表响应 DTO
func documentVersionListItemResponse(version entity.DocumentVersion) dto.DocumentVersionListItemResponse {
	return dto.DocumentVersionListItemResponse{
		ID:            version.ID,
		DocumentID:    version.DocumentID,
		VersionNo:     version.VersionNo,
		ContentHash:   version.ContentHash,
		ChangeSummary: version.ChangeSummary,
		CreatedAt:     version.CreatedAt,
	}
}

// documentVersionDetailResponse 转换文档版本详情响应 DTO
func documentVersionDetailResponse(version entity.DocumentVersion) dto.DocumentVersionDetailResponse {
	return dto.DocumentVersionDetailResponse{
		ID:            version.ID,
		DocumentID:    version.DocumentID,
		VersionNo:     version.VersionNo,
		Content:       version.Content,
		ContentHash:   version.ContentHash,
		ChangeSummary: version.ChangeSummary,
		CreatedAt:     version.CreatedAt,
	}
}
