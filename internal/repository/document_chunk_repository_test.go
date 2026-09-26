package repository

import (
	"strings"
	"testing"

	"solvify-agent/internal/model/entity"
)

// 本文件守的是「关键字搜索」这条 chunk 出口的两个边界：可见性 + 排序全序。
//
// ⚠️ 两条断言都是 **SQL 文本级**的，只能证明「条件/排序键写进去了」，证不了语义正确。
// 语义证据由 test1/ 下的真库探针给出（造一个软删文档 + chunk，确认搜不到）。

// TestSearchByKeywordSQLCarriesVisibilityBoundary 断言软删文档的 chunk 不会被搜到。
//
// 回归价值：这条出口曾漏了可见性边界 ⇒ 用户已删除的文档仍能被搜到。
// 边界定义与 rag 侧共用 entity.RetrievedChunkVisibilitySQL，所以本断言同时也锁住
// 「两边是同一份定义、没有各写一份」。
func TestSearchByKeywordSQLCarriesVisibilityBoundary(t *testing.T) {
	// 最危险的退化：常量缺失变成 0 → `status = 0` 不匹配任何行 → 过滤静默变成 no-op。
	if entity.RetrievedChunkVisibilitySQL == "" {
		t.Fatal("entity.RetrievedChunkVisibilitySQL 为空 —— 可见性过滤会静默变成 no-op，比不写还危险")
	}
	if !strings.Contains(searchByKeywordSQL, entity.RetrievedChunkVisibilitySQL) {
		t.Errorf("关键字搜索没有拼上可见性边界，软删文档的 chunk 会被搜到:\n%s", searchByKeywordSQL)
	}
}

// TestSearchByKeywordSQLIsTotallyOrdered 断言排序是「全序」。
//
// 分值是 CASE 出来的三种取值（1.0 / 0.8 / 0.0）⇒ 同分是常态；只有 `score DESC` 时，
// 并列行的先后由 PG 的排序算法决定，而算法会随 `LIMIT` 变 ⇒ 结果不可复现。
//
// ⚠️ 特别防「假兜底」：这里曾经写的是 `, created_at DESC`，看着已有第二排序键，
// 但同一篇文档的 chunk 是同一批插入的、时间戳完全相同
// （实测评测库 5 篇文档 × 每篇 3 块，count(DISTINCT created_at) 恒为 1），
// 对「同文档内并列」零作用 —— 而同分并列最常发生的就是同一篇文档的块之间。
func TestSearchByKeywordSQLIsTotallyOrdered(t *testing.T) {
	// 判据（orderEndsWithUniqueKey / clauseAfterLastOrderBy）来自 order_guard_test.go，
	// 全仓唯一一份；本用例只额外钉住「第二键不许用 created_at」这条**本出口特有**的约束。
	clause := clauseAfterLastOrderBy(searchByKeywordSQL)
	if clause == "" {
		t.Fatal("关键字搜索没有 ORDER BY —— 本测试会失去意义")
	}
	if strings.Contains(clause, "created_at") {
		t.Errorf("created_at 不是唯一键（同批插入的 chunk 时间戳完全相同），不能当第二排序键:\n  ORDER BY %s",
			clause)
	}
	if !orderEndsWithUniqueKey(clause) {
		t.Errorf("ORDER BY 缺唯一键兜底，排序不是全序:\n  ORDER BY %s", clause)
	}
}
