package service

import (
	"context"

	"github.com/cloudwego/eino/components/model"

	"solvify-agent/internal/model/entity"
)

// BuildContextConfig 构建上下文的配置
type BuildContextConfig struct {
	MaxTokens         int // 历史消息最大 token 预算（0 表示按模型窗口自动计算）
	MaxMemories       int
	MaxRecentMessages int
	RetrievalBudget   int // 知识库检索上下文最大 token 预算
	MemoryBudget      int // 用户记忆最大 token 预算
}

// EnhancedContext 增强后的对话上下文
type EnhancedContext struct {
	History         []entity.ChatMessage
	Summary         *entity.ChatSummary
	Memories        []entity.UserMemory
	HistoryBudget   int // 实际使用的历史消息 token 预算
	RetrievalBudget int // 实际使用的检索上下文 token 预算
}

// ContextServiceInterface 上下文管理服务接口
type ContextServiceInterface interface {
	// BuildContext 构建增强后的对话上下文
	BuildContext(ctx context.Context, userID, sessionID, currentQuery string, cfg BuildContextConfig, chatModel model.BaseChatModel) (*EnhancedContext, error)

	// SummarizeSession 对会话生成或更新摘要
	SummarizeSession(ctx context.Context, sessionID string, chatModel model.BaseChatModel) (*entity.ChatSummary, error)

	// ExtractMemories 从消息中提取用户长期记忆
	ExtractMemories(ctx context.Context, userID, sessionID string, messages []entity.ChatMessage, chatModel model.BaseChatModel) ([]entity.UserMemory, error)
}
