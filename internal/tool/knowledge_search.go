package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"solvify-agent/internal/rag"
	"solvify-agent/pkg/config"
)

// KnowledgeSearchTool 知识库语义搜索工具
type KnowledgeSearchTool struct {
	retriever rag.Retriever
	userID    string
	kbIDs     []string
}

// NewKnowledgeSearchTool 创建知识库搜索工具
func NewKnowledgeSearchTool(retriever rag.Retriever) *KnowledgeSearchTool {
	return &KnowledgeSearchTool{retriever: retriever}
}

// WithContext 设置当前请求上下文（用户ID和知识库ID）
func (t *KnowledgeSearchTool) WithContext(userID string, kbIDs []string) *KnowledgeSearchTool {
	return &KnowledgeSearchTool{
		retriever: t.retriever,
		userID:    userID,
		kbIDs:     kbIDs,
	}
}

func (t *KnowledgeSearchTool) Name() string {
	return "knowledge_search"
}

func (t *KnowledgeSearchTool) Description() string {
	return "语义向量搜索知识库，返回相关文档片段。当需要从知识库中查找信息时使用。"
}

func (t *KnowledgeSearchTool) Parameters() map[string]any {
	return map[string]any{
		"query": map[string]any{
			"type":        "string",
			"description": "搜索查询文本",
			"required":    true,
		},
	}
}

// SearchResult 知识库搜索结果（同时包含内容和来源元数据）
type SearchResult struct {
	Content string           `json:"content"`
	Sources []SourceDocument `json:"sources"`
}

// SourceDocument 来源文档信息
type SourceDocument struct {
	DocumentID      string  `json:"document_id"`
	KnowledgeBaseID string  `json:"knowledge_base_id"`
	Title           string  `json:"title"`
	Score           float64 `json:"score"`
	Content         string  `json:"content"`
}

func (t *KnowledgeSearchTool) StartReport(args string) ProgressReport {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &params); err == nil && params.Query != "" {
		return ProgressReport{Title: "正在检索知识库", Detail: params.Query, Status: "running"}
	}
	return ProgressReport{Title: "正在检索知识库", Status: "running"}
}

func (t *KnowledgeSearchTool) ResultReport(result string, execErr error) ProgressReport {
	if execErr != nil {
		return ProgressReport{Title: "知识库查询失败", Detail: "正在尝试重新获取信息", Status: "error"}
	}
	if strings.Contains(result, "未找到相关内容") {
		return ProgressReport{Title: "未找到相关知识", Detail: "知识库中暂无相关信息", Status: "warning"}
	}
	var sr SearchResult
	if err := json.Unmarshal([]byte(result), &sr); err == nil {
		return ProgressReport{
			Title:  "找到相关资料",
			Detail: fmt.Sprintf("发现%d条相关资料，正在整理...", len(sr.Sources)),
			Status: "success",
		}
	}
	return ProgressReport{Title: "知识库检索完成", Status: "success"}
}

func (t *KnowledgeSearchTool) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if params.Query == "" {
		return "", fmt.Errorf("query 参数不能为空")
	}

	result, err := t.retriever.Retrieve(ctx, rag.Query{
		Question:         params.Query,
		TopK:             config.Get().RAG.TopK,
		KnowledgeBaseIDs: t.kbIDs,
		UserID:           t.userID,
	})
	if err != nil {
		return "", fmt.Errorf("知识库检索失败: %w", err)
	}

	if !result.Hit || len(result.Documents) == 0 {
		return "未找到相关内容", nil
	}

	var contentBuilder strings.Builder
	var sources []SourceDocument
	for i, doc := range result.Documents {
		contentBuilder.WriteString(fmt.Sprintf("[%d] %s\n%s\n\n", i+1, doc.Title, doc.Content))
		sources = append(sources, SourceDocument{
			DocumentID:      doc.DocumentID,
			KnowledgeBaseID: doc.KnowledgeBaseID,
			Title:           doc.Title,
			Score:           doc.Score,
			Content:         doc.Content,
		})
	}

	searchResult := SearchResult{
		Content: contentBuilder.String(),
		Sources: sources,
	}
	data, _ := json.Marshal(searchResult)
	return string(data), nil
}
