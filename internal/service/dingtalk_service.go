package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"solvify-agent/internal/integration/dingtalk"
	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/config"
	apperrors "solvify-agent/pkg/errors"
)

const (
	dingTalkOAuthScope        = "openid"
	dingTalkOAuthResponseType = "code"
	dingTalkOAuthPrompt       = "consent"
	dingTalkOAuthStateTTL     = 10 * time.Minute
)

// DingTalkOAuthClient 定义钉钉 OAuth 和知识库客户端能力
type DingTalkOAuthClient interface {
	ExchangeUserAccessToken(ctx context.Context, authCode string) (dingtalk.UserAccessToken, error)
	GetCurrentUserInfo(ctx context.Context, userAccessToken string) (dingtalk.UserInfo, error)
	GetWorkspace(ctx context.Context, operatorID, workspaceID string) (dingtalk.Workspace, error)
	ListWorkspaces(ctx context.Context, operatorID, nextToken string, maxResults int) ([]dingtalk.Workspace, string, error)
	ListNodes(ctx context.Context, operatorID, parentNodeID, nextToken string, maxResults int) ([]dingtalk.Node, string, error)
}

// DingTalkStateCache 定义扫码 state 缓存能力
type DingTalkStateCache interface {
	Get(ctx context.Context, key string, dest any) (bool, error)
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// dingtalkService 封装钉钉绑定业务用例
type dingtalkService struct {
	cfg                 config.DingTalkConfig
	dingtalkBindingRepo repository.DingTalkBindingRepository
	stateCache          DingTalkStateCache
	dingtalkClient      DingTalkOAuthClient
}

type dingTalkOAuthState struct {
	UserID string `json:"user_id"`
}

// NewDingTalkService 创建钉钉绑定服务
func NewDingTalkService(
	cfg config.DingTalkConfig,
	dingtalkBindingRepo repository.DingTalkBindingRepository,
	stateCache DingTalkStateCache,
	dingtalkClient DingTalkOAuthClient,
) DingTalkServiceInterface {
	return &dingtalkService{
		cfg:                 cfg,
		dingtalkBindingRepo: dingtalkBindingRepo,
		stateCache:          stateCache,
		dingtalkClient:      dingtalkClient,
	}
}

// OAuthConfig 生成前端内嵌二维码登录参数
func (s *dingtalkService) OAuthConfig(ctx context.Context, userID string) (dto.DingTalkOAuthConfigResponse, error) {
	if strings.TrimSpace(s.cfg.AppKey) == "" || strings.TrimSpace(s.cfg.OAuthRedirectURI) == "" {
		return dto.DingTalkOAuthConfigResponse{}, apperrors.NewDefault(apperrors.CodeDingTalkConfigMissing)
	}
	state, err := generateDingTalkState()
	if err != nil {
		return dto.DingTalkOAuthConfigResponse{}, err
	}
	if err := s.stateCache.Set(ctx, state, dingTalkOAuthState{UserID: userID}, dingTalkOAuthStateTTL); err != nil {
		return dto.DingTalkOAuthConfigResponse{}, err
	}
	return dto.DingTalkOAuthConfigResponse{
		ClientID:     strings.TrimSpace(s.cfg.AppKey),
		RedirectURI:  url.QueryEscape(strings.TrimSpace(s.cfg.OAuthRedirectURI)),
		Scope:        dingTalkOAuthScope,
		ResponseType: dingTalkOAuthResponseType,
		Prompt:       dingTalkOAuthPrompt,
		State:        state,
	}, nil
}

// ExchangeAuthCode 兑换授权码并保存钉钉绑定
func (s *dingtalkService) ExchangeAuthCode(ctx context.Context, userID string, req requestdto.DingTalkAuthCodeExchangeRequest) (dto.DingTalkBindingResponse, error) {
	state := strings.TrimSpace(req.State)
	authCode := strings.TrimSpace(req.AuthCode)
	if state == "" || authCode == "" {
		return dto.DingTalkBindingResponse{}, apperrors.New(apperrors.CodeBadRequest, "钉钉授权参数不能为空")
	}
	if err := s.validateState(ctx, userID, state); err != nil {
		return dto.DingTalkBindingResponse{}, err
	}
	userToken, err := s.dingtalkClient.ExchangeUserAccessToken(ctx, authCode)
	if err != nil {
		return dto.DingTalkBindingResponse{}, err
	}
	userInfo, err := s.dingtalkClient.GetCurrentUserInfo(ctx, userToken.AccessToken)
	if err != nil {
		return dto.DingTalkBindingResponse{}, err
	}
	if err := s.ensureUnionAvailable(ctx, userID, userInfo.UnionID); err != nil {
		return dto.DingTalkBindingResponse{}, err
	}
	binding := entity.DingTalkUserBinding{
		ID:          uuid.NewString(),
		UserID:      userID,
		DingOpenID:  strings.TrimSpace(userInfo.OpenID),
		DingUnionID: strings.TrimSpace(userInfo.UnionID),
		CorpID:      strings.TrimSpace(userToken.CorpID),
		Nickname:    strings.TrimSpace(userInfo.Nick),
		Avatar:      strings.TrimSpace(userInfo.AvatarURL),
	}
	if err := s.dingtalkBindingRepo.UpsertByUserID(ctx, binding); err != nil {
		return dto.DingTalkBindingResponse{}, err
	}
	return dingtalkBindingResponse(binding, true), nil
}

// Binding 查询当前用户钉钉绑定状态
func (s *dingtalkService) Binding(ctx context.Context, userID string) (dto.DingTalkBindingResponse, error) {
	binding, ok, err := s.dingtalkBindingRepo.FindByUserID(ctx, userID)
	if err != nil {
		return dto.DingTalkBindingResponse{}, err
	}
	if !ok {
		return dto.DingTalkBindingResponse{Bound: false}, nil
	}
	return dingtalkBindingResponse(binding, true), nil
}

// DeleteBinding 删除当前用户钉钉绑定
func (s *dingtalkService) DeleteBinding(ctx context.Context, userID string) error {
	_, err := s.dingtalkBindingRepo.DeleteByUserID(ctx, userID)
	return err
}

// ListWorkspaces 查询当前绑定账号可访问的钉钉知识库
func (s *dingtalkService) ListWorkspaces(ctx context.Context, userID, nextToken string, maxResults int) (dto.DingTalkWorkspaceListResponse, error) {
	binding, err := s.requireBinding(ctx, userID)
	if err != nil {
		return dto.DingTalkWorkspaceListResponse{}, err
	}
	items, next, err := s.dingtalkClient.ListWorkspaces(ctx, binding.DingUnionID, nextToken, maxResults)
	if err != nil {
		return dto.DingTalkWorkspaceListResponse{}, err
	}
	return dto.DingTalkWorkspaceListResponse{Workspaces: dingtalkWorkspaceResponses(items), NextToken: next}, nil
}

// ListNodes 查询当前绑定账号可访问的钉钉知识库节点
func (s *dingtalkService) ListNodes(ctx context.Context, userID, workspaceID, parentNodeID, nextToken string, maxResults int) (dto.DingTalkNodeListResponse, error) {
	binding, err := s.requireBinding(ctx, userID)
	if err != nil {
		return dto.DingTalkNodeListResponse{}, err
	}
	parentNodeID = strings.TrimSpace(parentNodeID)
	if parentNodeID == "" {
		workspace, err := s.dingtalkClient.GetWorkspace(ctx, binding.DingUnionID, strings.TrimSpace(workspaceID))
		if err != nil {
			return dto.DingTalkNodeListResponse{}, err
		}
		parentNodeID = workspace.RootNodeID
	}
	nodes, next, err := s.dingtalkClient.ListNodes(ctx, binding.DingUnionID, parentNodeID, nextToken, maxResults)
	if err != nil {
		return dto.DingTalkNodeListResponse{}, err
	}
	return dto.DingTalkNodeListResponse{Nodes: dingtalkNodeResponses(nodes), NextToken: next}, nil
}

// validateState 校验扫码 state 与当前用户一致
func (s *dingtalkService) validateState(ctx context.Context, userID, state string) error {
	var cached dingTalkOAuthState
	found, err := s.stateCache.Get(ctx, state, &cached)
	if err != nil {
		return err
	}
	if !found || cached.UserID != userID {
		return apperrors.New(apperrors.CodeBadRequest, "钉钉授权状态已失效")
	}
	return s.stateCache.Delete(ctx, state)
}

// ensureUnionAvailable 确保钉钉 unionId 没有绑定给其他用户
func (s *dingtalkService) ensureUnionAvailable(ctx context.Context, userID, unionID string) error {
	if strings.TrimSpace(unionID) == "" {
		return apperrors.New(apperrors.CodeDingTalkAPICallFailed, "钉钉用户 unionId 为空")
	}
	binding, ok, err := s.dingtalkBindingRepo.FindByUnionID(ctx, unionID)
	if err != nil {
		return err
	}
	if ok && binding.UserID != userID {
		return apperrors.New(apperrors.CodeConflict, "该钉钉账号已绑定其他用户")
	}
	return nil
}

// requireBinding 获取当前用户钉钉绑定
func (s *dingtalkService) requireBinding(ctx context.Context, userID string) (entity.DingTalkUserBinding, error) {
	binding, ok, err := s.dingtalkBindingRepo.FindByUserID(ctx, userID)
	if err != nil {
		return entity.DingTalkUserBinding{}, err
	}
	if !ok {
		return entity.DingTalkUserBinding{}, apperrors.New(apperrors.CodeBadRequest, "请先绑定钉钉账号")
	}
	return binding, nil
}

// generateDingTalkState 生成扫码登录随机 state
func generateDingTalkState() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", apperrors.NewWithErr(apperrors.CodeInternalError, "生成钉钉授权状态失败", err)
	}
	return hex.EncodeToString(buf), nil
}

// dingtalkBindingResponse 转换钉钉绑定响应
func dingtalkBindingResponse(binding entity.DingTalkUserBinding, bound bool) dto.DingTalkBindingResponse {
	return dto.DingTalkBindingResponse{
		Bound:       bound,
		DingOpenID:  binding.DingOpenID,
		DingUnionID: binding.DingUnionID,
		CorpID:      binding.CorpID,
		Nickname:    binding.Nickname,
		Avatar:      binding.Avatar,
	}
}

// dingtalkWorkspaceResponses 转换钉钉知识库列表响应
func dingtalkWorkspaceResponses(items []dingtalk.Workspace) []dto.DingTalkWorkspaceResponse {
	output := make([]dto.DingTalkWorkspaceResponse, 0, len(items))
	for _, item := range items {
		output = append(output, dto.DingTalkWorkspaceResponse{
			WorkspaceID: item.WorkspaceID,
			RootNodeID:  item.RootNodeID,
			Name:        item.Name,
			Type:        item.Type,
		})
	}
	return output
}

// dingtalkNodeResponses 转换钉钉节点列表响应
func dingtalkNodeResponses(items []dingtalk.Node) []dto.DingTalkNodeResponse {
	output := make([]dto.DingTalkNodeResponse, 0, len(items))
	for _, item := range items {
		output = append(output, dto.DingTalkNodeResponse{
			NodeID:      item.NodeID,
			WorkspaceID: item.WorkspaceID,
			Name:        item.Name,
			Type:        item.Type,
			URL:         item.URL,
			Size:        item.Size,
			ModifiedAt:  item.ModifiedAt,
		})
	}
	return output
}
