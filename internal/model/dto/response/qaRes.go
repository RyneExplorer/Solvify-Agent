package response

import (
	"solvify-agent/internal/rag"
	"solvify-agent/internal/tool"
)

// AskResponse 描述问答响应数据
type AskResponse struct {
	Answer      string            `json:"answer"`
	TraceID     string            `json:"trace_id"`
	RAGHit      bool              `json:"rag_hit"`
	Documents   []rag.Document    `json:"documents,omitempty"`
	ToolResults []tool.CallResult `json:"tool_results,omitempty"`
}
