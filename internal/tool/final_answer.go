package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// FinalAnswerTool 提交最终答案工具
type FinalAnswerTool struct{}

// NewFinalAnswerTool 创建最终答案工具
func NewFinalAnswerTool() *FinalAnswerTool {
	return &FinalAnswerTool{}
}

func (t *FinalAnswerTool) Name() string {
	return "final_answer"
}

func (t *FinalAnswerTool) Description() string {
	return "提交最终答案。当已有足够信息回答用户问题时调用此工具。"
}

func (t *FinalAnswerTool) Parameters() map[string]any {
	return map[string]any{
		"answer": map[string]any{
			"type":        "string",
			"description": "最终答案内容，使用 Markdown 格式",
			"required":    true,
		},
	}
}

func (t *FinalAnswerTool) Execute(_ context.Context, args string) (string, error) {
	var params struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if params.Answer == "" {
		return "", fmt.Errorf("answer 参数不能为空")
	}
	return params.Answer, nil
}
