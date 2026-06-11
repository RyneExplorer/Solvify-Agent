package agent

import (
	"solvify-agent/internal/rag"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
)

// Engine ReAct Agent 引擎
type Engine struct {
	retriever rag.Retriever
	registry  *tool.Registry
	cfg       config.AgentConfig
}

// NewEngine 创建 Agent 引擎
func NewEngine(
	retriever rag.Retriever,
	registry *tool.Registry,
	cfg config.AgentConfig,
) *Engine {
	return &Engine{
		retriever: retriever,
		registry:  registry,
		cfg:       cfg,
	}
}
