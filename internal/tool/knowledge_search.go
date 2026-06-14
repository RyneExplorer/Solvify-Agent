package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	orderedmap "github.com/wk8/go-ordered-map/v2"

	"solvify-agent/internal/rag"
	"solvify-agent/pkg/config"
)

// KnowledgeSearchTool 知识库语义搜索工具
// 直接实现 eino tool.InvokableTool 接口
type KnowledgeSearchTool struct {
	retriever rag.Retriever
	userID    string
	kbIDs     []string

	// CollectedSources 记录本次请求中所有检索命中的来源（Agent 结束后读取）
	CollectedSources []SourceDocument
	// SearchCount 记录搜索次数
	SearchCount int
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

// Info 返回工具元数据，供 ChatModel 决定何时调用
func (t *KnowledgeSearchTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "knowledge_search",
		Desc: "语义向量搜索知识库，返回相关文档片段。当需要从知识库中查找信息时使用。",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
			Type:       "object",
			Properties: buildProperties("query", "string", "搜索查询文本"),
			Required:   []string{"query"},
		}),
	}, nil
}

// InvokableRun 执行知识库搜索
func (t *KnowledgeSearchTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einoTool.Option) (string, error) {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &params); err != nil {
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
	contentBuilder.WriteString("根据以下参考资料回答。\n")
	contentBuilder.WriteString("回答时必须在句末插入引用标签，格式为 <kb doc=\"文档名\" chunk_id=\"真实ID\" />。\n")
	contentBuilder.WriteString("【禁止】把以下原文复制到回答中。\n\n")
	for _, doc := range result.Documents {
		contentBuilder.WriteString(fmt.Sprintf("[chunk_id=%s] %s: %s\n\n", doc.ID, doc.Title, truncateRunes(doc.Content, 150)))
		sources = append(sources, SourceDocument{
			ID:              doc.ID,
			DocumentID:      doc.DocumentID,
			KnowledgeBaseID: doc.KnowledgeBaseID,
			Title:           doc.Title,
			Score:           doc.Score,
			Content:         doc.Content,
		})
	}

	// 记录来源（Agent 结束后从 CollectedSources 读取）
	t.CollectedSources = append(t.CollectedSources, sources...)
	t.SearchCount++

	searchResult := SearchResult{
		Content: contentBuilder.String(),
		Sources: sources,
	}
	data, _ := json.Marshal(searchResult)
	return string(data), nil
}

// SearchResult 知识库搜索结果
type SearchResult struct {
	Content string           `json:"content"`
	Sources []SourceDocument `json:"sources"`
}

// SourceDocument 来源文档信息
type SourceDocument struct {
	ID              string  `json:"id"` // chunk_id，如 chunk_17
	DocumentID      string  `json:"document_id"`
	KnowledgeBaseID string  `json:"knowledge_base_id"`
	Title           string  `json:"title"`
	Score           float64 `json:"score"`
	Content         string  `json:"content"`
}

// buildProperties 构建单参数的 JSON Schema Properties
func buildProperties(name, typ, desc string) *orderedmap.OrderedMap[string, *jsonschema.Schema] {
	p := orderedmap.New[string, *jsonschema.Schema]()
	p.Set(name, &jsonschema.Schema{Type: typ, Description: desc})
	return p
}

// truncateRunes 截断字符串到指定 rune 长度
func truncateRunes(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
