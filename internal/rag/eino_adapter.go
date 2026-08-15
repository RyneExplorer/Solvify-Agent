package rag

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/observability"
	"solvify-agent/pkg/tokenutil"
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

// EinoRetrieverAdapter 把项目内自研的 rag.Retriever 包装成 eino 的
// components/retriever.Retriever 接口。现有 HybridRetriever/VectorRetriever
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
// 【观测性设计】：不自己开/关 span，完全复用 compose.AddRetrieverNode 自动调的
// Graph 级 OnStart/OnEnd（它负责创建 span、挂 parent、记 duration）。
// 之所以不再手动调 callbacks.OnStart/OnEnd，是因为同一 span 被 EndSpan 两次的话，
// 第二次真实 attrs（hit_n/top_k）会因为 span 已经 Ended 被直接丢弃，
// 导致前端看到 top_k=0 / hit_n=0 / 无 score 预览。
//
// 真实细粒度 attrs（top_k / kb_n / hit_n / avg_score / top_docs_preview）通过
// observability.SetSpanAttrs 直接写 span.Attrs，绕开 Graph 包装的 CallbackInput。
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

	// --- 观测：OnStart 之后、检索开始之前，先写输入侧 attrs ---
	inAttrs := observability.Attrs{"top_k": topK}
	if common.ScoreThreshold != nil {
		inAttrs["score_threshold"] = *common.ScoreThreshold
	}
	if query != "" {
		inAttrs["query"] = query
	}
	if len(impl.KnowledgeBaseIDs) > 0 {
		inAttrs["kb_n"] = len(impl.KnowledgeBaseIDs)
		if preview := joinIDsPreview(impl.KnowledgeBaseIDs, 5); preview != "" {
			inAttrs["kb_ids_preview"] = preview
		}
	}
	if impl.UserID != "" {
		inAttrs["user_id_hash"] = shortHash(impl.UserID)
	}
	observability.SetSpanAttrs(ctx, inAttrs)

	bizQuery := Query{
		Question:         query,
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

	// --- 观测：检索结束、OnEnd 之前，写输出侧 attrs（hit_n / score / top docs 预览）---
	outAttrs := observability.Attrs{}
	observability.FillDocScoreAttrs(outAttrs, docs)
	if len(docs) > 0 {
		if preview := observability.DocsPreview(docs, 3, observability.RecorderFromContext(ctx)); preview != "" {
			outAttrs["top_docs_preview"] = preview
		}
	}
	observability.SetSpanAttrs(ctx, outAttrs)

	return docs, nil
}

// joinIDsPreview 把知识库 ID 列表拼成 "id1,id2 (+3 more)"，避免 attrs 里放太长数组
func joinIDsPreview(ids []string, maxN int) string {
	if len(ids) == 0 {
		return ""
	}
	if maxN <= 0 {
		maxN = 5
	}
	n := maxN
	if len(ids) < n {
		n = len(ids)
	}
	head := strings.Join(ids[:n], ",")
	if len(ids) > n {
		head += fmt.Sprintf(" (+%d more)", len(ids)-n)
	}
	return head
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

// DocsPreviewByScore 同 observability.DocsPreview，保留给 RAG 包内部未来调用（别名）。
// 实际统一走 observability.DocsPreview。
func DocsPreviewByScore(docs []*schema.Document, topN int) string {
	_ = tokenutil.CountTokens // 预占 import 占位，未来按 token 截断预览时启用
	return observability.DocsPreview(docs, topN, nil)
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
