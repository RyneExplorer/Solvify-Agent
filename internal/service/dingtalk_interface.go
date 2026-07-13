package service

import (
	"context"

	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
)

// DingTalkServiceInterface 定义钉钉绑定业务能力
type DingTalkServiceInterface interface {
	OAuthConfig(ctx context.Context, userID string) (dto.DingTalkOAuthConfigResponse, error)
	ExchangeAuthCode(ctx context.Context, userID string, req requestdto.DingTalkAuthCodeExchangeRequest) (dto.DingTalkBindingResponse, error)
	Binding(ctx context.Context, userID string) (dto.DingTalkBindingResponse, error)
	DeleteBinding(ctx context.Context, userID string) error
	ListWorkspaces(ctx context.Context, userID, nextToken string, maxResults int) (dto.DingTalkWorkspaceListResponse, error)
	ListNodes(ctx context.Context, userID, workspaceID, parentNodeID, nextToken string, maxResults int) (dto.DingTalkNodeListResponse, error)
}
