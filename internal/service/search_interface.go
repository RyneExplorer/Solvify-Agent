package service

import (
	"context"

	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
)

// SearchServiceInterface 统一搜索服务接口
type SearchServiceInterface interface {
	// Search 执行关键字搜索：历史对话 + 知识库文档
	Search(ctx context.Context, userID string, req *requestdto.SearchRequest) (*dto.SearchResponse, error)
}
