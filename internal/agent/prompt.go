package agent

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/rag"
)

// buildSystemPrompt 构建 Agent 系统提示词
func buildSystemPrompt(tools []toolInfo, initialResult rag.Result) string {
	var sb strings.Builder

	sb.WriteString(`你是 Solvify 知识助理，一个专业的 AI 助手。

## 能力
- 基于知识库进行语义搜索
- 使用工具获取信息并综合分析
- 多步推理解决复杂问题

## 工具使用
你有以下工具可用：
`)

	for _, t := range tools {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", t.name, t.description))
	}

	// 如果有已有检索结果，注入到提示词中
	if initialResult.Hit && len(initialResult.Documents) > 0 {
		sb.WriteString("\n## 已有检索结果\n以下是从知识库中检索到的相关内容，如果这些内容足以回答问题，请直接使用 final_answer 工具提交答案。\n\n")
		for i, doc := range initialResult.Documents {
			sb.WriteString(fmt.Sprintf("### [%d] %s\n%s\n\n", i+1, doc.Title, doc.Content))
		}
	}

	sb.WriteString(`
## 回答要求
1. 优先使用已有检索结果回答
2. 如果已有结果不足，使用工具搜索补充
3. 回答要准确、简洁、有条理
4. 在相关句子后标注来源 [1][2][3]
5. 使用中文回答

## 重要
- 使用 final_answer 工具提交最终答案
- 如果无法回答，诚实说明原因`)

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
		content := msg.content
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		sb.WriteString(fmt.Sprintf("%s: %s\n", role, content))
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

const (
	maxRewriteHistoryChar = 200 // 改写 prompt 中每条历史消息最大字符数
)

// buildReActSystemPrompt 构建 ReAct 循环系统提示词（知识库无结果场景）
func buildReActSystemPrompt(tools []toolInfo) string {
	var sb strings.Builder

	sb.WriteString(`你是 Solvify 知识助理，一个专业的 AI 助手。

## 当前情况
用户的问题已经在知识库中检索过，但没有找到相关内容。

## 工具使用
你有以下工具可用：
`)

	for _, t := range tools {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", t.name, t.description))
	}

	sb.WriteString(`
## 回答要求
1. 使用 web_search 工具搜索互联网获取相关信息
2. 基于搜索结果，使用 final_answer 工具提交最终答案
3. 回答要准确、简洁、有条理
4. 在相关句子后标注信息来源
5. 使用中文回答

## 重要
- 必须使用 final_answer 工具提交最终答案
- 如果搜索后仍然无法回答，诚实说明原因
- 不要编造信息`)

	return sb.String()
}

// buildRewritePrompt 组装查询改写 Prompt（取最近 5 条消息）
func buildRewritePrompt(history []historyMessage, question string) []*schema.Message {
	systemPrompt := `你是一个查询改写助手。根据历史对话，将用户最新的问题改写为独立的、完整的检索查询。

规则：
1. 如果用户使用了代词（它、这个、那个、上面的等），请替换为具体指代的内容
2. 保持改写后的查询简洁，只保留用于检索的关键信息
3. 如果问题已经是独立完整的，直接返回原问题
4. 只输出改写后的查询，不要输出任何解释`

	start := 0
	if len(history) > 5 {
		start = len(history) - 5
	}

	var historyText string
	for _, msg := range history[start:] {
		content := msg.content
		if len(content) > maxRewriteHistoryChar {
			content = content[:maxRewriteHistoryChar] + "..."
		}
		switch msg.role {
		case "user":
			historyText += "用户: " + content + "\n"
		case "assistant":
			historyText += "助手: " + content + "\n"
		}
	}

	userPrompt := fmt.Sprintf("历史对话：\n%s\n用户最新问题：%s\n\n改写后的检索查询：", historyText, question)

	return []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(userPrompt),
	}
}
