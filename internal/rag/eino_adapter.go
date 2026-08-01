package rag

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

// 业务 metadata 固定 key，避免散落成魔法字符串
const (
	metaKnowledgeBaseID = "knowledge_base_id"
	metaDocumentID      = "document_id"
	metaVersionID       = "version_id"
	metaChunkIndex      = "chunk_index"
	metaTitle           = "title"
	metaUserID          = "user_id"
	metaKnowledgeBaseIDs = "knowledge_base_ids"
)

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
	inner      Retriever
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
// 入参 opts 里，标准参数 (TopK/ScoreThreshold/DSLInfo) 与 impl 特定参数
// (KnowledgeBaseIDs/UserID) 会分别解析，内部仍走自研 rag.Retriever 接口。
//
// 因为这是自定义实现，不走 eino 生成的带 callback 注入的 wrapper，所以这里
// 手动调用 callbacks.EnsureRunInfo / OnStart / OnEnd / OnError，让全局
// callback handler 能收到 Retriever 的 span 和指标。
func (a *EinoRetrieverAdapter) Retrieve(ctx context.Context, query string, opts ...retriever.Option) (docs []*schema.Document, retErr error) {
	if a == nil || a.inner == nil {
		return nil, fmt.Errorf("eino retriever adapter: inner retriever is nil")
	}
	// 1) 把 RunInfo 挂到 context（让 callback handler 知道 Component=Retriever）
	ctx = callbacks.EnsureRunInfo(ctx, a.GetType(), components.ComponentOfRetriever)

	defaultTopK := a.defaultTopK
	common := retriever.GetCommonOptions(&retriever.Options{
		TopK: &defaultTopK,
	}, opts...)
	impl := retriever.GetImplSpecificOptions(&implOptions{}, opts...)

	topK := a.defaultTopK
	if common.TopK != nil && *common.TopK > 0 {
		topK = *common.TopK
	}

	// 2) 组装 retriever.CallbackInput 并触发 OnStart
	input := &retriever.CallbackInput{
		Query:          query,
		TopK:           topK,
		ScoreThreshold: common.ScoreThreshold,
		Extra: map[string]any{
			metaKnowledgeBaseIDs: append([]string(nil), impl.KnowledgeBaseIDs...),
			metaUserID:           impl.UserID,
		},
	}
	ctx = callbacks.OnStart(ctx, input)
	defer func() {
		// 4) 出参路径：正常 → OnEnd(docs)；panic/err → OnError
		if retErr != nil {
			_ = callbacks.OnError(ctx, retErr)
			return
		}
		_ = callbacks.OnEnd(ctx, &retriever.CallbackOutput{Docs: docs})
	}()

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

	docs = make([]*schema.Document, 0, len(result.Documents))
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

	return docs, nil
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
