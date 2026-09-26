package entity

import "fmt"

// RetrievedChunkVisibilitySQL 是所有「读取 chunk 内容、可能把内容交给用户」的 SQL
// **必须**拼上的可见性边界：排除已软删文档（documents.status = DocumentStatusDeleted）。
//
// 为什么定义在领域层：软删文档的 chunk 不可见是一条**领域不变量**，它现在有两个使用方 ——
// rag（向量 / 关键词 / 相邻扩展三条检索 SQL）与 repository（关键字搜索 SQL）。
// 定义放在任一使用方里，另一个就得复制一份，于是「什么算可见」出现两个来源，
// 而「有两个来源」正是本项目反复踩到的缺陷温床。状态值 DocumentStatusDeleted
// 本来就在本包，所以上移到这里不需要任何额外依赖。
//
// 为什么用 NOT IN 子查询而不是 JOIN documents：检索主查询刻意不 JOIN documents
// （见 rag/hybrid_retriever.go 的说明），软删文档只占极小比例、documents.id 是主键，
// 子查询结果集很小。
//
// ⚠️ 若 DocumentStatusDeleted 退化成 0（常量被改/缺失），条件会变成 `status = 0`，
// 不匹配任何行 ⇒ 过滤**静默变成 no-op**，比不写这条还危险。
// 由 rag 与 repository 两侧的守卫测试共同兜住。
var RetrievedChunkVisibilitySQL = fmt.Sprintf(`
			AND dc.document_id NOT IN (
				SELECT d.id FROM documents d WHERE d.status = %d)`,
	DocumentStatusDeleted)
