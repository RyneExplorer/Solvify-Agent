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

// buildMessages 组装快速检索模式的 LLM 消息列表
func buildMessages(history []entity.ChatMessage, question string, retrieveResult rag.Result) []*schema.Message {
	// 快速检索模式专用提示词
	systemPrompt := "你是一个专业的知识问答助手。\n\n" +
		"## 回答规则\n" +
		"1. 先分析知识库检索结果是否与问题直接相关\n" +
		"2. 如果知识库有直接相关内容，用知识库内容回答\n" +
		"3. 如果知识库没有直接相关内容，说明情况，然后用通用知识回答\n" +
		"4. 可以提及知识库中找到的相关文档\n" +
		"5. 禁止捏造虚假信息\n\n" +
		"## 格式要求\n" +
		"- 使用 Markdown 格式\n" +
		"- 用自己的语言组织回答，不要直接复制原文\n" +
		"- 不要在回答中使用 [1] [2] 等编号引用\n" +
		"- 引用信息自动显示在消息底部，无需手动标注"

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
		contextText := "## 知识库检索结果\n\n"
		for _, doc := range retrieveResult.Documents {
			contextText += fmt.Sprintf("### %s\n\n%s\n\n", doc.Title, doc.Content)
		}
		if len(contextText) > maxContextChars {
			contextText = contextText[:maxContextChars] + "\n\n（参考资料过长，已截断）\n\n"
		}
		questionText := fmt.Sprintf("%s---\n\n**问题**：%s", contextText, question)
		messages = append(messages, schema.UserMessage(questionText))
	} else {
		questionText := fmt.Sprintf("**问题**：%s\n\n知识库中未找到相关内容，请用通用知识回答。", question)
		messages = append(messages, schema.UserMessage(questionText))
	}

	return messages
}
