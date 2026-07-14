package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/datatypes"

	"solvify-agent/internal/integration/dingtalk"
	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/documentparser"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/logger"
)

const (
	syncSourceStatusNormal   = 1
	syncSourceStatusDisabled = 2
	syncSourceStatusDeleted  = 3

	syncJobStatusPending = 1
	syncJobStatusRunning = 2
	syncJobStatusSuccess = 3
	syncJobStatusFailed  = 4

	syncJobTypeManual = "manual"

	syncPlatformDingTalk = "dingtalk"
	syncModeFull         = "full"

	knowledgeBaseSourceSync = "sync"
	documentSourceSync      = "sync"
)

// DingTalkWikiClient 定义钉钉知识库客户端能力
type DingTalkWikiClient interface {
	ListNodes(ctx context.Context, operatorID, parentNodeID, nextToken string, maxResults int) ([]dingtalk.Node, string, error)
	QueryDentryID(ctx context.Context, operatorID, dentryUUID string) (dingtalk.DentryInfo, error)
	DownloadFile(ctx context.Context, operatorID, spaceID, dentryID string) ([]byte, string, error)
	QueryDocumentBlocks(ctx context.Context, operatorID, documentID string) ([]dingtalk.DocumentBlock, error)
}

// syncService 封装同步业务用例实现
type syncService struct {
	knowledgeBaseRepo   repository.KnowledgeBaseRepository
	syncSourceRepo      repository.SyncSourceRepository
	syncJobRepo         repository.SyncJobRepository
	syncItemRepo        repository.SyncItemRepository
	syncedDocumentRepo  repository.SyncedDocumentRepository
	dingtalkBindingRepo repository.DingTalkBindingRepository
	chunkService        DocumentChunkServiceInterface
	textExtractor       documentparser.TextExtractor
	dingtalkClient      DingTalkWikiClient
	uploadRoot          string
}

type syncSourceConfig struct {
	OperatorUnionID string `json:"operator_union_id"`
	WorkspaceID     string `json:"workspace_id"`
	RootNodeID      string `json:"root_node_id"`
	SyncMode        string `json:"sync_mode"`
}

const (
	syncItemImportStatusPending   = 1
	syncItemImportStatusImporting = 2
	syncItemImportStatusImported  = 3
	syncItemImportStatusFailed    = 4

	syncTaskOverviewLogFile = "sync_task_overview.log"
)

type syncRunResult struct {
	totalCount   int
	successCount int
	failedCount  int
	errors       []string
}

type syncTaskOverviewLog struct {
	Time         string `json:"time"`
	SourceName   string `json:"source_name"`
	Platform     string `json:"platform"`
	Status       string `json:"status"`
	TotalCount   int    `json:"total_count"`
	SuccessCount int    `json:"success_count"`
	FailedCount  int    `json:"failed_count"`
	CostMS       int64  `json:"cost_ms"`
	ErrorSummary string `json:"error_summary,omitempty"`
}

// NewSyncService 创建同步服务
func NewSyncService(
	knowledgeBaseRepo repository.KnowledgeBaseRepository,
	syncSourceRepo repository.SyncSourceRepository,
	syncJobRepo repository.SyncJobRepository,
	syncItemRepo repository.SyncItemRepository,
	syncedDocumentRepo repository.SyncedDocumentRepository,
	dingtalkBindingRepo repository.DingTalkBindingRepository,
	chunkService DocumentChunkServiceInterface,
	textExtractor documentparser.TextExtractor,
	dingtalkClient DingTalkWikiClient,
	uploadRoot string,
) SyncServiceInterface {
	return &syncService{
		knowledgeBaseRepo:   knowledgeBaseRepo,
		syncSourceRepo:      syncSourceRepo,
		syncJobRepo:         syncJobRepo,
		syncItemRepo:        syncItemRepo,
		syncedDocumentRepo:  syncedDocumentRepo,
		dingtalkBindingRepo: dingtalkBindingRepo,
		chunkService:        chunkService,
		textExtractor:       textExtractor,
		dingtalkClient:      dingtalkClient,
		uploadRoot:          uploadRoot,
	}
}

// CreateSource 创建钉钉同步源
func (s *syncService) CreateSource(ctx context.Context, userID string, req requestdto.CreateSyncSourceRequest) (dto.SyncSourceResponse, error) {
	if strings.TrimSpace(req.Platform) != syncPlatformDingTalk {
		return dto.SyncSourceResponse{}, apperrors.New(apperrors.CodeBadRequest, "同步平台仅支持钉钉")
	}
	if _, err := s.findNormalKnowledgeBase(ctx, userID, req.KnowledgeBaseID); err != nil {
		return dto.SyncSourceResponse{}, err
	}
	cfg, err := s.sourceConfigFromRequest(ctx, userID, req.SourceConfig)
	if err != nil {
		return dto.SyncSourceResponse{}, err
	}
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return dto.SyncSourceResponse{}, apperrors.NewWithErr(apperrors.CodeBadRequest, "同步配置编码失败", err)
	}
	source := entity.SyncSource{
		ID:              uuid.NewString(),
		UserID:          userID,
		KnowledgeBaseID: strings.TrimSpace(req.KnowledgeBaseID),
		Name:            strings.TrimSpace(req.Name),
		Platform:        syncPlatformDingTalk,
		SourceConfig:    datatypes.JSON(configJSON),
		Status:          syncSourceStatusNormal,
	}
	if err := s.syncSourceRepo.Create(ctx, &source, knowledgeBaseSourceSync, syncPlatformDingTalk); err != nil {
		return dto.SyncSourceResponse{}, err
	}
	logger.Info("同步源创建成功",
		zap.String("name", source.Name),
		zap.String("platform", source.Platform),
		zap.Int("status", source.Status),
	)
	return syncSourceResponse(source), nil
}

// ListSources 查询同步源列表
func (s *syncService) ListSources(ctx context.Context, userID string) ([]dto.SyncSourceResponse, error) {
	items, err := s.syncSourceRepo.List(ctx, userID, syncSourceStatusDeleted)
	if err != nil {
		return nil, err
	}
	output := make([]dto.SyncSourceResponse, 0, len(items))
	for _, item := range items {
		output = append(output, syncSourceResponse(item))
	}
	return output, nil
}

// SourceDetail 查询同步源详情
func (s *syncService) SourceDetail(ctx context.Context, userID, sourceID string) (dto.SyncSourceResponse, error) {
	source, err := s.findSource(ctx, userID, sourceID)
	if err != nil {
		return dto.SyncSourceResponse{}, err
	}
	return syncSourceResponse(source), nil
}

// UpdateSource 更新同步源配置
func (s *syncService) UpdateSource(ctx context.Context, userID, sourceID string, req requestdto.UpdateSyncSourceRequest) (dto.SyncSourceResponse, error) {
	source, err := s.findSource(ctx, userID, sourceID)
	if err != nil {
		return dto.SyncSourceResponse{}, err
	}
	cfg, err := s.sourceConfigFromRequest(ctx, userID, req.SourceConfig)
	if err != nil {
		return dto.SyncSourceResponse{}, err
	}
	status := req.Status
	if status == 0 {
		status = source.Status
	}
	if status != syncSourceStatusNormal && status != syncSourceStatusDisabled {
		return dto.SyncSourceResponse{}, apperrors.New(apperrors.CodeBadRequest, "同步源状态不支持")
	}
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return dto.SyncSourceResponse{}, apperrors.NewWithErr(apperrors.CodeBadRequest, "同步配置编码失败", err)
	}
	source.Name = strings.TrimSpace(req.Name)
	source.SourceConfig = datatypes.JSON(configJSON)
	source.Status = status
	ok, err := s.syncSourceRepo.Update(ctx, source, syncSourceStatusDeleted)
	if err != nil {
		return dto.SyncSourceResponse{}, err
	}
	if !ok {
		return dto.SyncSourceResponse{}, apperrors.NewDefault(apperrors.CodeSyncSourceNotFound)
	}
	logger.Info("同步源更新成功",
		zap.String("name", source.Name),
		zap.String("platform", source.Platform),
		zap.Int("status", source.Status),
	)
	return syncSourceResponse(source), nil
}

// DeleteSource 软删除同步源
func (s *syncService) DeleteSource(ctx context.Context, userID, sourceID string) error {
	source, err := s.findSource(ctx, userID, sourceID)
	if err != nil {
		return err
	}
	ok, err := s.syncSourceRepo.SoftDelete(ctx, userID, sourceID, syncSourceStatusNormal, syncSourceStatusDeleted, time.Now())
	if err != nil {
		return err
	}
	if !ok {
		return apperrors.NewDefault(apperrors.CodeSyncSourceNotFound)
	}
	logger.Info("同步源软删除成功",
		zap.String("name", source.Name),
		zap.String("platform", source.Platform),
	)
	return nil
}

// CreateJob 创建异步同步任务
func (s *syncService) CreateJob(ctx context.Context, userID, sourceID string) (dto.SyncJobResponse, error) {
	source, err := s.findSource(ctx, userID, sourceID)
	if err != nil {
		return dto.SyncJobResponse{}, err
	}
	if source.Status != syncSourceStatusNormal {
		return dto.SyncJobResponse{}, apperrors.NewDefault(apperrors.CodeSyncSourceStatusInvalid)
	}
	job := entity.SyncJob{
		ID:              uuid.NewString(),
		UserID:          userID,
		SyncSourceID:    source.ID,
		KnowledgeBaseID: source.KnowledgeBaseID,
		JobType:         syncJobTypeManual,
		Status:          syncJobStatusPending,
	}
	if err := s.syncJobRepo.Create(ctx, &job); err != nil {
		return dto.SyncJobResponse{}, err
	}
	logger.Info("同步任务已创建",
		zap.String("source_name", source.Name),
		zap.String("platform", source.Platform),
	)
	go s.runJob(source, job.ID)
	return syncJobResponse(job), nil
}

// ListJobs 查询同步任务列表
func (s *syncService) ListJobs(ctx context.Context, userID, sourceID string) ([]dto.SyncJobResponse, error) {
	if _, err := s.findSource(ctx, userID, sourceID); err != nil {
		return nil, err
	}
	items, err := s.syncJobRepo.ListBySource(ctx, userID, sourceID)
	if err != nil {
		return nil, err
	}
	output := make([]dto.SyncJobResponse, 0, len(items))
	for _, item := range items {
		output = append(output, syncJobResponse(item))
	}
	return output, nil
}

// JobDetail 查询同步任务详情
func (s *syncService) JobDetail(ctx context.Context, userID, jobID string) (dto.SyncJobResponse, error) {
	job, ok, err := s.syncJobRepo.FindByID(ctx, userID, jobID)
	if err != nil {
		return dto.SyncJobResponse{}, err
	}
	if !ok {
		return dto.SyncJobResponse{}, apperrors.NewDefault(apperrors.CodeSyncJobNotFound)
	}
	return syncJobResponse(job), nil
}

// ListItems 查询同步源下外部文件目录项
func (s *syncService) ListItems(ctx context.Context, userID, sourceID string) ([]dto.SyncItemResponse, error) {
	if _, err := s.findSource(ctx, userID, sourceID); err != nil {
		return nil, err
	}
	if err := s.syncItemRepo.ResetDeletedDocumentLinks(ctx, userID, sourceID, syncItemImportStatusPending, documentStatusDeleted); err != nil {
		return nil, err
	}
	items, err := s.syncItemRepo.ListBySource(ctx, userID, sourceID)
	if err != nil {
		return nil, err
	}
	output := make([]dto.SyncItemResponse, 0, len(items))
	for _, item := range items {
		output = append(output, syncItemResponse(item))
	}
	return output, nil
}

// ImportItem 导入外部同步文件到本地文档
func (s *syncService) ImportItem(ctx context.Context, userID, itemID string) (dto.DocumentResponse, error) {
	item, ok, err := s.syncItemRepo.FindByID(ctx, userID, itemID)
	if err != nil {
		return dto.DocumentResponse{}, err
	}
	if !ok {
		return dto.DocumentResponse{}, apperrors.New(apperrors.CodeNotFound, "同步文件不存在")
	}
	if !strings.EqualFold(item.ItemType, "FILE") {
		return dto.DocumentResponse{}, apperrors.New(apperrors.CodeBadRequest, "目录不能导入为本地文档")
	}
	if isAliSheetItem(item) {
		return dto.DocumentResponse{}, apperrors.New(apperrors.CodeDocumentFileTypeInvalid, "钉钉在线表格暂不支持自动导入，请查看原文")
	}
	source, err := s.findSource(ctx, userID, item.SyncSourceID)
	if err != nil {
		return dto.DocumentResponse{}, err
	}
	cfg, err := sourceConfigFromJSON(source.SourceConfig)
	if err != nil {
		return dto.DocumentResponse{}, err
	}
	ok, err = s.syncItemRepo.MarkImporting(ctx, userID, item.ID, syncItemImportStatusImporting)
	if err != nil {
		return dto.DocumentResponse{}, err
	}
	if !ok {
		return dto.DocumentResponse{}, apperrors.New(apperrors.CodeNotFound, "同步文件不存在")
	}
	doc, err := s.importFileItem(ctx, source, cfg, item)
	if err != nil {
		_ = s.syncItemRepo.MarkImportFailed(ctx, userID, item.ID, syncItemImportStatusFailed, err.Error())
		return dto.DocumentResponse{}, err
	}
	if doc.Status != documentStatusReady {
		errorMessage := strings.TrimSpace(doc.ErrorMessage)
		if errorMessage == "" {
			errorMessage = "钉钉文件解析失败"
		}
		if err := s.syncItemRepo.MarkImportFailed(ctx, userID, item.ID, syncItemImportStatusFailed, errorMessage); err != nil {
			return dto.DocumentResponse{}, err
		}
		return documentResponse(doc), nil
	}
	if err := s.syncItemRepo.MarkImported(ctx, userID, item.ID, doc.ID, syncItemImportStatusImported); err != nil {
		return dto.DocumentResponse{}, err
	}
	return documentResponse(doc), nil
}

// runJob 异步执行钉钉同步任务
func (s *syncService) runJob(source entity.SyncSource, jobID string) {
	ctx := context.Background()
	startedAt := time.Now()
	logger.Info("同步任务开始",
		zap.String("source_name", source.Name),
		zap.String("platform", source.Platform),
	)
	ok, err := s.syncJobRepo.MarkRunning(ctx, source.UserID, jobID, syncJobStatusPending, syncJobStatusRunning, startedAt)
	if err != nil {
		logger.Error("同步任务启动失败",
			zap.String("source_name", source.Name),
			zap.String("platform", source.Platform),
			zap.Duration("cost", time.Since(startedAt)),
			zap.Error(err),
		)
		return
	}
	if !ok {
		logger.Warn("同步任务状态已变化，跳过执行",
			zap.String("source_name", source.Name),
			zap.String("platform", source.Platform),
			zap.Duration("cost", time.Since(startedAt)),
		)
		return
	}
	result := s.syncDingTalkSource(ctx, source)
	finishedAt := time.Now()
	status := syncJobStatusSuccess
	errorMessage := ""
	if len(result.errors) > 0 {
		status = syncJobStatusFailed
		errorMessage = strings.Join(result.errors, "；")
	}
	if len([]rune(errorMessage)) > 1000 {
		errorMessage = string([]rune(errorMessage)[:1000])
	}
	if err := s.syncJobRepo.Finish(ctx, source.UserID, jobID, status, result.totalCount, result.successCount, result.failedCount, errorMessage, finishedAt); err != nil {
		logger.Error("同步任务结果入库失败",
			zap.String("source_name", source.Name),
			zap.String("platform", source.Platform),
			zap.Error(err),
		)
	}
	var lastSyncAt *time.Time
	if status == syncJobStatusSuccess {
		lastSyncAt = &finishedAt
	}
	if err := s.syncSourceRepo.MarkSyncResult(ctx, source.UserID, source.ID, lastSyncAt, errorMessage); err != nil {
		logger.Error("同步源结果更新失败",
			zap.String("source_name", source.Name),
			zap.String("platform", source.Platform),
			zap.Error(err),
		)
	}
	cost := time.Since(startedAt)
	logger.Info("同步任务数据总览",
		zap.String("source_name", source.Name),
		zap.String("platform", source.Platform),
		zap.String("status", syncJobStatusText(status)),
		zap.Int("total_count", result.totalCount),
		zap.Int("success_count", result.successCount),
		zap.Int("failed_count", result.failedCount),
		zap.Duration("cost", cost),
		zap.String("error_summary", errorMessage),
	)
	if err := writeSyncTaskOverviewLog(source, status, result, errorMessage, cost, finishedAt); err != nil {
		logger.Warn("同步任务总览文件写入失败", zap.Error(err))
	}
}

// syncDingTalkSource 同步钉钉知识库节点
func (s *syncService) syncDingTalkSource(ctx context.Context, source entity.SyncSource) syncRunResult {
	cfg, err := sourceConfigFromJSON(source.SourceConfig)
	if err != nil {
		return syncRunResult{failedCount: 1, errors: []string{err.Error()}}
	}
	if s.dingtalkClient == nil {
		return syncRunResult{failedCount: 1, errors: []string{"钉钉客户端未初始化"}}
	}
	result := syncRunResult{}
	s.syncNodeChildren(ctx, source, cfg, cfg.RootNodeID, &result)
	return result
}

// syncNodeChildren 递归同步钉钉目录节点
func (s *syncService) syncNodeChildren(ctx context.Context, source entity.SyncSource, cfg syncSourceConfig, parentNodeID string, result *syncRunResult) {
	nextToken := ""
	for {
		nodes, next, err := s.dingtalkClient.ListNodes(ctx, cfg.OperatorUnionID, parentNodeID, nextToken, 50)
		if err != nil {
			result.failedCount++
			result.errors = append(result.errors, fmt.Sprintf("获取节点列表失败: %v", err))
			return
		}
		for _, node := range nodes {
			result.totalCount++
			if err := s.saveSyncItem(ctx, source, node, parentNodeID); err != nil {
				result.failedCount++
				result.errors = append(result.errors, fmt.Sprintf("%s 元数据刷新失败: %v", node.Name, err))
				continue
			}
			result.successCount++
			if isDingTalkFolderNode(node) && node.HasChildren {
				s.syncNodeChildren(ctx, source, cfg, node.NodeID, result)
			}
		}
		if next == "" {
			return
		}
		nextToken = next
	}
}

// saveSyncItem 保存钉钉节点元数据
func (s *syncService) saveSyncItem(ctx context.Context, source entity.SyncSource, node dingtalk.Node, parentNodeID string) error {
	item := entity.SyncItem{
		ID:               uuid.NewString(),
		UserID:           source.UserID,
		SyncSourceID:     source.ID,
		KnowledgeBaseID:  source.KnowledgeBaseID,
		ExternalID:       strings.TrimSpace(node.NodeID),
		ParentExternalID: strings.TrimSpace(parentNodeID),
		Name:             strings.TrimSpace(node.Name),
		ItemType:         strings.TrimSpace(node.Type),
		Category:         strings.TrimSpace(node.Category),
		Extension:        strings.ToLower(strings.TrimSpace(node.Extension)),
		ExternalURL:      strings.TrimSpace(node.URL),
		FileSize:         node.Size,
		HasChildren:      node.HasChildren,
		SourceUpdatedAt:  sourceUpdatedAt(node.ModifiedAt),
		ImportStatus:     syncItemImportStatusPending,
		ErrorMessage:     "",
	}
	return s.syncItemRepo.Upsert(ctx, item)
}

// syncFileNode 下载并入库单个钉钉文件节点
func (s *syncService) syncFileNode(ctx context.Context, source entity.SyncSource, cfg syncSourceConfig, node dingtalk.Node) (err error) {
	startedAt := time.Now()
	fileName := filepath.Base(strings.TrimSpace(node.Name))
	fileType := documentFileType(fileName)
	if fileType == "" {
		fileType = strings.ToLower(strings.TrimSpace(node.Extension))
	}
	logger.Info("钉钉同步文件处理开始", zap.String("file_name", fileName),
		zap.String("file_type", fileType),
	)
	defer func() {
		if err == nil {
			return
		}
		logger.Error("钉钉同步文件处理失败", zap.String("file_name", fileName),
			zap.String("file_type", fileType),
			zap.Duration("cost", time.Since(startedAt)),
			zap.Error(err),
		)
	}()
	if s.textExtractor == nil || !s.textExtractor.Supports(fileType) {
		logger.Warn("钉钉同步文件暂不支持自动解析，保存占位文档", zap.String("file_name", fileName),
			zap.String("file_type", fileType),
		)
		return s.saveUnsupportedFileNode(ctx, source, node, fileName, fileType)
	}
	dentry, err := s.dingtalkClient.QueryDentryID(ctx, cfg.OperatorUnionID, node.NodeID)
	if err != nil {
		return err
	}
	contentBytes, fileHash, err := s.dingtalkClient.DownloadFile(ctx, cfg.OperatorUnionID, dentry.SpaceID, dentry.DentryID)
	if err != nil {
		return err
	}
	logger.Info("钉钉同步文件下载完成", zap.String("file_name", fileName),
		zap.String("file_type", fileType),
		zap.Int("file_size", len(contentBytes)),
	)
	extractStartedAt := time.Now()
	rawContent, err := s.textExtractor.ExtractBytes(ctx, contentBytes, fileType)
	if err != nil {
		return err
	}
	displayContent := normalizeDocumentDisplayContent(rawContent)
	chunkContent := s.chunkService.NormalizeContent(displayContent, fileType)
	if displayContent == "" || chunkContent == "" {
		return apperrors.New(apperrors.CodeDocumentStatusInvalid, "同步文档正文为空")
	}
	logger.Info("钉钉同步文件正文提取完成", zap.String("file_type", fileType),
		zap.Int("raw_content_bytes", len(rawContent)),
		zap.Int("content_bytes", len(displayContent)),
		zap.Duration("cost", time.Since(extractStartedAt)),
	)
	doc, found, err := s.syncedDocumentRepo.FindByExternalID(ctx, source.UserID, documentSourceSync, node.NodeID, documentStatusDeleted)
	if err != nil {
		return err
	}
	if !found {
		doc.ID = uuid.NewString()
	}
	doc.UserID = source.UserID
	doc.KnowledgeBaseID = source.KnowledgeBaseID
	doc.Title = strings.TrimSuffix(fileName, filepath.Ext(fileName))
	doc.FileName = fileName
	doc.FileType = fileType
	doc.FileSize = int64(len(contentBytes))
	doc.FileHash = fileHash
	doc.SourceType = documentSourceSync
	doc.ExternalID = node.NodeID
	doc.ExternalURL = node.URL
	doc.SourceUpdatedAt = sourceUpdatedAt(node.ModifiedAt)
	doc.Status = documentStatusReady
	doc.ErrorMessage = ""
	storagePath, err := s.saveSyncedFile(source.UserID, source.KnowledgeBaseID, fileName, contentBytes)
	if err != nil {
		return err
	}
	doc.StoragePath = storagePath

	versionID := uuid.NewString()
	version := entity.DocumentVersion{
		ID:            versionID,
		UserID:        source.UserID,
		DocumentID:    doc.ID,
		Content:       displayContent,
		ContentHash:   hashSyncedText(displayContent),
		ChangeSummary: "钉钉同步生成版本",
	}
	chunkStartedAt := time.Now()
	contents := s.chunkService.SplitContent(chunkContent)
	chunks, err := s.chunkService.BuildChunks(ctx, doc, versionID, contents)
	if err != nil {
		return err
	}
	if len(chunks) == 0 {
		return apperrors.New(apperrors.CodeDocumentStatusInvalid, "同步文档分块为空")
	}
	logger.Info("钉钉同步文件分块构建完成", zap.Int("chunk_count", len(chunks)),
		zap.Duration("cost", time.Since(chunkStartedAt)),
	)
	if err := s.syncedDocumentRepo.SaveSyncedDocument(ctx, doc, &version, chunks, documentStatusReady, time.Now()); err != nil {
		return err
	}
	logger.Info("钉钉同步文件处理成功", zap.Duration("cost", time.Since(startedAt)))
	return nil
}

// importFileItem 导入单个外部文件目录项
func (s *syncService) importFileItem(ctx context.Context, source entity.SyncSource, cfg syncSourceConfig, item entity.SyncItem) (doc entity.Document, err error) {
	startedAt := time.Now()
	fileName := syncItemFileName(item)
	fileType := syncItemFileType(item, fileName)
	logger.Info("钉钉文件导入开始", zap.String("file_name", fileName),
		zap.String("file_type", fileType),
	)
	defer func() {
		if err == nil {
			return
		}
		logger.Error("钉钉文件导入失败", zap.String("file_name", fileName),
			zap.String("file_type", fileType),
			zap.Duration("cost", time.Since(startedAt)),
			zap.Error(err),
		)
	}()
	if isAliDocItem(item) {
		return s.importAliDocItem(ctx, source, cfg, item, fileName)
	}
	if s.textExtractor == nil || !s.textExtractor.Supports(fileType) {
		logger.Warn("钉钉文件暂不支持自动解析，保存失败占位文档", zap.String("file_name", fileName),
			zap.String("file_type", fileType),
		)
		doc := syncedDocumentFromItem(source, item, fileName, fileType, documentStatusFailed, fmt.Sprintf("%s 文件暂不支持自动解析，可通过钉钉原文链接查看", strings.ToUpper(fileType)))
		if err := s.applyExistingDocumentID(ctx, &doc); err != nil {
			return entity.Document{}, err
		}
		return doc, s.syncedDocumentRepo.SaveSyncedPlaceholder(ctx, doc, documentStatusDeleted)
	}
	dentry, err := s.dingtalkClient.QueryDentryID(ctx, cfg.OperatorUnionID, item.ExternalID)
	if err != nil {
		return entity.Document{}, err
	}
	contentBytes, fileHash, err := s.dingtalkClient.DownloadFile(ctx, cfg.OperatorUnionID, dentry.SpaceID, dentry.DentryID)
	if err != nil {
		return entity.Document{}, err
	}
	logger.Info("钉钉文件下载完成", zap.String("file_name", fileName),
		zap.String("file_type", fileType),
		zap.Int("file_size", len(contentBytes)),
	)
	extractStartedAt := time.Now()
	rawContent, err := s.textExtractor.ExtractBytes(ctx, contentBytes, fileType)
	if err != nil {
		return entity.Document{}, err
	}
	displayContent := normalizeDocumentDisplayContent(rawContent)
	chunkContent := s.chunkService.NormalizeContent(displayContent, fileType)
	if displayContent == "" || chunkContent == "" {
		return entity.Document{}, apperrors.New(apperrors.CodeDocumentStatusInvalid, "同步文档正文为空")
	}
	logger.Info("钉钉文件正文提取完成", zap.String("file_type", fileType),
		zap.Int("raw_content_bytes", len(rawContent)),
		zap.Int("content_bytes", len(displayContent)),
		zap.Duration("cost", time.Since(extractStartedAt)),
	)
	doc = syncedDocumentFromItem(source, item, fileName, fileType, documentStatusReady, "")
	if err := s.applyExistingDocumentID(ctx, &doc); err != nil {
		return entity.Document{}, err
	}
	doc.FileSize = int64(len(contentBytes))
	doc.FileHash = fileHash
	storagePath, err := s.saveSyncedFile(source.UserID, source.KnowledgeBaseID, fileName, contentBytes)
	if err != nil {
		return entity.Document{}, err
	}
	doc.StoragePath = storagePath

	versionID := uuid.NewString()
	version := entity.DocumentVersion{
		ID:            versionID,
		UserID:        source.UserID,
		DocumentID:    doc.ID,
		Content:       displayContent,
		ContentHash:   hashSyncedText(displayContent),
		ChangeSummary: "钉钉导入生成版本",
	}
	chunkStartedAt := time.Now()
	contents := s.chunkService.SplitContent(chunkContent)
	chunks, err := s.chunkService.BuildChunks(ctx, doc, versionID, contents)
	if err != nil {
		return entity.Document{}, err
	}
	if len(chunks) == 0 {
		return entity.Document{}, apperrors.New(apperrors.CodeDocumentStatusInvalid, "同步文档分块为空")
	}
	logger.Info("钉钉文件分块构建完成", zap.Int("chunk_count", len(chunks)),
		zap.Duration("cost", time.Since(chunkStartedAt)),
	)
	if err := s.syncedDocumentRepo.SaveSyncedDocument(ctx, doc, &version, chunks, documentStatusReady, time.Now()); err != nil {
		return entity.Document{}, err
	}
	logger.Info("钉钉文件导入成功", zap.Duration("cost", time.Since(startedAt)))
	return doc, nil
}

// importAliDocItem 导入钉钉在线文档
func (s *syncService) importAliDocItem(ctx context.Context, source entity.SyncSource, cfg syncSourceConfig, item entity.SyncItem, fileName string) (entity.Document, error) {
	blocks, err := s.dingtalkClient.QueryDocumentBlocks(ctx, cfg.OperatorUnionID, item.ExternalID)
	if err != nil {
		return entity.Document{}, err
	}
	displayContent := dingtalk.DocumentBlocksToMarkdown(blocks)
	displayContent = normalizeDocumentDisplayContent(displayContent)
	chunkContent := s.chunkService.NormalizeContent(displayContent, "md")
	if displayContent == "" || chunkContent == "" {
		return entity.Document{}, apperrors.New(apperrors.CodeDocumentStatusInvalid, "钉钉在线文档正文为空")
	}

	doc := syncedDocumentFromItem(source, item, fileName, "adoc", documentStatusReady, "")
	if err := s.applyExistingDocumentID(ctx, &doc); err != nil {
		return entity.Document{}, err
	}
	doc.FileSize = 0
	doc.FileHash = hashSyncedText(displayContent)
	doc.StoragePath = ""
	versionID := uuid.NewString()
	version := entity.DocumentVersion{
		ID:            versionID,
		UserID:        source.UserID,
		DocumentID:    doc.ID,
		Content:       displayContent,
		ContentHash:   hashSyncedText(displayContent),
		ChangeSummary: "钉钉在线文档块元素导入生成版本",
	}
	chunks, err := s.chunkService.BuildChunks(ctx, doc, versionID, s.chunkService.SplitContent(chunkContent))
	if err != nil {
		return entity.Document{}, err
	}
	if len(chunks) == 0 {
		return entity.Document{}, apperrors.New(apperrors.CodeDocumentStatusInvalid, "钉钉在线文档分块为空")
	}
	if err := s.syncedDocumentRepo.SaveSyncedDocument(ctx, doc, &version, chunks, documentStatusReady, time.Now()); err != nil {
		return entity.Document{}, err
	}
	logger.Info("钉钉在线文档块元素导入成功", zap.String("file_name", fileName))
	return doc, nil
}

// applyExistingDocumentID 复用已有外部文档 ID
func (s *syncService) applyExistingDocumentID(ctx context.Context, doc *entity.Document) error {
	current, found, err := s.syncedDocumentRepo.FindByExternalID(ctx, doc.UserID, doc.SourceType, doc.ExternalID, documentStatusDeleted)
	if err != nil {
		return err
	}
	if found {
		doc.ID = current.ID
	}
	return nil
}

// saveUnsupportedFileNode 保存暂不支持解析的钉钉文件元数据
func (s *syncService) saveUnsupportedFileNode(ctx context.Context, source entity.SyncSource, node dingtalk.Node, fileName, fileType string) error {
	if fileName == "." || strings.TrimSpace(fileName) == "" {
		fileName = strings.TrimSpace(node.Name)
	}
	if fileType == "" {
		fileType = "unknown"
	}
	doc := entity.Document{
		ID:              uuid.NewString(),
		UserID:          source.UserID,
		KnowledgeBaseID: source.KnowledgeBaseID,
		Title:           strings.TrimSuffix(fileName, filepath.Ext(fileName)),
		FileName:        fileName,
		FileType:        fileType,
		FileSize:        node.Size,
		FileHash:        hashSyncedText(node.NodeID + ":" + fmt.Sprintf("%d", node.ModifiedAt)),
		SourceType:      documentSourceSync,
		ExternalID:      node.NodeID,
		ExternalURL:     node.URL,
		SourceUpdatedAt: sourceUpdatedAt(node.ModifiedAt),
		Status:          documentStatusFailed,
		ErrorMessage:    fmt.Sprintf("%s 文件暂不支持自动解析，可通过钉钉原文链接查看", strings.ToUpper(fileType)),
	}
	return s.syncedDocumentRepo.SaveSyncedPlaceholder(ctx, doc, documentStatusDeleted)
}

// findSource 查询当前用户同步源
func (s *syncService) findSource(ctx context.Context, userID, sourceID string) (entity.SyncSource, error) {
	source, ok, err := s.syncSourceRepo.FindByID(ctx, userID, sourceID, syncSourceStatusDeleted)
	if err != nil {
		return entity.SyncSource{}, err
	}
	if !ok {
		return entity.SyncSource{}, apperrors.NewDefault(apperrors.CodeSyncSourceNotFound)
	}
	return source, nil
}

// findNormalKnowledgeBase 查询当前用户正常知识库
func (s *syncService) findNormalKnowledgeBase(ctx context.Context, userID, kbID string) (entity.KnowledgeBase, error) {
	kb, ok, err := s.knowledgeBaseRepo.FindNormal(ctx, userID, kbID, knowledgeBaseStatusNormal)
	if err != nil {
		return entity.KnowledgeBase{}, err
	}
	if !ok {
		return entity.KnowledgeBase{}, apperrors.NewDefault(apperrors.CodeKnowledgeBaseNotFound)
	}
	return kb, nil
}

// sourceConfigFromRequest 规整同步配置
func (s *syncService) sourceConfigFromRequest(ctx context.Context, userID string, input requestdto.SyncSourceConfigRequest) (syncSourceConfig, error) {
	cfg := syncSourceConfig{
		OperatorUnionID: "",
		WorkspaceID:     strings.TrimSpace(input.WorkspaceID),
		RootNodeID:      strings.TrimSpace(input.RootNodeID),
		SyncMode:        strings.TrimSpace(input.SyncMode),
	}
	if cfg.SyncMode == "" {
		cfg.SyncMode = syncModeFull
	}
	operatorUnionID, err := s.dingtalkOperatorUnionID(ctx, userID)
	if err != nil {
		return syncSourceConfig{}, err
	}
	cfg.OperatorUnionID = operatorUnionID
	if cfg.WorkspaceID == "" || cfg.RootNodeID == "" {
		return syncSourceConfig{}, apperrors.New(apperrors.CodeBadRequest, "钉钉同步配置不完整")
	}
	if cfg.SyncMode != syncModeFull {
		return syncSourceConfig{}, apperrors.New(apperrors.CodeBadRequest, "当前仅支持全量同步")
	}
	return cfg, nil
}

// dingtalkOperatorUnionID 读取当前用户绑定的钉钉 unionId
func (s *syncService) dingtalkOperatorUnionID(ctx context.Context, userID string) (string, error) {
	binding, ok, err := s.dingtalkBindingRepo.FindByUserID(ctx, userID)
	if err != nil {
		return "", err
	}
	if !ok || strings.TrimSpace(binding.DingUnionID) == "" {
		return "", apperrors.New(apperrors.CodeBadRequest, "请先绑定钉钉账号")
	}
	return strings.TrimSpace(binding.DingUnionID), nil
}

// sourceConfigFromJSON 解析同步配置
func sourceConfigFromJSON(data datatypes.JSON) (syncSourceConfig, error) {
	var cfg syncSourceConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return syncSourceConfig{}, apperrors.NewWithErr(apperrors.CodeBadRequest, "同步配置解析失败", err)
	}
	if cfg.OperatorUnionID == "" || cfg.WorkspaceID == "" || cfg.RootNodeID == "" {
		return syncSourceConfig{}, apperrors.New(apperrors.CodeBadRequest, "钉钉同步配置不完整")
	}
	return cfg, nil
}

// saveSyncedFile 保存同步下载文件
func (s *syncService) saveSyncedFile(userID, kbID, fileName string, content []byte) (string, error) {
	dir := filepath.Join(s.uploadRoot, userID, kbID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	ext := filepath.Ext(fileName)
	finalPath := filepath.Join(dir, hex.EncodeToString(sum[:])+ext)
	if err := os.WriteFile(finalPath, content, 0644); err != nil {
		return "", err
	}
	return finalPath, nil
}

// isDingTalkFileNode 判断钉钉节点是否为文件
func isDingTalkFileNode(node dingtalk.Node) bool {
	return strings.EqualFold(strings.TrimSpace(node.Type), "FILE")
}

// isDingTalkFolderNode 判断钉钉节点是否为目录
func isDingTalkFolderNode(node dingtalk.Node) bool {
	return strings.EqualFold(strings.TrimSpace(node.Type), "FOLDER")
}

// isAliDocItem 判断是否为钉钉在线文档
func isAliDocItem(item entity.SyncItem) bool {
	if !strings.EqualFold(strings.TrimSpace(item.Category), "ALIDOC") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(item.Extension), "adoc")
}

// isAliSheetItem 判断是否为钉钉在线表格
func isAliSheetItem(item entity.SyncItem) bool {
	if !strings.EqualFold(strings.TrimSpace(item.Category), "ALIDOC") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(item.Extension), "axls")
}

// syncItemFileName 规整外部文件名
func syncItemFileName(item entity.SyncItem) string {
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = strings.TrimSpace(item.ExternalID)
	}
	if filepath.Ext(name) == "" && strings.TrimSpace(item.Extension) != "" {
		name = name + "." + strings.ToLower(strings.TrimSpace(item.Extension))
	}
	return filepath.Base(name)
}

// syncItemFileType 规整外部文件类型
func syncItemFileType(item entity.SyncItem, fileName string) string {
	fileType := documentFileType(fileName)
	if fileType != "" {
		return fileType
	}
	fileType = strings.ToLower(strings.TrimSpace(item.Extension))
	if fileType == "" {
		return "unknown"
	}
	return fileType
}

// syncedDocumentFromItem 从外部目录项构造本地文档
func syncedDocumentFromItem(source entity.SyncSource, item entity.SyncItem, fileName, fileType string, status int, errorMessage string) entity.Document {
	return entity.Document{
		ID:              uuid.NewString(),
		UserID:          source.UserID,
		KnowledgeBaseID: source.KnowledgeBaseID,
		Title:           strings.TrimSuffix(fileName, filepath.Ext(fileName)),
		FileName:        fileName,
		FileType:        fileType,
		FileSize:        item.FileSize,
		FileHash:        hashSyncedText(item.ExternalID + ":" + fmt.Sprintf("%d", item.FileSize)),
		SourceType:      documentSourceSync,
		ExternalID:      item.ExternalID,
		ExternalURL:     item.ExternalURL,
		SourceUpdatedAt: item.SourceUpdatedAt,
		Status:          status,
		ErrorMessage:    errorMessage,
	}
}

// sourceUpdatedAt 转换钉钉更新时间
func sourceUpdatedAt(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	var t time.Time
	if value > 1_000_000_000_000 {
		t = time.UnixMilli(value)
	} else {
		t = time.Unix(value, 0)
	}
	return &t
}

// hashSyncedText 计算同步正文哈希
func hashSyncedText(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// writeSyncTaskOverviewLog 追加写入同步任务总览日志文件
func writeSyncTaskOverviewLog(source entity.SyncSource, status int, result syncRunResult, errorMessage string, cost time.Duration, finishedAt time.Time) error {
	if err := os.MkdirAll("logs", 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join("logs", syncTaskOverviewLogFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	payload := syncTaskOverviewLog{
		Time:         finishedAt.Format(time.RFC3339),
		SourceName:   source.Name,
		Platform:     source.Platform,
		Status:       syncJobStatusText(status),
		TotalCount:   result.totalCount,
		SuccessCount: result.successCount,
		FailedCount:  result.failedCount,
		CostMS:       cost.Milliseconds(),
		ErrorSummary: errorMessage,
	}
	return json.NewEncoder(file).Encode(payload)
}

// syncJobStatusText 转换同步任务状态文案
func syncJobStatusText(status int) string {
	switch status {
	case syncJobStatusPending:
		return "待同步"
	case syncJobStatusRunning:
		return "同步中"
	case syncJobStatusSuccess:
		return "成功"
	case syncJobStatusFailed:
		return "失败"
	default:
		return "未知"
	}
}

// syncSourceResponse 转换同步源响应
func syncSourceResponse(source entity.SyncSource) dto.SyncSourceResponse {
	cfg, _ := sourceConfigFromJSON(source.SourceConfig)
	return dto.SyncSourceResponse{
		ID:              source.ID,
		KnowledgeBaseID: source.KnowledgeBaseID,
		Name:            source.Name,
		Platform:        source.Platform,
		SourceConfig: dto.SyncSourceConfigResponse{
			OperatorUnionID: cfg.OperatorUnionID,
			WorkspaceID:     cfg.WorkspaceID,
			RootNodeID:      cfg.RootNodeID,
			SyncMode:        cfg.SyncMode,
		},
		Status:           source.Status,
		LastSyncAt:       source.LastSyncAt,
		LastErrorMessage: source.LastErrorMessage,
		CreatedAt:        source.CreatedAt,
		UpdatedAt:        source.UpdatedAt,
		DeletedAt:        source.DeletedAt,
	}
}

// syncJobResponse 转换同步任务响应
func syncJobResponse(job entity.SyncJob) dto.SyncJobResponse {
	return dto.SyncJobResponse{
		ID:              job.ID,
		SyncSourceID:    job.SyncSourceID,
		KnowledgeBaseID: job.KnowledgeBaseID,
		JobType:         job.JobType,
		Status:          job.Status,
		TotalCount:      job.TotalCount,
		SuccessCount:    job.SuccessCount,
		FailedCount:     job.FailedCount,
		ErrorMessage:    job.ErrorMessage,
		StartedAt:       job.StartedAt,
		FinishedAt:      job.FinishedAt,
		CreatedAt:       job.CreatedAt,
		UpdatedAt:       job.UpdatedAt,
	}
}

// syncItemResponse 转换外部同步文件目录项响应
func syncItemResponse(item entity.SyncItem) dto.SyncItemResponse {
	localDocumentID := ""
	if item.LocalDocumentID != nil {
		localDocumentID = *item.LocalDocumentID
	}
	return dto.SyncItemResponse{
		ID:               item.ID,
		SyncSourceID:     item.SyncSourceID,
		KnowledgeBaseID:  item.KnowledgeBaseID,
		ExternalID:       item.ExternalID,
		ParentExternalID: item.ParentExternalID,
		Name:             item.Name,
		ItemType:         item.ItemType,
		Category:         item.Category,
		Extension:        item.Extension,
		ExternalURL:      item.ExternalURL,
		FileSize:         item.FileSize,
		HasChildren:      item.HasChildren,
		SourceUpdatedAt:  item.SourceUpdatedAt,
		LocalDocumentID:  localDocumentID,
		ImportStatus:     item.ImportStatus,
		ErrorMessage:     item.ErrorMessage,
		CreatedAt:        item.CreatedAt,
		UpdatedAt:        item.UpdatedAt,
	}
}
