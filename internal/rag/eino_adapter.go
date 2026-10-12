package rag

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

// 业务 metadata 固定 key，避免散落成魔法字符串
const (
	metaKnowledgeBaseID  = "knowledge_base_id"
	metaDocumentID       = "document_id"
	metaVersionID        = "version_id"
	metaChunkIndex       = "chunk_index"
	metaTitle            = "title"
	metaUserID           = "user_id"
	metaKnowledgeBaseIDs = "knowledge_base_ids"
)

// MetaTitle 返回 Eino Document metadata 中表示文档标题的 key（供上层格式化上下文块使用）
func MetaTitle() string { return metaTitle }

// MetaDocumentID 返回 Eino Document metadata 中表示文档 ID 的 key
func MetaDocumentID() string { return metaDocumentID }

// MetaKnowledgeBaseID 返回 Eino Document metadata 中表示知识库 ID 的 key
func MetaKnowledgeBaseID() string { return metaKnowledgeBaseID }

// implOptions 是适配器的实现特定选项，通过 retriever.GetImplSpecificOptions 解析
type implOptions struct {
	// KnowledgeBaseIDs 限定检索的知识库 ID 列表，为空表示全量
	KnowledgeBaseIDs []string
	// UserID 附加到检索请求的用户标识（用于后续权限/埋点）
	UserID string
	// KeywordQuery 关键字检索专用的 query。为空时关键字侧回退用公共 query。
	KeywordQuery string
}

// WithKnowledgeBaseIDs 指定检索时的知识库范围。配合 EinoRetrieverAdapter 使用。
func WithKnowledgeBaseIDs(ids []string) retriever.Option {
	return retriever.WrapImplSpecificOptFn(func(o *implOptions) {
		o.KnowledgeBaseIDs = append([]string(nil), ids...)
	})
}

// WithUserID 在检索请求上附加用户标识。配合 EinoRetrieverAdapter 使用。
func WithUserID(uid string) retriever.Option {
	return retriever.WrapImplSpecificOptFn(func(o *implOptions) {
		o.UserID = uid
	})
}

// WithKeywordQuery 为关键字侧单独指定 query，与公共 query（向量侧）分离。
// 典型用法：公共 query 是「原问题 + 最近几轮用户提问」的长 query，关键字侧传
// 「实体回填后的短 query」—— 关键字打分是命中率（分母 = query 词项数），
// 长 query 会把所有候选分数一起压低。
func WithKeywordQuery(q string) retriever.Option {
	return retriever.WrapImplSpecificOptFn(func(o *implOptions) {
		o.KeywordQuery = q
	})
}

// EinoRetrieverAdapter 把项目内自研的 rag.Retriever 包装成 eino 的
// components/retriever.Retriever 接口。现有 HybridRetriever 及其装饰器链
// 等内部检索逻辑完全不变，仅做输入/输出格式对齐，使上游（eino Graph、Agent、
// 可观测性 callback）能按 eino 统一组件标准接入。
type EinoRetrieverAdapter struct {
	inner       Retriever
	defaultTopK int
}

// NewEinoRetrieverAdapter 创建 EinoRetrieverAdapter。
// inner: 业务侧实现的 rag.Retriever（例如 HybridRetriever）。
// defaultTopK: 调用方未通过 retriever.WithTopK 传值时使用的默认返回条数。
func NewEinoRetrieverAdapter(inner Retriever, defaultTopK int) *EinoRetrieverAdapter {
	if defaultTopK <= 0 {
		defaultTopK = 10
	}
	return &EinoRetrieverAdapter{inner: inner, defaultTopK: defaultTopK}
}

// GetType 实现 components.Typer，在 eino DevOps/回调中显示组件类型。
func (a *EinoRetrieverAdapter) GetType() string {
	return "HybridPG"
}

// 保证编译期接口对齐
var _ retriever.Retriever = (*EinoRetrieverAdapter)(nil)
var _ components.Typer = (*EinoRetrieverAdapter)(nil)

// Retrieve 实现 retriever.Retriever。
//
// 【观测性设计】：本方法不自己产出任何观测数据。链路上能看到的 span 全部由
// eino 官方 Langfuse callback 在 Graph 节点级自动产生（compose.AddRetrieverNode
// 会自动调 Graph 级 OnStart/OnEnd）。项目已移除自研 span 树与 attrs 注入，
// 所以这里只做检索本身。
func (a *EinoRetrieverAdapter) Retrieve(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	if a == nil || a.inner == nil {
		return nil, fmt.Errorf("eino retriever adapter: inner retriever is nil")
	}
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}

	defaultTopK := a.defaultTopK
	common := retriever.GetCommonOptions(&retriever.Options{
		TopK: &defaultTopK,
	}, opts...)
	impl := retriever.GetImplSpecificOptions(&implOptions{}, opts...)

	topK := a.defaultTopK
	if common.TopK != nil && *common.TopK > 0 {
		topK = *common.TopK
	}

	bizQuery := Query{
		Question:         query,
		KeywordQuery:     impl.KeywordQuery,
		TopK:             topK,
		KnowledgeBaseIDs: append([]string(nil), impl.KnowledgeBaseIDs...),
		UserID:           impl.UserID,
	}

	result, err := a.inner.Retrieve(ctx, bizQuery)
	if err != nil {
		return nil, fmt.Errorf("eino retriever adapter: inner retrieve failed: %w", err)
	}

	docs := make([]*schema.Document, 0, len(result.Documents))
	for _, d := range result.Documents {
		if common.ScoreThreshold != nil && d.Score < *common.ScoreThreshold {
			continue
		}
		meta := map[string]any{
			metaKnowledgeBaseID: d.KnowledgeBaseID,
			metaDocumentID:      d.DocumentID,
			metaVersionID:       d.VersionID,
			metaChunkIndex:      d.ChunkIndex,
			metaTitle:           d.Title,
		}
		if impl.UserID != "" {
			meta[metaUserID] = impl.UserID
		}
		if len(impl.KnowledgeBaseIDs) > 0 {
			meta[metaKnowledgeBaseIDs] = append([]string(nil), impl.KnowledgeBaseIDs...)
		}
		sd := &schema.Document{
			ID:       d.ID,
			Content:  d.Content,
			MetaData: meta,
		}
		sd.WithScore(d.Score)
		docs = append(docs, sd)
	}

	// --- 观测：自研 attrs 注入已随可观测性模块一并移除，检索结果原样返回 ---
	return docs, nil
}

// shortHash 把长 user_id 取后 8 位，方便在观测字段里区分不同用户，但不暴露原始 ID。
// 不做加密（只是在观测展示上缩短 + 轻度脱敏）。
func shortHash(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return s
	}
	return s[len(s)-8:]
}

// EinoDocToRagDoc 把 eino schema.Document 转回内部 rag.Document，
// 方便在过渡期里某些下游仍吃自研 Document 结构。
func EinoDocToRagDoc(d *schema.Document) Document {
	if d == nil {
		return Document{}
	}
	meta := d.MetaData
	doc := Document{
		ID:      d.ID,
		Content: d.Content,
		Score:   d.Score(),
	}
	if v, ok := meta[metaKnowledgeBaseID].(string); ok {
		doc.KnowledgeBaseID = v
	}
	if v, ok := meta[metaDocumentID].(string); ok {
		doc.DocumentID = v
	}
	if v, ok := meta[metaVersionID].(string); ok {
		doc.VersionID = v
	}
	if v, ok := meta[metaChunkIndex].(int); ok {
		doc.ChunkIndex = v
	}
	if v, ok := meta[metaTitle].(string); ok {
		doc.Title = v
	}
	return doc
}
