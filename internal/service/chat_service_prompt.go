package service

import (
	"fmt"

	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/rag"
)

const (
	maxContextChars = 2000 // 检索结果最大字符数，防止超出模型上下文窗口
)

// buildRewritePrompt 组装查询改写 Prompt
// 历史消息已在 service 层按 token 预算截断，此处直接使用
func buildRewritePrompt(history []entity.ChatMessage, question string) []*schema.Message {
	systemPrompt := `你是一个查询改写助手。根据历史对话，将用户最新的问题改写为独立的、完整的检索查询。

规则：
1. 如果用户使用了代词（它、这个、那个、上面的等），请替换为具体指代的内容
2. 保持改写后的查询简洁，只保留用于检索的关键信息
3. 如果问题已经是独立完整的，直接返回原问题
4. 只输出改写后的查询，不要输出任何解释`

	var historyText string
	for _, msg := range history {
		switch msg.Role {
		case "user":
			historyText += "用户: " + msg.Content + "\n"
		case "assistant":
			historyText += "助手: " + msg.Content + "\n"
		}
	}

	userPrompt := fmt.Sprintf("历史对话：\n%s\n用户最新问题：%s\n\n改写后的检索查询：", historyText, question)

	return []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(userPrompt),
	}
}

// buildMessages 组装 LLM 消息列表
func buildMessages(history []entity.ChatMessage, question string, retrieveResult rag.Result) []*schema.Message {
	systemPrompt := `你是一个专业的知识问答助手。请严格遵守以下规则：

## 回答规则
1. **优先使用参考资料**：如果参考资料中包含足够的信息来回答问题，请直接基于参考资料作答，不要额外编造内容。
2. **资料不足时适度扩展**：如果参考资料只覆盖了问题的部分方面，可以结合你的通用知识适度补充，但必须明确标注哪些内容来自参考资料、哪些是补充说明，且补充内容不得与参考资料矛盾。
3. **无相关资料时如实告知**：如果参考资料中完全没有相关信息，请明确告知用户"当前知识库中未找到相关信息"，不要编造答案。
4. **绝不编造或篡改**：绝对不要捏造参考资料中不存在的数据、事实或结论。宁可说"不确定"也不要胡编。

## 格式要求
- 使用 Markdown 格式输出回答
- 适当使用标题（##、###）、列表（- 或 1.）、加粗（**重点**）等格式提升可读性
- 引用参考资料时使用行内标注，如：（来源：文档标题）`

	messages := []*schema.Message{
		schema.SystemMessage(systemPrompt),
	}

	for _, msg := range history {
		switch msg.Role {
		case "user":
			messages = append(messages, schema.UserMessage(msg.Content))
		case "assistant":
			messages = append(messages, schema.AssistantMessage(msg.Content, nil))
		}
	}

	if retrieveResult.Hit {
		contextText := "## 参考资料\n\n"
		for i, doc := range retrieveResult.Documents {
			contextText += fmt.Sprintf("### [%d] %s\n\n%s\n\n", i+1, doc.Title, doc.Content)
		}
		if len(contextText) > maxContextChars {
			contextText = contextText[:maxContextChars] + "\n\n（参考资料过长，已截断）\n\n"
		}
		questionText := fmt.Sprintf("%s\n---\n\n**问题**：%s\n\n请根据以上参考资料回答。如果资料充分，直接作答；如果资料不足，请在回答中说明哪些部分来自参考资料、哪些是补充说明。", contextText, question)
		messages = append(messages, schema.UserMessage(questionText))
	} else {
		questionText := fmt.Sprintf("**问题**：%s\n\n（当前无匹配的参考资料，请如实告知用户知识库中未找到相关信息。）", question)
		messages = append(messages, schema.UserMessage(questionText))
	}

	return messages
}
