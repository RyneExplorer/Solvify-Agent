package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/llm"
	"solvify-agent/pkg/logger"
)

const plannerSystemPrompt = `你是一个任务规划助手。根据用户问题，制定一个简单的执行计划。

规则：
1. 分析用户问题的核心目标
2. 将问题拆解为 2-5 个可执行步骤
3. 每个步骤应该是具体的搜索或分析动作
4. 只输出 JSON，不要输出任何解释

输出格式：
{"goal":"...","steps":["步骤1","步骤2",...]}`

// buildPlannerUserMessage 构建 Planner 的用户消息
func buildPlannerUserMessage(query string, history []historyMessage) string {
	if len(history) == 0 {
		return fmt.Sprintf("用户问题：%s", query)
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
	sb.WriteString(fmt.Sprintf("\n用户问题：%s", query))
	return sb.String()
}

// plan 调用 LLM 制定执行计划
func (e *Engine) plan(ctx context.Context, llmClient llm.Client, query string, history []historyMessage) *Plan {
	messages := []*schema.Message{
		schema.SystemMessage(plannerSystemPrompt),
		schema.UserMessage(buildPlannerUserMessage(query, history)),
	}

	resp, err := llmClient.Generate(ctx, llm.GenerateRequest{Messages: messages})
	if err != nil {
		logger.Warnf("Planner 调用失败，跳过计划: %v", err)
		return nil
	}
	if resp.Message == nil || resp.Message.Content == "" {
		return nil
	}

	var plan Plan
	if err := json.Unmarshal([]byte(resp.Message.Content), &plan); err != nil {
		logger.Warnf("Planner 返回格式错误，跳过计划: %v", err)
		return nil
	}
	if len(plan.Steps) == 0 {
		return nil
	}

	logger.Infof("Planner 生成计划: goal=%q, steps=%d", plan.Goal, len(plan.Steps))
	return &plan
}

// formatPlanForPrompt 将计划格式化为 prompt 注入文本
func formatPlanForPrompt(plan *Plan) string {
	if plan == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("## 执行计划\n目标：%s\n步骤：\n", plan.Goal))
	for i, step := range plan.Steps {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
	}
	sb.WriteString("\n请按照计划有序执行，避免重复搜索。")
	return sb.String()
}
