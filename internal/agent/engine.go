package agent

import (
	"solvify-agent/internal/observability"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
)

type KnowledgeSearchFactory func(userID string, kbIDs []string) *tool.KnowledgeSearchTool

type GrepChunksFactory func(userID string, kbIDs []string) *tool.GrepChunksTool

type GetDocumentInfoFactory func(userID string) *tool.GetDocumentInfoTool

type ListKnowledgeChunksFactory func(userID string, kbIDs []string) *tool.ListKnowledgeChunksTool

type ListKnowledgeBasesFactory func(userID string) *tool.ListKnowledgeBasesTool

type Engine struct {
	knowledgeSearchFactory     KnowledgeSearchFactory
	grepChunksFactory          GrepChunksFactory
	getDocumentInfoFactory     GetDocumentInfoFactory
	listKnowledgeChunksFactory ListKnowledgeChunksFactory
	listKnowledgeBasesFactory  ListKnowledgeBasesFactory
	toolFactory                tool.ToolFactory
	cfg                        config.AgentConfig
	obs                        observability.Recorder
}

func NewEngine(
	knowledgeSearchFactory KnowledgeSearchFactory,
	grepChunksFactory GrepChunksFactory,
	getDocumentInfoFactory GetDocumentInfoFactory,
	listKnowledgeChunksFactory ListKnowledgeChunksFactory,
	listKnowledgeBasesFactory ListKnowledgeBasesFactory,
	toolFactory tool.ToolFactory,
	cfg config.AgentConfig,
	obs ...observability.Recorder,
) *Engine {
	e := &Engine{
		knowledgeSearchFactory:     knowledgeSearchFactory,
		grepChunksFactory:          grepChunksFactory,
		getDocumentInfoFactory:     getDocumentInfoFactory,
		listKnowledgeChunksFactory: listKnowledgeChunksFactory,
		listKnowledgeBasesFactory:  listKnowledgeBasesFactory,
		toolFactory:                toolFactory,
		cfg:                        cfg,
	}
	if len(obs) > 0 && obs[0] != nil {
		e.obs = obs[0]
	}
	return e
}

func (e *Engine) WithObservability(obs observability.Recorder) {
	e.obs = obs
}
