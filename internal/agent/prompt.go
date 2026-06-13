package agent

import (
	"fmt"
	"strings"
)

// buildReActSystemPrompt 构建 ReAct 循环系统提示词
// 支持注入执行计划和搜索记忆
func buildReActSystemPrompt(tools []toolInfo, plan *Plan, memory *Memory) string {
	var sb strings.Builder

	sb.WriteString("你是 Solvify 知识助理，一个专业的 AI 助手。\n\n")

	sb.WriteString("## 工具\n")
	for _, t := range tools {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", t.name, t.description))
	}

	sb.WriteString(`
## 工作流程

### 第一步：检索知识库
- 使用 knowledge_search 搜索 1-2 次，每次用不同的关键词或角度
- 第一次用核心关键词，如果结果不理想，换一个角度或同义词再搜一次
- 搜索结果会自动返回相关文档片段，不需要你逐个浏览

### 第二步：判断是否足够
- 如果检索到了相关信息，直接进入第三步生成答案
- 如果知识库完全没有相关内容，使用 web_search 联网搜索 1 次
- 如果联网搜索也不可用，用你自己的知识回答

### 第三步：生成答案
- 当信息充分时，直接在回复中输出最终答案（Markdown 格式）

### 搜索次数限制
- knowledge_search 最多调用 2 次
- web_search 最多调用 1 次
- 总工具调用不超过 3 次
- 不要反复搜索相同或相似的内容

## 引用规范（必须遵守）
- 答案中凡是参考了知识库内容的地方，必须在句末用方括号标注文档标题，并用花括号标注引用的原文
- 格式：xxx内容[文档标题]{引用的原文}，多个来源依次标注：xxx内容[文档A]{原文A}[文档B]{原文B}
- 文档标题来自工具返回结果中的 title 字段，必须原样引用，不要修改或翻译
- 引用的原文必须是知识库文档中与当前句子直接相关的那一句话或那一段话，不要引用整篇文档
- 引用原文要精炼，只保留支撑当前句子的核心内容，一般不超过 50 字
- 不要使用 [1][2][3] 数字编号，直接使用文档标题
- 没有引用来源的通用知识不需要标注
- 示例：Go 语言由 Google 于 2009 年发布[Go语言简介]{Go 语言由 Google 开发并于 2009 年正式发布}，支持并发编程[Go语言并发]{Go 原生支持 goroutine 和 channel 实现并发}

## 通用要求
- 回答要准确、简洁、有条理
- 使用中文回答
- 使用 Markdown 格式（标题、列表、加粗等）
- 信息充分时直接在回复中输出答案，无需调用工具
- 如果工具执行失败或搜索无结果，用你自己的知识尽力回答，不要直接说"我无法回答"
- 不要编造信息`)

	// 注入执行计划
	if planSection := formatPlanForPrompt(plan); planSection != "" {
		sb.WriteString("\n\n")
		sb.WriteString(planSection)
	}

	// 注入搜索记忆
	if memory != nil {
		if memSummary := memory.Summary(); memSummary != "" {
			sb.WriteString("\n\n## 搜索记忆\n")
			sb.WriteString(memSummary)
		}
	}

	return sb.String()
}

// buildUserMessage 构建用户消息
func buildUserMessage(query string, history []historyMessage) string {
	if len(history) == 0 {
		return query
	}

	var sb strings.Builder
	sb.WriteString("历史对话：\n")
	for _, msg := range history {
		role := "用户"
		if msg.role == "assistant" {
			role = "助手"
		}
		sb.WriteString(fmt.Sprintf("%s: %s\n", role, msg.content))
	}
	sb.WriteString(fmt.Sprintf("\n当前问题：%s", query))
	return sb.String()
}

// toolInfo 工具信息（用于提示词构建）
type toolInfo struct {
	name        string
	description string
}

// historyMessage 历史消息
type historyMessage struct {
	role    string
	content string
}
