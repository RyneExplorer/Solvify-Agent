package service

import (
	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/tokenutil"
)

// calculateContextBudgets 根据模型最大上下文窗口 + 工具定义占用，分配历史、检索、记忆的 token 预算。
//
// P0-④ 关键修复：toolsTokens (深度模式/多工具场景的工具 JSON Schema 真 token 数) 必须先从总窗口
// 扣除，再分配回复预留和固定预留，否则多工具时直接把历史 + 检索预算挤成负数或零。
// 同时所有预算最终都按 0.95*maxCtx 的安全顶封顶，给偶发的角色名/special token 留余量。
func calculateContextBudgets(maxContextLength int, toolsTokens ...int) (historyBudget, retrievalBudget, memoryBudget int) {
	toolReserve := 0
	if len(toolsTokens) > 0 && toolsTokens[0] > 0 {
		toolReserve = toolsTokens[0]
	}
	if maxContextLength <= 0 {
		maxContextLength = 8192
	}
	// 0.95 的安全顶：角色标记、特殊 token、工具结果 JSON 序列化扩展，都容易让"算刚好"爆。
	safeCap := int(float64(maxContextLength) * 0.95)
	if toolReserve >= safeCap {
		// 工具定义已经吃掉整个窗口（极端异常配置）：给历史留 200 保底，其他归零
		return 200, 0, 0
	}
	remaining := safeCap - toolReserve

	// 1. 回复预留：不超过 4096 或 safeCap 的 1/4
	completionReserved := 4096
	if remaining/4 < completionReserved {
		completionReserved = remaining / 4
	}
	if completionReserved < 200 {
		completionReserved = 200
	}
	remaining -= completionReserved

	// 2. 固定预留：System Prompt 基础骨架 + 当前 user question 包装 + 安全边距
	fixedReserved := 1500
	if remaining-fixedReserved < 500 {
		fixedReserved = remaining / 4
		if fixedReserved < 300 {
			fixedReserved = 300
		}
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

	// 5. 历史消息预算：剩下的全给历史，保底 500，封顶 6000（防止过大的上下文拖慢模型推理）
	historyBudget = remaining
	historyBudget = max(historyBudget, 500)
	if historyBudget > 6000 {
		historyBudget = 6000
	}
	return
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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
					m.Content = cut + "\n\n（内容过长，已截断）"
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
