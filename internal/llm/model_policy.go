package llm

import (
	"fmt"

	"solvify-agent/pkg/config"
)

// 最少保留可形成简短答复的空间；原调用额度更小时不擅自提高。
const MinimumAdaptiveOutput = 512

// FitOutputLimit 在总窗口与独立输入上限内分配输出，不修改部署能力。
// 调用方应先移除可选材料，再用此策略为必要输入让出空间。
func FitOutputLimit(window, inputLimit, requested, used int, policy config.ContextPolicy) (int, error) {
	minimum := min(requested, MinimumAdaptiveOutput)
	budget, err := ResolveContextBudget("", "", window, minimum, false, policy)
	if err != nil {
		return 0, err
	}
	if inputLimit > 0 && used > inputLimit {
		return 0, fmt.Errorf("%w: 必要输入=%d 独立输入上限=%d", ErrContextBudget, used, inputLimit)
	}
	available := window - budget.Margin - used
	if available < minimum {
		return 0, fmt.Errorf("%w: 输入=%d 可用输出=%d 最低输出=%d", ErrContextBudget, used, available, minimum)
	}
	return min(requested, available), nil
}

// ManagedOutputLimit 是项目调用策略，不是服务商能力默认值。
// 留出至少四分之三窗口供指令、工具和输入；缺少能力时仍拒绝调用。
func ManagedOutputLimit(window, maxOutput int) int {
	if window <= 0 || maxOutput <= 0 {
		return 0
	}
	return min(8192, maxOutput, window/4)
}
