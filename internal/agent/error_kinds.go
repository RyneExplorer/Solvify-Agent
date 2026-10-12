package agent

import (
	"github.com/cloudwego/eino/adk"

	apperrors "solvify-agent/pkg/errors"
)

// 在 init 阶段把 eino 的哨兵错误登记到失败种类上。
//
// 这样 pkg/errors 不必依赖 eino，而「迭代超限」又有了**结构化**判据：
// 之前它只能靠文本匹配（"exceeds"），结果被上下文超长的关键词表抢先命中，
// 一次迭代超限就把模型有效窗口从 64000 砍到 8192。
//
// ⚠️ 新增「eino 有哨兵错误」的种类时登记在这里，不要在 service / controller 里
// 另写一份文本匹配。
func init() {
	apperrors.RegisterKindSentinel(apperrors.KindMaxIterations, adk.ErrExceedMaxIterations)
}
