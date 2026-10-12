package service

import (
	"context"

	"github.com/cloudwego/eino/components/model"

	"solvify-agent/internal/model/entity"
)

// BuildContextConfig 构建上下文的配置
type BuildContextConfig struct {
	MaxTokens          int    // 历史消息总 token 预算（0 表示按模型窗口自动计算）；近期对话与相关历史各自按比例分
	MaxMemories        int
	MaxRecentMessages int
	RetrievalBudget    int // 知识库检索上下文最大 token 预算
	MemoryBudget       int // 用户记忆最大 token 预算
	// QueryEmbedding 是**当前问题**的向量，由调用方算好传进来（决策 ①）。
	// 调用方预算好、传进来是本项目的既有范式：contextService 不再持有一份 embed 客户端。
	// 为空时语义召回通道直接关闭，关键词通道不受影响。
	QueryEmbedding []float64
	ModelName      string // 本次要发送到的具体模型名（用于 tiktoken 选 BPE 编码；未知模型回退 cl100k_base）
	ToolsTokens    int    // 深度模式/带工具调用时，预扣工具定义的 token，不参与历史预算
}

// EnhancedContext 增强后的对话上下文
type EnhancedContext struct {
	// History 是近期对话（干净原文）。摘要与记忆抽取复用它，所以**不带任何召回标注**。
	History []entity.ChatMessage
	// RecallHistory 是语义/关键词召回的相关历史，与 History 是两个独立容器：
	// 各自按自己的预算裁剪，不合并后再裁 —— 合并后按时间从尾部裁，召回的老消息第一个被裁掉。
	// 只有内容会被加上「召回片段」标注后进模型，History 侧保持原文。
	RecallHistory   []entity.ChatMessage
	Summary         *entity.ChatSummary
	Memories        []entity.UserMemory
	UserCtx         UserContext
	HistoryBudget   int // 近期对话实际使用的 token 预算
	RecallBudget    int // 相关历史实际使用的 token 预算
	RetrievalBudget int // 实际使用的检索上下文 token 预算
	Profile         *entity.User           // 当前用户画像实体（来源：user表扩展字段）
	Preference      *entity.UserPreference // 当前用户偏好（来源：user_preferences 表）
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
