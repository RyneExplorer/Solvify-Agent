package service

import (
	"context"

	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/logger"

	"golang.org/x/sync/errgroup"
)

// searchService 统一搜索服务实现
type searchService struct {
	messageRepo repository.ChatMessageRepo
	chunkRepo   repository.ChunkRepository
}

// NewSearchService 创建统一搜索服务
func NewSearchService(messageRepo repository.ChatMessageRepo, chunkRepo repository.ChunkRepository) SearchServiceInterface {
	return &searchService{
		messageRepo: messageRepo,
		chunkRepo:   chunkRepo,
	}
}

// Search 执行关键字搜索
func (s *searchService) Search(ctx context.Context, userID string, req *requestdto.SearchRequest) (*dto.SearchResponse, error) {
	topK := req.TopK
	if topK <= 0 {
		topK = 10
	}

	var chatResults []dto.ChatMessageSearchResult
	var docResults []dto.DocumentSearchResult
	var chatErr, docErr error

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		chatResults, chatErr = s.searchChatMessages(ctx, userID, req.Query, topK)
		return nil
	})
	g.Go(func() error {
		docResults, docErr = s.searchDocuments(ctx, userID, req.Query, topK)
		return nil
	})
	_ = g.Wait()

	if chatErr != nil {
		logger.Warnf("历史对话关键字搜索失败: userID=%s, err=%v", userID, chatErr)
	}
	if docErr != nil {
		logger.Warnf("知识库文档关键字搜索失败: userID=%s, err=%v", userID, docErr)
	}

	return &dto.SearchResponse{
		ChatMessages: chatResults,
		Documents:    docResults,
	}, nil
}

// searchChatMessages 历史对话关键字搜索
func (s *searchService) searchChatMessages(ctx context.Context, userID, query string, topK int) ([]dto.ChatMessageSearchResult, error) {
	rows, err := s.messageRepo.SearchByKeyword(ctx, userID, query, topK)
	if err != nil {
		return nil, err
	}

	results := make([]dto.ChatMessageSearchResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, dto.ChatMessageSearchResult{
			ID:           row.ID,
			SessionID:    row.SessionID,
			SessionTitle: row.SessionTitle,
			Role:         row.Role,
			Content:      row.Content,
			Score:        row.Score,
			CreatedAt:    row.CreatedAt,
		})
	}
	return results, nil
}

// searchDocuments 知识库文档关键字搜索
func (s *searchService) searchDocuments(ctx context.Context, userID, query string, topK int) ([]dto.DocumentSearchResult, error) {
	rows, err := s.chunkRepo.SearchByKeyword(ctx, userID, query, topK)
	if err != nil {
		return nil, err
	}

	results := make([]dto.DocumentSearchResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, dto.DocumentSearchResult{
			ID:              row.ID,
			KnowledgeBaseID: row.KnowledgeBaseID,
			DocumentID:      row.DocumentID,
			Title:           row.Title,
			Content:         row.Content,
			Score:           row.Score,
		})
	}
	return results, nil
}
