package rag

import (
	"fmt"
	"strings"
	"testing"

	"solvify-agent/internal/model/entity"
)

// 这一组测试覆盖「检索的输入边界 + 可见性出口」四个缺陷：
//
//  1. topK 兜底形同虚设：Retrieve 里算了一遍，两条 search 却各自重算 `query.TopK * 2`
//     → 调用方漏传 TopK 就是 `LIMIT 0` 恒空。
//  2. `dc.embedding IS NOT NULL` 被从向量检索抄进关键词 SQL → 向量化失败的 chunk
//     在关键词侧永久不可见（而关键词侧正是它们唯一的通道）。
//  3. 两条 SQL 都没排除软删文档（documents.status = 5）→ 删掉的文档继续出现在回答里。
//  4. KeywordScoreThreshold 没有配置入口，线上恒为硬编码默认值。
//
// ⚠️ 本包没有 DB 测试基建（现有测试都是纯函数），所以第 3 条的 SQL **文本**级断言
// 只能证明「这句写进 SQL 了」，不能证明语义正确。行为证据由 test1/ 下的一次性真库
// 探针给出（造一个软删文档 + chunk，确认检索不再返回它）。
// 这里另外用 `entity.DocumentStatusDeleted != 0` 兜住最坏情况：若该常量缺失退化成 0，
// `status = 0` 不匹配任何行 → 过滤条件会**静默变成 no-op**，比不写还危险。

func TestEffectiveTopK(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{"未传 TopK 走兜底", 0, defaultTopK},
		{"负数也走兜底", -3, defaultTopK},
		{"正常值原样返回", 3, 3},
		{"最小有效值", 1, 1},
		{"大值不截断", 100, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Query{TopK: tt.in}).effectiveTopK(); got != tt.want {
				t.Errorf("effectiveTopK(TopK=%d)=%d 期望 %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestCandidateLimit(t *testing.T) {
	if got := (Query{TopK: 3}).candidateLimit(); got != 3*candidateMultiplier {
		t.Errorf("candidateLimit(TopK=3)=%d 期望 %d", got, 3*candidateMultiplier)
	}
	// 兜底必须发生在乘系数**之前**，否则 0*2 仍是 0。
	if got := (Query{TopK: 0}).candidateLimit(); got != defaultTopK*candidateMultiplier {
		t.Errorf("candidateLimit(TopK=0)=%d 期望 %d（兜底没生效？）", got, defaultTopK*candidateMultiplier)
	}
}

// TestSearchArgsCarryEffectiveLimit 是本组里唯一能守住**调用点**的测试。
//
// 只测 effectiveTopK 是不够的：把 vectorSearch 里的 `query.candidateLimit()` 改回
// `query.TopK * 2`，方法级测试照样全绿，而线上漏传 TopK 时 LIMIT 又变成 0。
func TestSearchArgsCarryEffectiveLimit(t *testing.T) {
	lastInt := func(t *testing.T, name string, args []any) int {
		t.Helper()
		if len(args) == 0 {
			t.Fatalf("%s 参数为空", name)
		}
		v, ok := args[len(args)-1].(int)
		if !ok {
			t.Fatalf("%s 末位参数应为 int（LIMIT），实际 %T", name, args[len(args)-1])
		}
		return v
	}

	t.Run("向量检索", func(t *testing.T) {
		args := vectorSearchArgs(Query{TopK: 3, KnowledgeBaseIDs: []string{"kb-1"}, UserID: "u-1"}, "VEC")
		if len(args) != 5 {
			t.Errorf("vectorSearchArgs 参数个数=%d 期望 5（与 SQL 里 ? 的个数一致）", len(args))
		}
		// 对齐 vectorSearchSQL 里 ? 的出现顺序：
		//   ?1 score 的 dc.embedding <=> ?::vector   ?2 knowledge_base_id IN (?)
		//   ?3 user_id = ?                           ?4 ORDER BY dc.embedding <=> ?::vector
		//   ?5 LIMIT ?
		// ⚠️ ?1 与 ?4 是同一个向量串，内联时最容易只改一处 —— 那会让"选的"和"排序的"不是同一个向量。
		if args[0] != "VEC" || args[3] != "VEC" {
			t.Errorf("两个向量占位符必须传同一个向量: args[0]=%v args[3]=%v", args[0], args[3])
		}
		if args[2] != "u-1" {
			t.Errorf("第 3 个参数应是 user_id，实际 %v", args[2])
		}
		if got := lastInt(t, "vectorSearchArgs", args); got != 6 {
			t.Errorf("LIMIT=%d 期望 6", got)
		}
		// 回归点：漏传 TopK 时必须是兜底值，不能是 0
		args = vectorSearchArgs(Query{}, "VEC")
		if got := lastInt(t, "vectorSearchArgs", args); got != defaultTopK*candidateMultiplier {
			t.Errorf("漏传 TopK 时 LIMIT=%d 期望 %d（退化回 LIMIT 0 恒空）", got, defaultTopK*candidateMultiplier)
		}
	})

	t.Run("关键词检索", func(t *testing.T) {
		args := keywordSearchArgs(Query{TopK: 3, KnowledgeBaseIDs: []string{"kb-1"}, UserID: "u-1"}, "{a,b}")
		if len(args) != 6 {
			t.Errorf("keywordSearchArgs 参数个数=%d 期望 6（与 SQL 里 ? 的个数一致）", len(args))
		}
		// 逐个对齐 keywordSearchSQL 里 ? 的出现顺序 —— 位置参数一旦错位，
		// 轻则查不到、重则把 user_id 当知识库 id 用，且只有跑真库才会暴露。
		//   ?1 打分分母 cardinality(?::text[])   ?2 ANY(?::text[])
		//   ?3 knowledge_base_id IN (?)          ?4 keywords && ?::text[]
		//   ?5 user_id = ?                       ?6 LIMIT ?
		want := []any{"{a,b}", "{a,b}", []string{"kb-1"}, "{a,b}", "u-1", 6}
		for i, w := range want {
			got := args[i]
			if s, ok := w.([]string); ok {
				gs, ok2 := got.([]string)
				if !ok2 || len(gs) != len(s) || (len(s) > 0 && gs[0] != s[0]) {
					t.Errorf("keywordSearchArgs 第 %d 个参数=%v 期望 %v", i+1, got, w)
				}
				continue
			}
			if got != w {
				t.Errorf("keywordSearchArgs 第 %d 个参数=%v 期望 %v", i+1, got, w)
			}
		}
		// 回归点：漏传 TopK 时末位必须是兜底值，不能是 0
		args = keywordSearchArgs(Query{}, "{a,b}")
		if got := lastInt(t, "keywordSearchArgs", args); got != defaultTopK*candidateMultiplier {
			t.Errorf("漏传 TopK 时 LIMIT=%d 期望 %d", got, defaultTopK*candidateMultiplier)
		}
	})
}

// TestAllChunkReadSQLsCarryVisibilityBoundary 断言**每一个**读取 chunk 内容的检索 SQL
// 都拼上了可见性边界。
//
// 回归价值：去掉任一条 SQL 里的 `+ retrievedChunkVisibilitySQL`，本测试立刻变红；
// 而「新加一条检索 SQL 忘了设边界」正是这次缺陷的形态 —— 所以还有个注册表完整性断言，
// 防止有人删掉登记项让测试静默失去覆盖。
func TestAllChunkReadSQLsCarryVisibilityBoundary(t *testing.T) {
	wantRegistered := []string{"vectorSearchSQL", "keywordSearchSQL", "expandAdjacentChunksSQL"}
	for _, name := range wantRegistered {
		sqlText, ok := chunkReadSQLs[name]
		if !ok {
			t.Errorf("%s 没有登记到 chunkReadSQLs —— 新增检索 SQL 必须登记，否则本测试覆盖不到它", name)
			continue
		}
		if !strings.Contains(sqlText, retrievedChunkVisibilitySQL) {
			t.Errorf("%s 没有拼上可见性边界，软删文档的 chunk 会被检索到", name)
		}
	}
	if len(chunkReadSQLs) != len(wantRegistered) {
		t.Errorf("chunkReadSQLs 有 %d 项，期望 %d 项：新增/删除登记项时请同步本测试",
			len(chunkReadSQLs), len(wantRegistered))
	}
}

// TestRetrievedChunkVisibilityBoundaryShape 校验边界条件本身的形态。
func TestRetrievedChunkVisibilityBoundaryShape(t *testing.T) {
	// 最危险的退化：常量缺失变成 0 → `status = 0` 不匹配任何行 → 过滤静默失效（no-op）。
	if entity.DocumentStatusDeleted == 0 {
		t.Fatal("entity.DocumentStatusDeleted 为 0，可见性过滤会静默变成 no-op")
	}
	// 必须以「排除」而非「包含」的语义写。
	if !strings.Contains(retrievedChunkVisibilitySQL, "NOT IN") {
		t.Errorf("可见性边界必须用 NOT IN 排除软删文档，实际: %s", retrievedChunkVisibilitySQL)
	}
	// 必须真的按 documents.status 判断（软删状态在 documents 表上，chunk 表没有 status）。
	if !strings.Contains(retrievedChunkVisibilitySQL, "documents") ||
		!strings.Contains(retrievedChunkVisibilitySQL, "status") {
		t.Errorf("可见性边界没有查 documents.status: %s", retrievedChunkVisibilitySQL)
	}
	// 状态值必须来自领域常量，不能是手写字面量。
	if !strings.Contains(retrievedChunkVisibilitySQL,
		fmt.Sprintf("status = %d", entity.DocumentStatusDeleted)) {
		t.Errorf("可见性边界里的状态值不是 entity.DocumentStatusDeleted(%d): %s",
			entity.DocumentStatusDeleted, retrievedChunkVisibilitySQL)
	}
}

// TestKeywordSearchSQLDoesNotRequireEmbedding 断言关键词 SQL 不再要求 chunk 有向量。
//
// 回归价值：把 `AND dc.embedding IS NOT NULL` 加回关键词 SQL，本测试变红。
func TestKeywordSearchSQLDoesNotRequireEmbedding(t *testing.T) {
	if strings.Contains(keywordSearchSQL, "embedding IS NOT NULL") {
		t.Errorf("关键词检索不应要求 chunk 有向量（会导致向量化失败的 chunk 永久不可见）:\n%s", keywordSearchSQL)
	}
	// 但它必须仍然要求「有关键词」，否则等于退化成全表扫描。
	for _, must := range []string{"dc.keywords IS NOT NULL", "dc.keywords && ?::text[]"} {
		if !strings.Contains(keywordSearchSQL, must) {
			t.Errorf("关键词检索缺少必要条件 %q:\n%s", must, keywordSearchSQL)
		}
	}
}

// TestVectorSearchSQLRequiresEmbedding 是上一条的对照组：
// 向量侧**必须**保留 `embedding IS NOT NULL`（没有向量算不出距离）。
// 没有这个对照，"不含 embedding 条件" 这条断言就可能因为拼串写错而空转。
func TestVectorSearchSQLRequiresEmbedding(t *testing.T) {
	if !strings.Contains(vectorSearchSQL, "dc.embedding IS NOT NULL") {
		t.Errorf("向量检索必须要求 chunk 有向量:\n%s", vectorSearchSQL)
	}
}
