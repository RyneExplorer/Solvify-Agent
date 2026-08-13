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
	"solvify-agent/pkg/logger"
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
// 注意：即使检索失败也返回字符串结果（而非 Go error），
// 这样 LLM 可以看到失败信息并自行决定如何处理（如用已有知识回答）
func (t *KnowledgeSearchTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einoTool.Option) (string, error) {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &params); err != nil {
		return fmt.Sprintf("参数解析失败: %v", err), nil
	}
	if params.Query == "" {
		return "query 参数不能为空", nil
	}

	result, err := t.retriever.Retrieve(ctx, rag.Query{
		Question:         params.Query,
		TopK:             config.Get().RAG.TopK,
		KnowledgeBaseIDs: t.kbIDs,
		UserID:           t.userID,
	})
	if err != nil {
		logger.Errorf("知识库检索异常: query=%q, err=%v", params.Query, err)
		return fmt.Sprintf("知识库检索暂时不可用（%v），请基于你已有的知识回答用户问题。", err), nil
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
	contentBuilder.WriteString("以上为知识库检索结果，不需要联网搜索来补充。如果这些内容满足用户需求，直接组织答案；如果需要列出文档清单、关键词精准查找等其他操作，可以继续调用知识库内部工具。\n")

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

// truncateRunes 截断字符串到指定 rune 长度
func truncateRunes(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// buildProperties 构建单个属性的 JSON Schema ordered map
func buildProperties(name, propType, desc string) *orderedmap.OrderedMap[string, *jsonschema.Schema] {
	props := jsonschema.NewProperties()
	props.Set(name, &jsonschema.Schema{
		Type:        propType,
		Description: desc,
	})
	return props
}
