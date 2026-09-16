package service

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/rag"
)

// newTestDoc 构造一个带 score 与文档名的 schema.Document，供 buildDocsContextBlock 测试使用。
func newTestDoc(id, title, content string, score float64) *schema.Document {
	d := &schema.Document{
		ID:       id,
		Content:  content,
		MetaData: map[string]any{rag.MetaTitle(): title},
	}
	d.WithScore(score)
	return d
}

func TestBuildDocsContextBlock(t *testing.T) {
	t.Run("空文档返回空串", func(t *testing.T) {
		if got := buildDocsContextBlock(nil, 2000, "cl100k_base"); got != "" {
			t.Fatalf("nil docs 应返回空串，得到 %q", got)
		}
		if got := buildDocsContextBlock([]*schema.Document{}, 2000, ""); got != "" {
			t.Fatalf("空 docs 应返回空串，得到 %q", got)
		}
	})

	t.Run("预算非法与模型名为空时仍返回内容", func(t *testing.T) {
		docs := []*schema.Document{newTestDoc("c1", "文档A", "这是知识库的一段参考内容", 0.9)}
		got := buildDocsContextBlock(docs, -1, "")
		if got == "" {
			t.Fatal("预算为负时应退化到默认预算并返回内容")
		}
		if !strings.Contains(got, "以下是知识库返回的参考资料") {
			t.Fatal("结果应包含参考资料 header")
		}
	})

	t.Run("包含 chunk_id 与文档名", func(t *testing.T) {
		docs := []*schema.Document{newTestDoc("chunk-001", "产品手册", "产品使用说明内容", 0.8)}
		got := buildDocsContextBlock(docs, 3000, "cl100k_base")
		for _, want := range []string{
			"以下是知识库返回的参考资料",
			"chunk_id=chunk-001",
			"文档名：产品手册",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("结果缺少 %q，实际输出：%s", want, got)
			}
		}
	})

	t.Run("高分文档排在前面", func(t *testing.T) {
		low := newTestDoc("low-chunk", "低相关文档", "低相关的内容文本", 0.4)
		high := newTestDoc("high-chunk", "高相关文档", "高相关的内容文本", 0.95)
		// 故意把 low 放在切片前面，验证函数内部按 score 降序重排
		got := buildDocsContextBlock([]*schema.Document{low, high}, 3000, "cl100k_base")

		hi := strings.Index(got, "chunk_id=high-chunk")
		lo := strings.Index(got, "chunk_id=low-chunk")
		if hi < 0 || lo < 0 {
			t.Fatalf("两个 chunk 都应输出，实际输出：%s", got)
		}
		if hi > lo {
			t.Errorf("高分段应排在前面：high=%d low=%d", hi, lo)
		}
	})
}
