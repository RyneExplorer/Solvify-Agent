package service

import (
	"solvify-agent/internal/llm"
	"solvify-agent/pkg/config"
	"testing"
)

// V3：预算不得超发。
//
// 回归对象是「保底 500」：remaining 已经小于 500 时，旧实现仍把历史预算抬到 500，
// 凭空超出 safeCap。修复后 remaining < 500 一律按实有给。
func TestBudgetsNeverExceedSafeCap(t *testing.T) {
	cases := []struct {
		name        string
		maxCtx      int
		toolsTokens int
	}{
		{"工具定义吃满窗口", 8192, 9000},
		{"工具定义恰好等于安全顶", 8192, 7782}, // int(8192*0.95)=7782
		{"工具定义略小于安全顶（剩余额度耗尽）", 8192, 7700},
		{"极小窗口", 4096, 3900},
		{"窗口未知返回零", 0, 0},
		{"正常 128k", 128000, 3000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, r, m := calculateContextBudgets(c.maxCtx, c.toolsTokens)
			budget, err := llm.ResolveContextBudget("", "", c.maxCtx, config.ContextDefaults().OutputLimit, false, config.ContextDefaults())
			safeCap := 0
			if err == nil {
				safeCap = budget.Hard
			}

			if h < 0 || r < 0 || m < 0 {
				t.Fatalf("预算不得为负: 历史=%d 检索=%d 记忆=%d", h, r, m)
			}
			available := max(safeCap-c.toolsTokens, 0)
			if sum := h + r + m; sum > available {
				t.Fatalf("预算超发: 历史=%d 检索=%d 记忆=%d 合计=%d > safeCap=%d", h, r, m, sum, safeCap)
			}
		})
	}
}

// V3 的具体回归点：剩余额度不足 500 时，历史预算必须是实有值而不是 500。
// 这条单独钉住，因为「不超发」可以靠别的约束蒙对，而这一格是缺陷的原样复现。
func TestHistoryBudgetDoesNotInflateBeyondRemaining(t *testing.T) {
	// 8192 窗口、工具定义占 7700 ⇒ 安全顶 7782，扣完工具只剩 82。
	// 旧实现会把历史抬到 500（超发 418），修复后为 0。
	h, _, _ := calculateContextBudgets(8192, 7700)
	if h != 0 {
		t.Fatalf("剩余额度已耗尽，历史预算应为 0，实际 %d", h)
	}
}

// 工具定义吃掉整个窗口时不得凭空分配历史预算。
func TestBudgetsWhenToolsEatWholeWindow(t *testing.T) {
	h, r, m := calculateContextBudgets(8192, 9000)
	if h != 0 || r != 0 || m != 0 {
		t.Fatalf("工具定义吃满窗口时应为 (0,0,0)，实际 (%d,%d,%d)", h, r, m)
	}
}

// 历史预算不得有一刀切的封顶。
//
// 回归对象是 `if historyBudget > 6000 { historyBudget = 6000 }`：那个数字与模型窗口无关，
// 128k 窗口的历史预算本来有 8 万+，被砍到 6000 后用户实际能带进上下文的历史只剩
// 约 20~30 轮中文，而且**砍掉的额度就此浪费**（不会分给检索或记忆）。
//
// 判据用「预算随窗口单调增长」而不是「某个具体数字」：钉死数字的话，
// 以后有人加封顶只会换一个数字蒙过去。
func TestHistoryBudgetHasNoFlatCap(t *testing.T) {
	small, _, _ := calculateContextBudgets(32000, 0)
	large, _, _ := calculateContextBudgets(128000, 0)

	if small <= 0 || large <= 0 {
		t.Fatalf("历史预算不应为 0：32k=%d 128k=%d", small, large)
	}
	if large <= small {
		t.Errorf("128k 窗口的历史预算(%d) 应大于 32k 窗口(%d) —— 说明历史预算被一个与窗口无关的数封了顶",
			large, small)
	}
}

// 单调性：窗口翻倍，预算必须严格变大（防止将来有人在中间塞一个绝对上限）。
func TestHistoryBudgetIsMonotonicInWindow(t *testing.T) {
	prev := 0
	for _, maxCtx := range []int{8192, 16000, 32000, 64000, 128000, 200000} {
		h, _, _ := calculateContextBudgets(maxCtx, 0)
		if h <= prev {
			t.Fatalf("窗口 %d 的历史预算 %d 未大于上一个窗口的 %d", maxCtx, h, prev)
		}
		prev = h
	}
}
