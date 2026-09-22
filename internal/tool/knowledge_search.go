package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	orderedmap "github.com/wk8/go-ordered-map/v2"

	"solvify-agent/internal/rag"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/strutil"
)

// KnowledgeSearchTool 知识库语义搜索工具
// 直接实现 eino tool.InvokableTool 接口
type KnowledgeSearchTool struct {
	retriever rag.Retriever
	userID    string
	kbIDs     []string

	// mu 保护 collectedSources。eino 的 ToolsNode 默认并行执行同一轮内的多个工具调用
	// （compose.ToolsNodeConfig.ExecuteSequentially 默认 false），而模型面对多主题提问
	// 常在一轮里并发发起多个 knowledge_search —— 同一实例被并发写会让 append 丢元素，
	// 甚至损坏切片内部结构，前端引用来源因此缺失或错位（审查报告 P0-4）。
	// 工具实例本身是按请求新建的（app.go 用工厂闭包
	// NewKnowledgeSearchTool(...).WithContext(...) 注册），故不需要跨请求加锁。
	mu sync.Mutex
	// collectedSources 记录本次请求中所有检索命中的来源（Agent 结束后经 Sources 读取）
	collectedSources []SourceDocument
}

// Sources 返回本次请求已收集到的来源快照。
//
// 返回**副本**而非内部切片：调用方拿到后要遍历/按文档分组，若直接交出内部切片，
// 并发写入会让它读到撕裂的中间状态；快照同时保证一次请求内多处读取看到同一份视图。
func (t *KnowledgeSearchTool) Sources() []SourceDocument {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.collectedSources) == 0 {
		return nil
	}
	out := make([]SourceDocument, len(t.collectedSources))
	copy(out, t.collectedSources)
	return out
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
		contentBuilder.WriteString(fmt.Sprintf("[chunk_id=%s] %s: %s\n\n", doc.ID, doc.Title, strutil.Truncate(doc.Content, 150)))
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

	// 记录来源（Agent 结束后经 Sources 读取）。同一轮可能并行进来多个调用，必须加锁。
	t.mu.Lock()
	t.collectedSources = append(t.collectedSources, sources...)
	t.mu.Unlock()

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

// buildProperties 构建单个属性的 JSON Schema ordered map
func buildProperties(name, propType, desc string) *orderedmap.OrderedMap[string, *jsonschema.Schema] {
	props := jsonschema.NewProperties()
	props.Set(name, &jsonschema.Schema{
		Type:        propType,
		Description: desc,
	})
	return props
}
