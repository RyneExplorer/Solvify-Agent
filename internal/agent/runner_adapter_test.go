package agent

import (
	"fmt"
	"reflect"
	"testing"

	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/tool"
)

// ─── 深度模式来源聚合 collectSources 的回归测试 ─────────────────────────────
//
// 旧实现按 Title 分组、且直接遍历 map 输出，导致两个线上可见的问题：
//   1. 不同文档同名时被并成一条来源，chunk 挂到错误的 documentID 上，前端点击引用跳错文档；
//   2. 来源顺序随 map 遍历随机，同一份检索结果每次刷新展示顺序都不同。
// 下面用「同名不同 documentID」和「多次调用顺序稳定」两条不变量把这两个问题钉住。

func docIDs(sources []response.SourceInfo) []string {
	ids := make([]string, 0, len(sources))
	for _, s := range sources {
		ids = append(ids, s.DocumentID)
	}
	return ids
}

// 同名但不同 documentID 的两份文档必须保留为两条来源，chunk 不能串。
func TestCollectSources_SameTitleDifferentDocumentNotMerged(t *testing.T) {
	got := collectSources([]tool.SourceDocument{
		{ID: "c1", DocumentID: "doc-a", KnowledgeBaseID: "kb-1", Title: "附件.pdf", Score: 0.9, Content: "A 的内容"},
		{ID: "c2", DocumentID: "doc-b", KnowledgeBaseID: "kb-1", Title: "附件.pdf", Score: 0.8, Content: "B 的内容"},
	})

	if len(got) != 2 {
		t.Fatalf("同名但不同 documentID 的文档被合并了：期望 2 条来源，实际 %d 条（documentID=%v）", len(got), docIDs(got))
	}
	if got[0].DocumentID != "doc-a" || got[1].DocumentID != "doc-b" {
		t.Fatalf("来源顺序或归属不对：期望 [doc-a doc-b]，实际 %v", docIDs(got))
	}
	if len(got[0].Chunks) != 1 || got[0].Chunks[0].ID != "c1" {
		t.Errorf("doc-a 应只挂 chunk c1，实际 %+v", got[0].Chunks)
	}
	if len(got[1].Chunks) != 1 || got[1].Chunks[0].ID != "c2" {
		t.Errorf("doc-b 应只挂 chunk c2，实际 %+v", got[1].Chunks)
	}
}

// 来源顺序必须等于检索命中顺序，且多次调用完全一致（旧实现遍历 map，顺序随机）。
func TestCollectSources_KeepsRetrievalOrderStably(t *testing.T) {
	const n = 8

	input := make([]tool.SourceDocument, 0, n)
	want := make([]string, 0, n)
	for i := 0; i < n; i++ {
		docID := fmt.Sprintf("doc-%d", i)
		input = append(input, tool.SourceDocument{
			ID:              fmt.Sprintf("c%d", i),
			DocumentID:      docID,
			KnowledgeBaseID: "kb-1",
			Title:           fmt.Sprintf("文档%d", i),
			Score:           1 - float64(i)/10,
			Content:         "内容",
		})
		want = append(want, docID)
	}

	// 跑多轮：单轮的 map 遍历顺序是随机的，只有实现本身有序才能轮轮一致
	for i := 0; i < 200; i++ {
		got := docIDs(collectSources(input))
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("第 %d 次调用来源顺序被打乱：期望 %v，实际 %v", i+1, want, got)
		}
	}
}

// 同一文档的多个 chunk 合并为一条来源，chunk 保持检索顺序，文档级 score 取最高分。
func TestCollectSources_MergesChunksOfSameDocument(t *testing.T) {
	got := collectSources([]tool.SourceDocument{
		{ID: "c1", DocumentID: "doc-a", KnowledgeBaseID: "kb-1", Title: "设计文档", Score: 0.52, Content: "第一段"},
		{ID: "c2", DocumentID: "doc-a", KnowledgeBaseID: "kb-1", Title: "设计文档", Score: 0.91, Content: "第二段"},
		{ID: "c3", DocumentID: "doc-b", KnowledgeBaseID: "kb-2", Title: "部署手册", Score: 0.33, Content: "第三段"},
	})

	if len(got) != 2 {
		t.Fatalf("同一文档的多 chunk 未合并：期望 2 条来源，实际 %d 条（documentID=%v）", len(got), docIDs(got))
	}
	if len(got[0].Chunks) != 2 {
		t.Fatalf("doc-a 应聚合 2 个 chunk，实际 %d 个", len(got[0].Chunks))
	}
	if got[0].Chunks[0].ID != "c1" || got[0].Chunks[1].ID != "c2" {
		t.Errorf("chunk 未保持检索顺序：%s, %s", got[0].Chunks[0].ID, got[0].Chunks[1].ID)
	}
	if got[0].Score != 0.91 {
		t.Errorf("文档级 score 应取最高分 chunk 0.91，实际 %v", got[0].Score)
	}
	if got[0].KnowledgeBaseID != "kb-1" || got[1].KnowledgeBaseID != "kb-2" {
		t.Errorf("knowledge_base_id 归属不对：%q / %q", got[0].KnowledgeBaseID, got[1].KnowledgeBaseID)
	}
	if got[0].Title != "设计文档" || got[1].Title != "部署手册" {
		t.Errorf("title 归属不对：%q / %q", got[0].Title, got[1].Title)
	}
}

// 没有检索命中时不产出来源，避免前端出现空的来源区块。
func TestCollectSources_EmptyInput(t *testing.T) {
	if got := collectSources(nil); got != nil {
		t.Errorf("nil 输入应返回 nil，实际 %v", got)
	}
	if got := collectSources([]tool.SourceDocument{}); got != nil {
		t.Errorf("空切片输入应返回 nil，实际 %v", got)
	}
}
