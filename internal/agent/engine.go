package agent

import (
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
)

// KnowledgeSearchFactory 创建带用户上下文的知识库搜索工具
type KnowledgeSearchFactory func(userID string, kbIDs []string) *tool.KnowledgeSearchTool

// GrepChunksFactory 创建带用户上下文的关键词搜索工具
type GrepChunksFactory func(userID string, kbIDs []string) *tool.GrepChunksTool

// GetDocumentInfoFactory 创建带用户上下文的文档信息工具
type GetDocumentInfoFactory func(userID string) *tool.GetDocumentInfoTool

// ListKnowledgeChunksFactory 创建带用户上下文的文档列表工具
type ListKnowledgeChunksFactory func(userID string, kbIDs []string) *tool.ListKnowledgeChunksTool

// ListKnowledgeBasesFactory 创建带用户上下文的知识库列表工具
type ListKnowledgeBasesFactory func(userID string) *tool.ListKnowledgeBasesTool

// Engine 基于 eino ReAct Agent 的推理引擎
//
// 职责：自主决定工具调用时机，执行 Think → Act → Observe 推理循环
// 内部使用 eino flow/agent/react 实现，不再手写循环
//
// 工具来源：
//   - knowledge_search: 内置，通过 KnowledgeSearchFactory 按请求创建（需要 userID + kbIDs）
//   - grep_chunks: 内置，通过 GrepChunksFactory 按请求创建
//   - get_document_info: 内置，通过 GetDocumentInfoFactory 按请求创建
//   - list_knowledge_chunks: 内置，通过 ListKnowledgeChunksFactory 按请求创建
//   - list_knowledge_bases: 内置，通过 ListKnowledgeBasesFactory 按请求创建
//   - 用户配置工具: 通过 ToolFactory.CreateAgentTools 动态加载（来自 DB → Redis 缓存）
type Engine struct {
	knowledgeSearchFactory     KnowledgeSearchFactory
	grepChunksFactory          GrepChunksFactory
	getDocumentInfoFactory     GetDocumentInfoFactory
	listKnowledgeChunksFactory ListKnowledgeChunksFactory
	listKnowledgeBasesFactory  ListKnowledgeBasesFactory
	toolFactory                tool.ToolFactory
	cfg                        config.AgentConfig
}

// NewEngine 创建 Agent 引擎
func NewEngine(
	knowledgeSearchFactory KnowledgeSearchFactory,
	grepChunksFactory GrepChunksFactory,
	getDocumentInfoFactory GetDocumentInfoFactory,
	listKnowledgeChunksFactory ListKnowledgeChunksFactory,
	listKnowledgeBasesFactory ListKnowledgeBasesFactory,
	toolFactory tool.ToolFactory,
	cfg config.AgentConfig,
) *Engine {
	return &Engine{
		knowledgeSearchFactory:     knowledgeSearchFactory,
		grepChunksFactory:          grepChunksFactory,
		getDocumentInfoFactory:     getDocumentInfoFactory,
		listKnowledgeChunksFactory: listKnowledgeChunksFactory,
		listKnowledgeBasesFactory:  listKnowledgeBasesFactory,
		toolFactory:                toolFactory,
		cfg:                        cfg,
	}
}
