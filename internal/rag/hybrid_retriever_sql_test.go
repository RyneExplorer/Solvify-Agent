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
	if got := (Query{TopK: 3}).candidateLimitWith(defaultCandidateMultiplier); got != 3*defaultCandidateMultiplier {
		t.Errorf("candidateLimitWith(TopK=3, 默认系数)=%d 期望 %d", got, 3*defaultCandidateMultiplier)
	}
	// 兜底必须发生在乘系数**之前**，否则 0*2 仍是 0。
	if got := (Query{TopK: 0}).candidateLimitWith(defaultCandidateMultiplier); got != defaultTopK*defaultCandidateMultiplier {
		t.Errorf("candidateLimitWith(TopK=0)=%d 期望 %d（兜底没生效？）", got, defaultTopK*defaultCandidateMultiplier)
	}
	// 系数 <= 0 必须回落到默认值，而不是让 LIMIT 变成 0（恒空）。
	// 这一条守的是"没配置"与"配成 0"不能同归一处理。
	for _, m := range []int{0, -1} {
		if got := (Query{TopK: 3}).candidateLimitWith(m); got != 3*defaultCandidateMultiplier {
			t.Errorf("candidateLimitWith(TopK=3, 系数=%d)=%d 期望回落 %d（否则 LIMIT 0 恒空）",
				m, got, 3*defaultCandidateMultiplier)
		}
	}
	// 收紧候选池：系数 1 ⇒ 只取 TopK 条
	if got := (Query{TopK: 3}).candidateLimitWith(1); got != 3 {
		t.Errorf("candidateLimitWith(TopK=3, 系数=1)=%d 期望 3", got)
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

	// 参数组装是 *HybridRetriever 的方法（它要用 r.candidateMultiplier），所以要先造一个检索器。
	// 这里只能走公开构造器：直接写 &HybridRetriever{} 会绕过 `<= 0 回落默认值` 那一步，
	// 而"没配置"和"配成 0"恰恰是本组要区分的两件事。
	newRetriever := func(multiplier int) *HybridRetriever {
		return NewHybridRetriever(HybridRetrieverConfig{CandidateMultiplier: multiplier})
	}

	t.Run("向量检索", func(t *testing.T) {
		r := newRetriever(defaultCandidateMultiplier)
		args := r.vectorSearchArgs(Query{TopK: 3, KnowledgeBaseIDs: []string{"kb-1"}, UserID: "u-1"}, "VEC")
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
		if got := lastInt(t, "vectorSearchArgs", args); got != 3*defaultCandidateMultiplier {
			t.Errorf("LIMIT=%d 期望 %d", got, 3*defaultCandidateMultiplier)
		}
		// 回归点：漏传 TopK 时必须是兜底值，不能是 0
		args = r.vectorSearchArgs(Query{}, "VEC")
		if got := lastInt(t, "vectorSearchArgs", args); got != defaultTopK*defaultCandidateMultiplier {
			t.Errorf("漏传 TopK 时 LIMIT=%d 期望 %d（退化回 LIMIT 0 恒空）", got, defaultTopK*defaultCandidateMultiplier)
		}
	})

	t.Run("向量检索_LIMIT跟系数走", func(t *testing.T) {
		// 收紧候选池（B 方案）就是把这个系数调成 1。
		// 若某天有人把 `r.candidateMultiplier` 硬编回常量，本断言变红。
		for _, m := range []int{1, 3} {
			args := newRetriever(m).vectorSearchArgs(Query{TopK: 4}, "VEC")
			if got := lastInt(t, "vectorSearchArgs", args); got != 4*m {
				t.Errorf("系数=%d、TopK=4 时 LIMIT=%d 期望 %d", m, got, 4*m)
			}
		}
	})

	t.Run("关键词检索", func(t *testing.T) {
		r := newRetriever(defaultCandidateMultiplier)
		args := r.keywordSearchArgs(Query{TopK: 3, KnowledgeBaseIDs: []string{"kb-1"}, UserID: "u-1"}, "{a,b}")
		if len(args) != 8 {
			t.Errorf("keywordSearchArgs 参数个数=%d 期望 8（与 SQL 里 ? 的个数一致）", len(args))
		}
		// 逐个对齐 keywordSearchSQL 里 ? 的出现顺序 —— 位置参数一旦错位，
		// 轻则查不到、重则把 user_id 当知识库 id 用，且只有跑真库才会暴露。
		//   ?1 权重模式开关 ?::boolean           ?2 CTE 统计 df 的知识库范围
		//   ?3 CTE 的 user_id                    ?4 CTE 的词项 unnest(?::text[])
		//   ?5 knowledge_base_id IN (?)          ?6 keywords && ?::text[]
		//   ?7 user_id = ?                       ?8 LIMIT ?
		//
		// ⚠️ 前 4 个参数全在 CTE 里，写错**不会报错**：例如把 ?1 的布尔和 ?3 的 user_id 调换，
		// SQL 依然能跑，只是权重要么恒 1、要么按 df 算但 df 算错 —— 两种都静默。
		want := []any{false, []string{"kb-1"}, "u-1", "{a,b}", []string{"kb-1"}, "{a,b}", "u-1",
			3 * defaultCandidateMultiplier}
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
		args = r.keywordSearchArgs(Query{}, "{a,b}")
		if got := lastInt(t, "keywordSearchArgs", args); got != defaultTopK*defaultCandidateMultiplier {
			t.Errorf("漏传 TopK 时 LIMIT=%d 期望 %d", got, defaultTopK*defaultCandidateMultiplier)
		}
	})

	t.Run("关键词检索_权重开关跟着配置走", func(t *testing.T) {
		// A 方案（给罕见词加权）就是把这个开关翻转。
		// 断言它真的被传进 SQL —— 否则"打开了开关"只是改了个没人读的字段。
		for _, want := range []bool{false, true} {
			r := NewHybridRetriever(HybridRetrieverConfig{KeywordIDFWeighted: want})
			args := r.keywordSearchArgs(Query{TopK: 3}, "{a,b}")
			got, ok := args[0].(bool)
			if !ok {
				t.Fatalf("第 1 个参数应是权重模式开关 bool，实际 %T", args[0])
			}
			if got != want {
				t.Errorf("KeywordIDFWeighted=%v 但传给 SQL 的是 %v", want, got)
			}
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

// TestRetrievedChunkVisibilityBoundaryHasSingleSource 断言本包的可见性常量只是
// entity 那份的**引用**，不是第二份定义。
//
// 回归价值：若有人在 rag 里重新写一份 `fmt.Sprintf(... status = 5 ...)`，
// 两份定义会各自漂移（改一处忘一处），而所有"内容像不像"的断言都看不出有几份。
func TestRetrievedChunkVisibilityBoundaryHasSingleSource(t *testing.T) {
	if retrievedChunkVisibilitySQL != entity.RetrievedChunkVisibilitySQL {
		t.Errorf("rag 的可见性边界与 entity.RetrievedChunkVisibilitySQL 不一致 —— 边界出现了第二个来源")
	}
}

// TestReciprocalRankFusionIsTotallyOrdered 用**行为**证明融合段的比较函数是全序：
// 两条融合分完全相等的结果，返回顺序必须每次都一样，且按 id 裁决。
//
// 为什么必须用行为证明：入参来自 map 迭代，顺序本身就随机；纯文本断言看不出
// "等分时会不会翻"。这里跑 200 次，任何一次顺序不同即失败。
func TestReciprocalRankFusionIsTotallyOrdered(t *testing.T) {
	// 两侧权重取同一个值，才能造出真正的等分：两条各在单侧排第 1 ⇒ 融合分相同。
	r := NewHybridRetriever(HybridRetrieverConfig{VectorWeight: 1, KeywordWeight: 1})

	vectorSide := []scoredChunk{{ID: "zzz"}}
	keywordSide := []scoredChunk{{ID: "aaa"}}

	first := ""
	for i := 0; i < 200; i++ {
		got := r.reciprocalRankFusion(vectorSide, keywordSide)
		if len(got) != 2 {
			t.Fatalf("期望 2 条结果，实际 %d 条", len(got))
		}
		// 前置条件：两条真的等分。不等分的话这条测试证明不了"等分时确定"。
		if got[0].Score != got[1].Score {
			t.Fatalf("前置条件不成立：两条应当等分，实际 %.6f vs %.6f", got[0].Score, got[1].Score)
		}
		order := got[0].ID + "," + got[1].ID
		if i == 0 {
			first = order
			continue
		}
		if order != first {
			t.Fatalf("等分时返回顺序不可复现：第 %d 次得到 %s，第 1 次是 %s", i+1, order, first)
		}
	}
	if first != "aaa,zzz" {
		t.Errorf("等分时应按 id 升序裁决，实际顺序 %s", first)
	}
}

// TestReciprocalRankFusionPrefersScoreOverID 是上一条的**对照组**：
// 分数不同时必须按分数降序，而不是按 id。
//
// 没有它，上一条断言可能退化成一个更弱的命题（"结果按 id 排序"）而照样全绿：
// 这里让 id 更大（zzz）的排在更前的位置，若实现改成按 id 排，本测试立刻变红。
func TestReciprocalRankFusionPrefersScoreOverID(t *testing.T) {
	r := NewHybridRetriever(HybridRetrieverConfig{VectorWeight: 1, KeywordWeight: 1})

	// 同侧第 1 名与第 2 名 ⇒ 分数不同；且第 1 名的 id 在字典序上更大。
	got := r.reciprocalRankFusion([]scoredChunk{{ID: "zzz"}, {ID: "aaa"}}, nil)
	if len(got) != 2 {
		t.Fatalf("期望 2 条结果，实际 %d 条", len(got))
	}
	if got[0].ID != "zzz" {
		t.Errorf("分数不同时应按分数降序，首条应为 zzz，实际 %s（是不是变成按 id 排序了？）", got[0].ID)
	}
}
