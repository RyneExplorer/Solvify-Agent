package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	"solvify-agent/internal/repository"
	"solvify-agent/pkg/logger"
)

type ToolResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

func successResponse(message string, data interface{}) string {
	resp := ToolResponse{Success: true, Message: message, Data: data}
	jsonBytes, _ := json.Marshal(resp)
	return string(jsonBytes)
}

func errorResponse(message string) string {
	resp := ToolResponse{Success: false, Message: message}
	jsonBytes, _ := json.Marshal(resp)
	return string(jsonBytes)
}

// ================ GrepChunksTool ================

type GrepChunksTool struct {
	chunkRepo repository.DocumentChunkRepository
	userID    string
	kbIDs     []string
}

func NewGrepChunksTool(chunkRepo repository.DocumentChunkRepository) *GrepChunksTool {
	return &GrepChunksTool{chunkRepo: chunkRepo}
}

func (t *GrepChunksTool) WithContext(userID string, kbIDs []string) *GrepChunksTool {
	return &GrepChunksTool{
		chunkRepo: t.chunkRepo,
		userID:    userID,
		kbIDs:     kbIDs,
	}
}

func (t *GrepChunksTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	props := jsonschema.NewProperties()
	props.Set("keyword", &jsonschema.Schema{Type: "string", Description: "搜索关键词"})
	props.Set("limit", &jsonschema.Schema{Type: "integer", Description: "返回数量限制，默认10"})
	return &schema.ToolInfo{
		Name: "grep_chunks",
		Desc: "关键词精确匹配搜索文档内容，返回文档ID、标题和匹配片段。当需要精确查找某个关键词在文档中的位置时使用。",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
			Type:       "object",
			Properties: props,
			Required:   []string{"keyword"},
		}),
	}, nil
}

func (t *GrepChunksTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einoTool.Option) (string, error) {
	var params struct {
		Keyword string `json:"keyword"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &params); err != nil {
		return errorResponse(fmt.Sprintf("参数解析失败: %v", err)), nil
	}
	if params.Keyword == "" {
		return errorResponse("keyword 参数不能为空"), nil
	}
	if params.Limit <= 0 {
		params.Limit = 10
	}

	results, err := t.chunkRepo.SearchByKeyword(ctx, t.userID, params.Keyword, params.Limit)
	if err != nil {
		logger.Errorf("grep_chunks 搜索异常: keyword=%q, err=%v", params.Keyword, err)
		return errorResponse(fmt.Sprintf("搜索暂时不可用（%v）", err)), nil
	}

	if len(results) == 0 {
		return successResponse("未找到匹配内容", []interface{}{}), nil
	}

	type GrepResult struct {
		DocumentID string `json:"document_id"`
		Title      string `json:"title"`
		Snippet    string `json:"snippet"`
	}
	var grepResults []GrepResult
	for _, r := range results {
		grepResults = append(grepResults, GrepResult{
			DocumentID: r.DocumentID,
			Title:      r.Title,
			Snippet:    truncateRunes(r.Content, 200),
		})
	}

	return successResponse(fmt.Sprintf("找到 %d 条匹配内容", len(grepResults)), grepResults), nil
}

// ================ GetDocumentInfoTool ================

type GetDocumentInfoTool struct {
	documentRepo repository.DocumentRepository
	userID       string
}

func NewGetDocumentInfoTool(documentRepo repository.DocumentRepository) *GetDocumentInfoTool {
	return &GetDocumentInfoTool{documentRepo: documentRepo}
}

func (t *GetDocumentInfoTool) WithContext(userID string) *GetDocumentInfoTool {
	return &GetDocumentInfoTool{
		documentRepo: t.documentRepo,
		userID:       userID,
	}
}

func (t *GetDocumentInfoTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	props := jsonschema.NewProperties()
	props.Set("document_id", &jsonschema.Schema{Type: "string", Description: "文档ID"})
	return &schema.ToolInfo{
		Name: "get_document_info",
		Desc: "获取文档完整元数据，包括标题、文件名、类型、大小、状态、分块数等。当需要了解某个文档的详细信息时使用。",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
			Type:       "object",
			Properties: props,
			Required:   []string{"document_id"},
		}),
	}, nil
}

func (t *GetDocumentInfoTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einoTool.Option) (string, error) {
	var params struct {
		DocumentID string `json:"document_id"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &params); err != nil {
		return errorResponse(fmt.Sprintf("参数解析失败: %v", err)), nil
	}
	if params.DocumentID == "" {
		return errorResponse("document_id 参数不能为空"), nil
	}

	doc, found, err := t.documentRepo.FindByID(ctx, t.userID, params.DocumentID, 0)
	if err != nil {
		logger.Errorf("get_document_info 查询异常: docID=%q, err=%v", params.DocumentID, err)
		return errorResponse(fmt.Sprintf("查询暂时不可用（%v）", err)), nil
	}
	if !found {
		return errorResponse("未找到该文档"), nil
	}

	statusText := map[int]string{
		1: "已上传",
		2: "处理中",
		3: "就绪",
		4: "失败",
		5: "已删除",
	}[doc.Status]

	type DocumentInfo struct {
		DocumentID   string     `json:"document_id"`
		Title        string     `json:"title"`
		FileName     string     `json:"file_name"`
		FileType     string     `json:"file_type"`
		FileSize     int64      `json:"file_size"`
		Status       string     `json:"status"`
		SourceType   string     `json:"source_type"`
		ReadyAt      *time.Time `json:"ready_at,omitempty"`
		ErrorMessage string     `json:"error_message,omitempty"`
		CreatedAt    time.Time  `json:"created_at"`
	}

	return successResponse("获取文档信息成功", DocumentInfo{
		DocumentID:   doc.ID,
		Title:        doc.Title,
		FileName:     doc.FileName,
		FileType:     doc.FileType,
		FileSize:     doc.FileSize,
		Status:       statusText,
		SourceType:   doc.SourceType,
		ReadyAt:      doc.ReadyAt,
		ErrorMessage: doc.ErrorMessage,
		CreatedAt:    doc.CreatedAt,
	}), nil
}

// ================ ListKnowledgeChunksTool ================

type ListKnowledgeChunksTool struct {
	documentRepo repository.DocumentRepository
	userID       string
	kbIDs        []string
}

func NewListKnowledgeChunksTool(documentRepo repository.DocumentRepository) *ListKnowledgeChunksTool {
	return &ListKnowledgeChunksTool{documentRepo: documentRepo}
}

func (t *ListKnowledgeChunksTool) WithContext(userID string, kbIDs []string) *ListKnowledgeChunksTool {
	return &ListKnowledgeChunksTool{
		documentRepo: t.documentRepo,
		userID:       userID,
		kbIDs:        kbIDs,
	}
}

func (t *ListKnowledgeChunksTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	props := jsonschema.NewProperties()
	props.Set("page", &jsonschema.Schema{Type: "integer", Description: "页码，从1开始"})
	props.Set("page_size", &jsonschema.Schema{Type: "integer", Description: "每页数量，默认20"})
	return &schema.ToolInfo{
		Name: "list_knowledge_chunks",
		Desc: "获取知识库中的文档列表，返回文档ID和标题。当用户问'知识库有哪些文档'或'这个知识库下有哪些文件'时使用。",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
			Type:       "object",
			Properties: props,
		}),
	}, nil
}

func (t *ListKnowledgeChunksTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einoTool.Option) (string, error) {
	var params struct {
		Page     int `json:"page"`
		PageSize int `json:"page_size"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &params); err != nil {
		return errorResponse(fmt.Sprintf("参数解析失败: %v", err)), nil
	}
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 20
	}

	var allDocs []repository.DocumentWithChunkCount
	for _, kbID := range t.kbIDs {
		docs, err := t.documentRepo.ListWithChunkCount(ctx, t.userID, kbID)
		if err != nil {
			logger.Errorf("list_knowledge_chunks 查询异常: kbID=%q, err=%v", kbID, err)
			continue
		}
		allDocs = append(allDocs, docs...)
	}

	if len(allDocs) == 0 {
		return successResponse("知识库中没有文档", []interface{}{}), nil
	}

	startIdx := (params.Page - 1) * params.PageSize
	endIdx := startIdx + params.PageSize
	if startIdx >= len(allDocs) {
		return successResponse("已到最后一页", []interface{}{}), nil
	}
	if endIdx > len(allDocs) {
		endIdx = len(allDocs)
	}

	pagedDocs := allDocs[startIdx:endIdx]

	type DocumentListItem struct {
		DocumentID string `json:"document_id"`
		Title      string `json:"title"`
		FileName   string `json:"file_name"`
		FileType   string `json:"file_type"`
		FileSize   int64  `json:"file_size"`
		ChunkCount int    `json:"chunk_count"`
		Status     string `json:"status"`
	}

	var docs []DocumentListItem
	for _, doc := range pagedDocs {
		docs = append(docs, DocumentListItem{
			DocumentID: doc.ID,
			Title:      doc.Title,
			FileName:   doc.FileName,
			FileType:   doc.FileType,
			FileSize:   doc.FileSize,
			ChunkCount: doc.ChunkCount,
			Status:     doc.StatusText,
		})
	}

	return successResponse(fmt.Sprintf("知识库文档列表（共 %d 个，第 %d 页）", len(allDocs), params.Page), docs), nil
}

// ================ ListKnowledgeBasesTool ================

type ListKnowledgeBasesTool struct {
	kbRepo repository.KnowledgeBaseRepository
	userID string
}

func NewListKnowledgeBasesTool(kbRepo repository.KnowledgeBaseRepository) *ListKnowledgeBasesTool {
	return &ListKnowledgeBasesTool{kbRepo: kbRepo}
}

func (t *ListKnowledgeBasesTool) WithContext(userID string) *ListKnowledgeBasesTool {
	return &ListKnowledgeBasesTool{
		kbRepo: t.kbRepo,
		userID: userID,
	}
}

func (t *ListKnowledgeBasesTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	props := jsonschema.NewProperties()
	props.Set("include_stats", &jsonschema.Schema{Type: "boolean", Description: "是否包含文档数和存储量统计，默认true"})
	return &schema.ToolInfo{
		Name: "list_knowledge_bases",
		Desc: "获取用户的所有知识库列表，返回知识库ID、名称、分类、描述、文档数、存储量。当用户问'有哪些知识库'或'知识库列表'时使用。",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
			Type:       "object",
			Properties: props,
		}),
	}, nil
}

func (t *ListKnowledgeBasesTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einoTool.Option) (string, error) {
	var params struct {
		IncludeStats bool `json:"include_stats"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &params); err != nil {
		params.IncludeStats = true
	}

	kbs, err := t.kbRepo.ListNormal(ctx, t.userID, 1)
	if err != nil {
		logger.Errorf("list_knowledge_bases 查询异常: userID=%q, err=%v", t.userID, err)
		return errorResponse(fmt.Sprintf("查询暂时不可用（%v）", err)), nil
	}

	if len(kbs) == 0 {
		return successResponse("还没有创建知识库", []interface{}{}), nil
	}

	type KBInfo struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		Category    string  `json:"category,omitempty"`
		Description string  `json:"description,omitempty"`
		DocCount    int     `json:"doc_count,omitempty"`
		StorageKB   float64 `json:"storage_kb,omitempty"`
	}

	var kbList []KBInfo
	for _, kb := range kbs {
		item := KBInfo{
			ID:          kb.ID,
			Name:        kb.Name,
			Category:    kb.Category,
			Description: kb.Description,
		}
		if params.IncludeStats {
			docCount, _ := t.kbRepo.CountDocuments(ctx, t.userID, kb.ID, 5)
			storage, _ := t.kbRepo.SumDocumentStorage(ctx, t.userID, kb.ID, 5)
			item.DocCount = int(docCount)
			item.StorageKB = float64(storage) / 1024
		}
		kbList = append(kbList, item)
	}

	return successResponse(fmt.Sprintf("知识库列表（共 %d 个）", len(kbList)), kbList), nil
}
