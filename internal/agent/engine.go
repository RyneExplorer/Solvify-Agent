package agent

import (
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
)

// KnowledgeSearchFactory 创建带用户上下文的知识库搜索工具
type KnowledgeSearchFactory func(userID string, kbIDs []string) *tool.KnowledgeSearchTool

// Engine 基于 eino ReAct Agent 的推理引擎
//
// 职责：自主决定工具调用时机，执行 Think → Act → Observe 推理循环
// 内部使用 eino flow/agent/react 实现，不再手写循环
//
// 工具来源：
//   - knowledge_search: 内置，通过 KnowledgeSearchFactory 按请求创建（需要 userID + kbIDs）
//   - 用户配置工具: 通过 ToolFactory.CreateAgentTools 动态加载（来自 DB → Redis 缓存）
type Engine struct {
	knowledgeSearchFactory KnowledgeSearchFactory
	toolFactory            tool.ToolFactory
	cfg                    config.AgentConfig
}

// NewEngine 创建 Agent 引擎
func NewEngine(
	knowledgeSearchFactory KnowledgeSearchFactory,
	toolFactory tool.ToolFactory,
	cfg config.AgentConfig,
) *Engine {
	return &Engine{
		knowledgeSearchFactory: knowledgeSearchFactory,
		toolFactory:            toolFactory,
		cfg:                    cfg,
	}
}
