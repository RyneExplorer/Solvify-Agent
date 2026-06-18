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
	"gorm.io/datatypes"

	"solvify-agent/internal/integration/dingtalk"
	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	apperrors "solvify-agent/pkg/errors"
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
}

// syncService 封装同步业务用例实现
type syncService struct {
	knowledgeBaseRepo  repository.KnowledgeBaseRepository
	syncSourceRepo     repository.SyncSourceRepository
	syncJobRepo        repository.SyncJobRepository
	syncedDocumentRepo repository.SyncedDocumentRepository
	chunkService       DocumentChunkServiceInterface
	dingtalkClient     DingTalkWikiClient
	uploadRoot         string
}

type syncSourceConfig struct {
	OperatorUnionID string `json:"operator_union_id"`
	WorkspaceID     string `json:"workspace_id"`
	RootNodeID      string `json:"root_node_id"`
	SyncMode        string `json:"sync_mode"`
}

type syncRunResult struct {
	totalCount   int
	successCount int
	failedCount  int
	errors       []string
}

// NewSyncService 创建同步服务
func NewSyncService(
	knowledgeBaseRepo repository.KnowledgeBaseRepository,
	syncSourceRepo repository.SyncSourceRepository,
	syncJobRepo repository.SyncJobRepository,
	syncedDocumentRepo repository.SyncedDocumentRepository,
	chunkService DocumentChunkServiceInterface,
	dingtalkClient DingTalkWikiClient,
	uploadRoot string,
) SyncServiceInterface {
	return &syncService{
		knowledgeBaseRepo:  knowledgeBaseRepo,
		syncSourceRepo:     syncSourceRepo,
		syncJobRepo:        syncJobRepo,
		syncedDocumentRepo: syncedDocumentRepo,
		chunkService:       chunkService,
		dingtalkClient:     dingtalkClient,
		uploadRoot:         uploadRoot,
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
	cfg, err := s.sourceConfigFromRequest(req.SourceConfig)
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
	cfg, err := s.sourceConfigFromRequest(req.SourceConfig)
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
	return syncSourceResponse(source), nil
}

// DeleteSource 软删除同步源
func (s *syncService) DeleteSource(ctx context.Context, userID, sourceID string) error {
	ok, err := s.syncSourceRepo.SoftDelete(ctx, userID, sourceID, syncSourceStatusNormal, syncSourceStatusDeleted, time.Now())
	if err != nil {
		return err
	}
	if !ok {
		return apperrors.NewDefault(apperrors.CodeSyncSourceNotFound)
	}
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

// runJob 异步执行钉钉同步任务
func (s *syncService) runJob(source entity.SyncSource, jobID string) {
	ctx := context.Background()
	startedAt := time.Now()
	ok, err := s.syncJobRepo.MarkRunning(ctx, source.UserID, jobID, syncJobStatusPending, syncJobStatusRunning, startedAt)
	if err != nil || !ok {
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
	_ = s.syncJobRepo.Finish(ctx, source.UserID, jobID, status, result.totalCount, result.successCount, result.failedCount, errorMessage, finishedAt)
	var lastSyncAt *time.Time
	if status == syncJobStatusSuccess {
		lastSyncAt = &finishedAt
	}
	_ = s.syncSourceRepo.MarkSyncResult(ctx, source.UserID, source.ID, lastSyncAt, errorMessage)
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
			if isDingTalkFileNode(node) {
				result.totalCount++
				if err := s.syncFileNode(ctx, source, cfg, node); err != nil {
					result.failedCount++
					result.errors = append(result.errors, fmt.Sprintf("%s 同步失败: %v", node.Name, err))
					continue
				}
				result.successCount++
				continue
			}
			s.syncNodeChildren(ctx, source, cfg, node.NodeID, result)
		}
		if next == "" {
			return
		}
		nextToken = next
	}
}

// syncFileNode 下载并入库单个钉钉文件节点
func (s *syncService) syncFileNode(ctx context.Context, source entity.SyncSource, cfg syncSourceConfig, node dingtalk.Node) error {
	fileName := filepath.Base(strings.TrimSpace(node.Name))
	fileType := documentFileType(fileName)
	if !s.chunkService.SupportsFileType(fileType) {
		return apperrors.New(apperrors.CodeDocumentFileTypeInvalid, "当前文件类型暂不支持同步解析")
	}
	dentry, err := s.dingtalkClient.QueryDentryID(ctx, cfg.OperatorUnionID, node.NodeID)
	if err != nil {
		return err
	}
	contentBytes, fileHash, err := s.dingtalkClient.DownloadFile(ctx, cfg.OperatorUnionID, dentry.SpaceID, dentry.DentryID)
	if err != nil {
		return err
	}
	content := s.chunkService.NormalizeContent(string(contentBytes), fileType)
	if content == "" {
		return apperrors.New(apperrors.CodeDocumentStatusInvalid, "同步文档正文为空")
	}
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
		Content:       content,
		ContentHash:   hashSyncedText(content),
		ChangeSummary: "钉钉同步生成版本",
	}
	chunks, err := s.chunkService.BuildChunks(ctx, doc, versionID, s.chunkService.SplitContent(content))
	if err != nil {
		return err
	}
	if len(chunks) == 0 {
		return apperrors.New(apperrors.CodeDocumentStatusInvalid, "同步文档分块为空")
	}
	return s.syncedDocumentRepo.SaveSyncedDocument(ctx, doc, &version, chunks, documentStatusReady, time.Now())
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
func (s *syncService) sourceConfigFromRequest(input requestdto.SyncSourceConfigRequest) (syncSourceConfig, error) {
	cfg := syncSourceConfig{
		OperatorUnionID: strings.TrimSpace(input.OperatorUnionID),
		WorkspaceID:     strings.TrimSpace(input.WorkspaceID),
		RootNodeID:      strings.TrimSpace(input.RootNodeID),
		SyncMode:        strings.TrimSpace(input.SyncMode),
	}
	if cfg.SyncMode == "" {
		cfg.SyncMode = syncModeFull
	}
	if cfg.OperatorUnionID == "" || cfg.WorkspaceID == "" || cfg.RootNodeID == "" {
		return syncSourceConfig{}, apperrors.New(apperrors.CodeBadRequest, "钉钉同步配置不完整")
	}
	if cfg.SyncMode != syncModeFull {
		return syncSourceConfig{}, apperrors.New(apperrors.CodeBadRequest, "当前仅支持全量同步")
	}
	return cfg, nil
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
