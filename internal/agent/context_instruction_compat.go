package agent

import (
	"context"
	"strings"
)

const citationFollowupInstruction = "多轮追问涉及原文、引用或事实核对时，历史答案仅作线索，必须重新调用当前范围的检索工具取得本轮来源；不能直接复用历史标签。无法取得证据就明确说明，不把历史回答称为已核验原文。"

const webResearchReadingInstruction = "优先少量相关官方页面或章节，不抓取无关全文。读取外置MCP结果时用json_path展开text再定位，关键词未命中从offset=0换用短词；后续使用返回的selection与next_offset，不猜测偏移，不重复相同失败参数。证据足够立即收敛，结论保留未核验范围。"

// 仅支持本次QA修复前的模板；旧checkpoint有效期结束后可移除此迁移。
// 不接受任意指令哈希，不重置摘要次数、预算或证据归属。
func (m *runtimeContextMiddleware) matchesSnapshotInstruction(ctx context.Context, hash string) bool {
	if hash == contentHash(m.instruction) {
		return true
	}
	restoring := m.req.CheckpointID != ""
	if private, ok := ctx.Value(taskContextSnapshotKey{}).(*taskContextSnapshot); ok {
		restoring = private.Restoring
	}
	if !restoring {
		return false
	}
	previous := strings.ReplaceAll(m.instruction, "\n"+citationFollowupInstruction, "")
	previous = strings.ReplaceAll(previous, webResearchReadingInstruction+"\n", "")
	previous = strings.ReplaceAll(previous, "1-2 句简短进度说明（可能展示给用户，不输出内部推理）", "1-2 句简短推理（不会展示给用户）")
	return previous != m.instruction && hash == contentHash(previous)
}
