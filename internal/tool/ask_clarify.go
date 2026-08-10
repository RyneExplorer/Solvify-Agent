package tool

import (
	"context"
	"fmt"

	einoTool "github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
)

// AskClarifyInput 澄清追问工具的参数
type AskClarifyInput struct {
	Question string   `json:"question" jsonschema:"required" jsonschema_description:"需要向用户澄清的问题，一句话不超过100字"`
	Options  []string `json:"options,omitempty" jsonschema_description:"可选选项，最多4个，用户可点选也可自由输入，开放式问题可省略"`
	Context  string   `json:"context,omitempty" jsonschema_description:"为什么需要澄清，当前理解程度和缺少的关键信息，最多200字"`
}

// NewAskClarifyTool 创建 ask_clarify 工具。
// 注意：这个工具的业务逻辑是空壳——真正的拦截和 Interrupt 由 agent.buildClarifyMiddleware 处理。
// 工具本身只返回"等待用户回答"，但永远不会到达这一步（被 middleware 拦截了）。
func NewAskClarifyTool() func(userID string, kbIDs []string) einoTool.InvokableTool {
	return func(userID string, kbIDs []string) einoTool.InvokableTool {
		t, _ := toolutils.InferTool[AskClarifyInput, ToolResponse](
			"ask_clarify",
			"向用户澄清模糊问题。当用户的指令/问题存在歧义、历史对话信息不足以唯一确定目标、或你不确定下一步该怎么做时调用此工具，暂停并向用户提问。调用后流程中断，用户回答后自动恢复执行。不要把它当普通工具用——它会暂停整个流程等待用户回复。",
			func(ctx context.Context, input AskClarifyInput) (ToolResponse, error) {
				return ToolResponse{
					Success: true,
					Message: fmt.Sprintf("等待用户回答澄清问题：%s", input.Question),
				}, nil
			},
		)
		return t
	}
}
