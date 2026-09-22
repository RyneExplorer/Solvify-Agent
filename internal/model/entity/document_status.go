package entity

// Document.Status 的取值域。
//
// 为什么这组常量放在 entity 而不是各 service 的私有作用域：
// 它不是某一层的内部细节，而是**跨层的领域语义**——检索层（internal/rag）也必须按它过滤，
// 否则软删文档的 chunk 会继续出现在回答与引用里。
//
// 原先它们是 internal/service 包的私有 const，rag 层既看不到、也无法引用，
// 于是两条检索 SQL 谁都没写这条过滤条件：**定义的缺失本身就是缺陷的成因**。
// 领域状态码只能有一处定义，任何层需要判断「什么状态算已删除」都必须引用这里。
const (
	// DocumentStatusUploaded 已上传，尚未开始解析
	DocumentStatusUploaded = 1
	// DocumentStatusProcessing 解析 / 分块 / 向量化中
	DocumentStatusProcessing = 2
	// DocumentStatusReady 处理完成，可被检索
	DocumentStatusReady = 3
	// DocumentStatusFailed 处理失败（chunk 可能只写入一部分）
	DocumentStatusFailed = 4
	// DocumentStatusDeleted 已软删。chunk 不会被物理清除，
	// 因此所有检索路径都必须显式排除该状态的文档（见 rag 包的 retrievedChunkVisibilitySQL）。
	DocumentStatusDeleted = 5
)
