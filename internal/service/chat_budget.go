package service

import (
	"solvify-agent/internal/llm"
	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/tokenutil"
)

// truncationMarker 是上下文预算裁剪时追加在消息末尾的标记。
//
// 它描述的是「预算不够、这条消息被裁过」这件事，不是用户原始内容。
// 任何二次消费历史文本的地方（例如构造检索 query、抽实体）都必须先剥掉它，
// 否则标记里的「内容/过长/截断」会被当成正文词汇混进检索条件 —— 见 stripTruncationMarker。
const truncationMarker = "\n\n（内容过长，已截断）"

// calculateContextBudgets 根据模型最大上下文窗口 + 工具定义占用，分配历史、检索、记忆的 token 预算。
//
// P0-④ 关键修复：toolsTokens (深度模式/多工具场景的工具 JSON Schema 真 token 数) 必须先从总窗口
// 扣除，再分配回复预留和固定预留，否则多工具时直接把历史 + 检索预算挤成负数或零。
// 输出与裕量使用 LLM 同源策略；这里仅为候选装配分配额度，最终输入另行核对。
//
// 保底规则：**只在剩余额度装得下时才保底**。硬抬保底会突破 safeCap ——
// 窗口极小（工具定义几乎吃满）时历史曾被无条件抬到 500，等于凭空超发。
func calculateContextBudgets(maxContextLength int, toolsTokens ...int) (historyBudget, retrievalBudget, memoryBudget int) {
	return calculateContextBudgetsWithOutput(maxContextLength, config.ContextDefaults().OutputLimit, toolsTokens...)
}

func calculateContextBudgetsWithOutput(maxContextLength, output int, toolsTokens ...int) (historyBudget, retrievalBudget, memoryBudget int) {
	return calculateContextBudgetsWithInput(maxContextLength, output, 0, toolsTokens...)
}

func calculateContextBudgetsWithInput(maxContextLength, output, input int, toolsTokens ...int) (historyBudget, retrievalBudget, memoryBudget int) {
	toolReserve := 0
	if len(toolsTokens) > 0 && toolsTokens[0] > 0 {
		toolReserve = toolsTokens[0]
	}
	if maxContextLength <= 0 {
		return 0, 0, 0
	}
	budget, err := llm.ResolveContextBudget("", "", maxContextLength, output, false, config.ContextDefaults())
	if err != nil {
		return 0, 0, 0
	}
	budget = budget.WithInputLimit(input)
	remaining := max(budget.Hard-toolReserve, 0)
	// 2. 固定预留：System Prompt 基础骨架 + 当前 user question 包装 + 安全边距。
	// ⚠️ 必须封顶到 remaining：过去这里没有上限，remaining < 300 时仍被抬到 300，
	// 于是 remaining 变负并一路传导，最后把历史保底成一笔超发额度。
	fixedReserved := 1500
	if remaining-fixedReserved < 500 {
		fixedReserved = remaining / 4
		if fixedReserved < 300 && remaining >= 300 {
			fixedReserved = 300
		}
	}
	if fixedReserved > remaining {
		fixedReserved = remaining
	}
	remaining -= fixedReserved

	// 3. 检索上下文块（RAG context）优先保证至少 500，最多取 min(3000, remaining/3)
	retrievalBudget = 3000
	if remaining-retrievalBudget < 1000 {
		retrievalBudget = remaining / 3
	}
	if retrievalBudget < 500 {
		retrievalBudget = 500
	}
	if retrievalBudget > remaining {
		retrievalBudget = max(remaining, 0)
	}
	remaining -= retrievalBudget

	// 4. 记忆预算：与模型窗口成正比，但封顶。8k 及以下不给记忆，省出空间给历史。
	memoryBudget = 800
	switch {
	case maxContextLength >= 32000:
		memoryBudget = 1200
	case maxContextLength >= 16000:
		memoryBudget = 1000
	case maxContextLength <= 8192:
		memoryBudget = 400
	}
	if memoryBudget > remaining {
		memoryBudget = max(remaining/2, 0)
	}
	remaining -= memoryBudget

	// 5. 历史消息预算：剩下的全给历史。
	// ⚠️ 不再有「保底 500」—— 它只可能有害：remaining ≥ 500 时是空操作，
	// remaining < 500 时把额度抬到超过实有，直接超发 safeCap。
	// ⚠️ 也不再有一刀切的 6000 封顶 —— 那个数字与模型窗口无关：
	// 128k 窗口的历史预算本来有 8 万+，被它砍到 6000 后用户实际能带进上下文的历史
	// 只有 6000 token（约 20~30 轮中文），而且**砍掉的额度就此浪费**（不会分给任何人）。
	// 真正的刹车是 agent 层的 budgetGuard：它按「剩余窗口水位」逐轮压缩，
	// 能压到刚好装得下，而不是不管窗口多大都砍到同一个数字。
	if remaining <= 0 {
		logger.Warnf("[Budget] 预算已耗尽，历史预算归零: 模型窗口=%d 工具定义=%d 检索=%d 记忆=%d",
			maxContextLength, toolReserve, retrievalBudget, memoryBudget)
	}
	historyBudget = max(remaining, 0)
	return
}

// truncateHistoryByTokens 按轮对（user + assistant 配对）从尾部保留历史消息，
// 保证最后一条 user 问题不被截断，且 assistant 不会孤立存在。
func truncateHistoryByTokens(messages []entity.ChatMessage, maxTokens int, modelName string) []entity.ChatMessage {
	if maxTokens <= 0 {
		return nil
	}
	n := len(messages)
	if n == 0 {
		return nil
	}

	tailIdx := n - 1
	tailReserved := 0
	tailCutMsg := (*entity.ChatMessage)(nil)
	if messages[tailIdx].Role == "user" {
		t := tokenutil.CountTokens(messages[tailIdx].Content, modelName)
		if t > maxTokens {
			cut, actual := tokenutil.TruncateByTokens(messages[tailIdx].Content, modelName, max(maxTokens-50, 50))
			if actual > 0 {
				m := messages[tailIdx]
				m.Content = cut
				tailCutMsg = &m
				tailReserved = actual
			}
		} else {
			tailReserved = t
		}
	}

	pairs := make([][]entity.ChatMessage, 0, 4)
	total := tailReserved
	i := n - 1
	if messages[tailIdx].Role == "user" {
		i--
	}
	for i >= 0 {
		if messages[i].Role != "assistant" {
			i--
			continue
		}
		a := i
		u := -1
		for j := i - 1; j >= 0; j-- {
			if messages[j].Role == "user" {
				u = j
				break
			}
		}
		if u < 0 {
			break
		}
		pairTokens := 0
		for k := u; k <= a; k++ {
			pairTokens += tokenutil.CountTokens(messages[k].Content, modelName)
		}
		if total+pairTokens > maxTokens {
			remain := maxTokens - total
			if remain >= 120 {
				m := messages[u]
				cut, actual := truncateContentHeadByTokens(m.Content, modelName, remain)
				if actual > 0 {
					m.Content = cut + truncationMarker
					pairs = append(pairs, []entity.ChatMessage{m})
				}
			}
			break
		}
		total += pairTokens
		pair := append([]entity.ChatMessage(nil), messages[u:a+1]...)
		pairs = append(pairs, pair)
		i = u - 1
	}

	for l, r := 0, len(pairs)-1; l < r; l, r = l+1, r-1 {
		pairs[l], pairs[r] = pairs[r], pairs[l]
	}
	out := make([]entity.ChatMessage, 0, 2*len(pairs)+1)
	for _, p := range pairs {
		out = append(out, p...)
	}
	if tailCutMsg != nil {
		out = append(out, *tailCutMsg)
	} else if messages[tailIdx].Role == "user" && tailReserved > 0 {
		out = append(out, messages[tailIdx])
	}
	return out
}

// truncateContentHeadByTokens 从"头"按真 BPE 截断到至多 maxTokens。
// 与 tokenutil.TruncateByTokens 的区别：后者默认从左往右，这里再包一层
// 统一返回（截断后文本，实际 token）。
func truncateContentHeadByTokens(content, modelName string, maxTokens int) (string, int) {
	return tokenutil.TruncateByTokens(content, modelName, maxTokens)
}

// truncateContentByTokens 保留旧签名给现有调用方，内部转成新接口。
// 新代码优先用 tokenutil.TruncateByTokens，可拿到实际用了多少 token。
func truncateContentByTokens(content string, maxTokens int) string {
	out, _ := tokenutil.TruncateByTokens(content, "", maxTokens)
	return out
}
