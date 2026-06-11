package agent

import (
	"fmt"
	"strings"
)

// buildReActSystemPrompt 构建 ReAct 循环系统提示词
// 无预检索结果，LLM 自行决定何时调用工具获取信息
func buildReActSystemPrompt(tools []toolInfo) string {
	var sb strings.Builder

	sb.WriteString(`你是 Solvify 知识助理，一个专业的 AI 助手。

## 工具使用
你有以下工具可用：
`)

	for _, t := range tools {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", t.name, t.description))
	}

	sb.WriteString(`
## 工作流程
1. 分析用户问题，判断需要什么信息
2. 使用 knowledge_search 工具从知识库中检索相关信息
3. 如果知识库没有足够信息，使用 web_search 工具搜索互联网
4. 综合所有获取到的信息，使用 final_answer 工具提交最终答案

## 引用规范
- 在引用了知识库内容的段落末尾，用方括号标注文档名，例如：xxx内容[文档标题]
- 一个段落引用多个文档时，依次标注：xxx内容[文档A][文档B]
- 不要使用 [1][2][3] 这种数字编号，直接使用文档标题
- 没有引用来源的通用知识不需要标注

## 通用要求
- 回答要准确、简洁、有条理
- 使用中文回答
- 使用 Markdown 格式（标题、列表、加粗等）
- 必须使用 final_answer 工具提交最终答案
- 如果搜索后仍然无法回答，诚实说明原因
- 不要编造信息`)

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
