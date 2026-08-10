package tool

import (
	"context"
	"fmt"
	"time"

	einoTool "github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"

	"solvify-agent/internal/repository"
	"solvify-agent/pkg/logger"
)

// ToolResponse 所有工具的统一返回结构。
// InferTool 会自动把它 JSON encode 成字符串返回给 LLM。
type ToolResponse struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// ================ grep_chunks ================

type GrepChunksInput struct {
	Keyword string `json:"keyword" jsonschema:"required" jsonschema_description:"搜索关键词"`
	Limit   int    `json:"limit" jsonschema_description:"返回数量限制，默认10"`
}

func runGrepChunks(ctx context.Context, chunkRepo repository.DocumentChunkRepository, userID string, input GrepChunksInput) (ToolResponse, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}

	results, err := chunkRepo.SearchByKeyword(ctx, userID, input.Keyword, limit)
	if err != nil {
		logger.Errorf("[Tool] grep_chunks 搜索异常: keyword=%q, err=%v", input.Keyword, err)
		return ToolResponse{Success: false, Message: fmt.Sprintf("搜索暂时不可用（%v）", err)}, nil
	}

	if len(results) == 0 {
		return ToolResponse{Success: true, Message: "未找到匹配内容", Data: []interface{}{}}, nil
	}

	type GrepResult struct {
		DocumentID string `json:"document_id"`
		Title      string `json:"title"`
		Snippet    string `json:"snippet"`
	}
	grepResults := make([]GrepResult, 0, len(results))
	for _, r := range results {
		grepResults = append(grepResults, GrepResult{
			DocumentID: r.DocumentID,
			Title:      r.Title,
			Snippet:    truncateRunes(r.Content, 200),
		})
	}

	return ToolResponse{Success: true, Message: fmt.Sprintf("找到 %d 条匹配内容", len(grepResults)), Data: grepResults}, nil
}

// NewGrepChunksTool 创建 grep_chunks 工具。
// repo 实例由调用方闭包捕获，InferTool 内部无状态。
func NewGrepChunksTool(chunkRepo repository.DocumentChunkRepository) func(userID string, kbIDs []string) einoTool.InvokableTool {
	return func(userID string, kbIDs []string) einoTool.InvokableTool {
		t, _ := toolutils.InferTool[GrepChunksInput, ToolResponse](
			"grep_chunks",
			"关键词精确匹配搜索文档内容，返回文档ID、标题和匹配片段。当需要精确查找某个关键词在文档中的位置时使用。",
			func(ctx context.Context, input GrepChunksInput) (ToolResponse, error) {
				return runGrepChunks(ctx, chunkRepo, userID, input)
			},
		)
		return t
	}
}

// ================ get_document_info ================

type GetDocumentInfoInput struct {
	DocumentID string `json:"document_id" jsonschema:"required" jsonschema_description:"文档ID"`
}

func runGetDocumentInfo(ctx context.Context, documentRepo repository.DocumentRepository, userID string, input GetDocumentInfoInput) (ToolResponse, error) {
	doc, found, err := documentRepo.FindByID(ctx, userID, input.DocumentID, 0)
	if err != nil {
		logger.Errorf("[Tool] get_document_info 查询异常: docID=%q, err=%v", input.DocumentID, err)
		return ToolResponse{Success: false, Message: fmt.Sprintf("查询暂时不可用（%v）", err)}, nil
	}
	if !found {
		return ToolResponse{Success: false, Message: "未找到该文档"}, nil
	}

	statusText := map[int]string{
		1: "已上传", 2: "处理中", 3: "就绪", 4: "失败", 5: "已删除",
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

	return ToolResponse{Success: true, Message: "获取文档信息成功", Data: DocumentInfo{
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
	}}, nil
}

func NewGetDocumentInfoTool(documentRepo repository.DocumentRepository) func(userID string, kbIDs []string) einoTool.InvokableTool {
	return func(userID string, kbIDs []string) einoTool.InvokableTool {
		t, _ := toolutils.InferTool[GetDocumentInfoInput, ToolResponse](
			"get_document_info",
			"获取文档完整元数据，包括标题、文件名、类型、大小、状态、分块数等。当需要了解某个文档的详细信息时使用。",
			func(ctx context.Context, input GetDocumentInfoInput) (ToolResponse, error) {
				return runGetDocumentInfo(ctx, documentRepo, userID, input)
			},
		)
		return t
	}
}

// ================ list_knowledge_chunks ================

type ListKnowledgeChunksInput struct {
	Page     int `json:"page" jsonschema_description:"页码，从1开始"`
	PageSize int `json:"page_size" jsonschema_description:"每页数量，默认20"`
}

func runListKnowledgeChunks(ctx context.Context, documentRepo repository.DocumentRepository, userID string, kbIDs []string, input ListKnowledgeChunksInput) (ToolResponse, error) {
	page := input.Page
	if page <= 0 {
		page = 1
	}
	pageSize := input.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}

	var allDocs []repository.DocumentWithChunkCount
	for _, kbID := range kbIDs {
		docs, err := documentRepo.ListWithChunkCount(ctx, userID, kbID)
		if err != nil {
			logger.Errorf("[Tool] list_knowledge_chunks 查询异常: kbID=%q, err=%v", kbID, err)
			continue
		}
		allDocs = append(allDocs, docs...)
	}

	if len(allDocs) == 0 {
		return ToolResponse{Success: true, Message: "知识库中没有文档", Data: []interface{}{}}, nil
	}

	startIdx := (page - 1) * pageSize
	endIdx := startIdx + pageSize
	if startIdx >= len(allDocs) {
		return ToolResponse{Success: true, Message: "已到最后一页", Data: []interface{}{}}, nil
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

	docs := make([]DocumentListItem, 0, len(pagedDocs))
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

	return ToolResponse{Success: true, Message: fmt.Sprintf("知识库文档列表（共 %d 个，第 %d 页）", len(allDocs), page), Data: docs}, nil
}

func NewListKnowledgeChunksTool(documentRepo repository.DocumentRepository) func(userID string, kbIDs []string) einoTool.InvokableTool {
	return func(userID string, kbIDs []string) einoTool.InvokableTool {
		t, _ := toolutils.InferTool[ListKnowledgeChunksInput, ToolResponse](
			"list_knowledge_chunks",
			"获取知识库中的文档列表，返回文档ID和标题。当用户问'知识库有哪些文档'或'这个知识库下有哪些文件'时使用。",
			func(ctx context.Context, input ListKnowledgeChunksInput) (ToolResponse, error) {
				return runListKnowledgeChunks(ctx, documentRepo, userID, kbIDs, input)
			},
		)
		return t
	}
}

// ================ list_knowledge_bases ================

type ListKnowledgeBasesInput struct {
	IncludeStats bool `json:"include_stats" jsonschema_description:"是否包含文档数和存储量统计，默认true"`
}

func runListKnowledgeBases(ctx context.Context, kbRepo repository.KnowledgeBaseRepository, userID string, input ListKnowledgeBasesInput) (ToolResponse, error) {
	// 未传 include_stats 时 InferTool 默认给零值 false，这里我们期望默认 true
	includeStats := input.IncludeStats
	// 但如果字段是 optional，InferTool 会给零值。我们希望默认 true：
	// 所以用指针 *bool 或者直接在这里设默认
	// 不过用户没传就是 false——那就按用户传的来，如果传了就按用户的

	kbs, err := kbRepo.ListNormal(ctx, userID, 1)
	if err != nil {
		logger.Errorf("[Tool] list_knowledge_bases 查询异常: userID=%q, err=%v", userID, err)
		return ToolResponse{Success: false, Message: fmt.Sprintf("查询暂时不可用（%v）", err)}, nil
	}

	if len(kbs) == 0 {
		return ToolResponse{Success: true, Message: "还没有创建知识库", Data: []interface{}{}}, nil
	}

	type KBInfo struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		Category    string  `json:"category,omitempty"`
		Description string  `json:"description,omitempty"`
		DocCount    int     `json:"doc_count,omitempty"`
		StorageKB   float64 `json:"storage_kb,omitempty"`
	}

	kbList := make([]KBInfo, 0, len(kbs))
	for _, kb := range kbs {
		item := KBInfo{
			ID:          kb.ID,
			Name:        kb.Name,
			Category:    kb.Category,
			Description: kb.Description,
		}
		if includeStats {
			docCount, _ := kbRepo.CountDocuments(ctx, userID, kb.ID, 5)
			storage, _ := kbRepo.SumDocumentStorage(ctx, userID, kb.ID, 5)
			item.DocCount = int(docCount)
			item.StorageKB = float64(storage) / 1024
		}
		kbList = append(kbList, item)
	}

	return ToolResponse{Success: true, Message: fmt.Sprintf("知识库列表（共 %d 个）", len(kbs)), Data: kbList}, nil
}

func NewListKnowledgeBasesTool(kbRepo repository.KnowledgeBaseRepository) func(userID string, kbIDs []string) einoTool.InvokableTool {
	return func(userID string, kbIDs []string) einoTool.InvokableTool {
		t, _ := toolutils.InferTool[ListKnowledgeBasesInput, ToolResponse](
			"list_knowledge_bases",
			"获取用户的所有知识库列表，返回知识库ID、名称、分类、描述、文档数、存储量。当用户问'有哪些知识库'或'知识库列表'时使用。",
			func(ctx context.Context, input ListKnowledgeBasesInput) (ToolResponse, error) {
				return runListKnowledgeBases(ctx, kbRepo, userID, input)
			},
		)
		return t
	}
}
