package agent

import (
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
)

// KnowledgeSearchFactory 创建带用户上下文的知识库搜索工具
type KnowledgeSearchFactory func(userID string, kbIDs []string) *tool.KnowledgeSearchTool

// Engine ReAct Agent 引擎
//
// 职责：自主决定工具调用时机，执行 Think → Analyze → Act → Observe 推理循环
// 不做预检索，knowledge_search 作为工具由 LLM 自行决定何时调用
type Engine struct {
	registry               *tool.Registry
	knowledgeSearchFactory KnowledgeSearchFactory
	cfg                    config.AgentConfig
}

// NewEngine 创建 Agent 引擎
func NewEngine(
	registry *tool.Registry,
	knowledgeSearchFactory KnowledgeSearchFactory,
	cfg config.AgentConfig,
) *Engine {
	return &Engine{
		registry:               registry,
		knowledgeSearchFactory: knowledgeSearchFactory,
		cfg:                    cfg,
	}
}
