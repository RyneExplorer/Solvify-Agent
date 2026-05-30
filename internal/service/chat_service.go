package service

import (
	"context"
	"fmt"

	"solvify-agent/internal/agent"
	requestdto "solvify-agent/internal/model/dto/request"
	responsedto "solvify-agent/internal/model/dto/response"
)

// ChatService 封装知识助理业务用例
type ChatService struct {
	agent *agent.KnowledgeAgent
}

// NewChatService 创建知识助理业务服务
func NewChatService(agent *agent.KnowledgeAgent) *ChatService {
	return &ChatService{agent: agent}
}

// Ask 调用 Agent 完成一次问答
func (s *ChatService) Ask(ctx context.Context, req requestdto.AskRequest) (responsedto.AskResponse, error) {
	if s.agent == nil {
		return responsedto.AskResponse{}, fmt.Errorf("knowledge agent is not initialized")
	}

	result, err := s.agent.Run(ctx, agent.Request{
		Question: req.Question,
		UseRAG:   req.UseRAG,
		UseTools: req.UseTools,
	})
	if err != nil {
		return responsedto.AskResponse{}, err
	}

	return responsedto.AskResponse{
		Answer:      result.Answer,
		TraceID:     result.TraceID,
		RAGHit:      result.RAGHit,
		Documents:   result.Documents,
		ToolResults: result.ToolResults,
	}, nil
}
