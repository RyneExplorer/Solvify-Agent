package agent

import (
	"solvify-agent/pkg/logger"
)

func deepMaxIterations(cfgVal int) int {
	if cfgVal <= 0 {
		return 5
	}
	return cfgVal
}

func (e *Engine) deepSubagentIterations(req Request) int {
	if req.TaskID != "" && e.cfg.DeepMaxSubagentIterations > 0 {
		return e.cfg.DeepMaxSubagentIterations
	}
	return deepMaxIterations(e.cfg.MaxIterations)
}

// quickMaxIterations 读快速模式迭代上限。构造函数已保证 > 0，这里只兜住
// 直接构造 Engine（测试/工具）的场景。
func (e *Engine) quickMaxIterations() int {
	if e.cfg.QuickAgentMaxIterations <= 0 {
		return 8
	}
	return e.cfg.QuickAgentMaxIterations
}

// maxToolCalls 把 MaxIterations 换算成 prompt 里的「工具调用次数上限」。
// eino 一轮 = 一次模型调用 + 可能的工具调用，所以工具轮比模型轮少一轮。
// 只在 prompt 文案里用，不参与编排。
func maxToolCalls(maxIterations int) int {
	if maxIterations < 2 {
		return 1
	}
	return maxIterations - 1
}

// ── 迭代预算 ──
//
// eino 的 MaxIterations =「一次执行里允许的模型调用次数」。ReAct 每一轮里模型可以调
// 一次工具，调完必须再进一次模型才能出答案，因此：
//
//	MaxIterations = 最终回答轮(1) + 工具轮预算
//
// 交互型工具（ask_clarify 澄清、危险工具审批）与业务工具一样各占一轮模型调用，
// 但它们「暂停等人」—— 而 prompt 明确鼓励「不确定的先用检索工具找线索，真不行再澄清」，
// 于是预算必须容得下「检索 1 + 澄清 1 + 再检索 1 + 最终回答 1」。
//
// 下界必须由「本模式实际挂了什么」推导，不能只读配置：只看配置会让
// 「挂了 ask_clarify 却给装不下的预算」结构上可能发生。实测就撞在这条上 ——
// 快速模式 2 轮下澄清吃掉 1 轮，恢复后模型又用 1 轮检索，回 ChatModel 即
// exceeds max iterations（计数器还存在 checkpoint 里，恢复会继续扣减）。
const (
	finalAnswerRounds     = 1 // 最后一轮必须是最终回答，不能再调工具
	businessToolRounds    = 1 // 业务工具（检索等）至少能用一次
	interactiveToolRounds = 2 // 一次澄清 / 审批，加上交互前的线索检索
)

// agentIterations 推导本模式的迭代预算：取「配置值」与「能力下界」的较大者。
//
// interactive 必须与本次真正注入的中间件读同一个结果（澄清 / 危险工具审批），
// 否则「注入了中断能力却没给足轮次」会再次发生。
// 配置值低于下界时抬升并记 Warn —— 静默抬升会让「配置写 2、实际跑 3」难以解释。
func agentIterations(configured int, interactive bool) int {
	need := finalAnswerRounds + businessToolRounds
	if interactive {
		need += interactiveToolRounds
	}
	if configured >= need {
		return configured
	}
	logger.Warnf("[Agent] 迭代预算不足：配置 %d 轮 < 已挂载能力所需下界 %d 轮（含交互型工具=%v），按 %d 轮执行",
		configured, need, interactive, need)
	return need
}
