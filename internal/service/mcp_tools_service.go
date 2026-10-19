package service

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/tool"
	apperrors "solvify-agent/pkg/errors"
)

// Tools 只在用户主动展开或刷新时发现工具，不执行工具调用。
func (s *mcpService) Tools(ctx context.Context, userID, id string) (*response.MCPConnectionTools, error) {
	c, err := s.owned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	cfg, err := s.validate(ctx, c)
	if err != nil {
		return nil, err
	}
	disabled, err := tool.DisabledMCPTools(c.Config)
	if err != nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, err.Error())
	}
	tools, err := s.provider.GetTools(ctx, cfg)
	if err != nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, "MCP工具发现失败，请检查连接配置或稍后重试")
	}
	out := &response.MCPConnectionTools{Tools: []response.MCPConnectionTool{}, ConnectionEnabled: mcpInfo(c, false).EffectiveEnabled}
	for _, item := range tools {
		info, err := item.Info(ctx)
		if err != nil || info == nil || info.Name == "" {
			return nil, apperrors.New(apperrors.CodeInternalError, "MCP工具信息无效")
		}
		out.Tools = append(out.Tools, response.MCPConnectionTool{Name: info.Name, Description: info.Desc, IsEnabled: !disabled[info.Name], EffectiveEnabled: out.ConnectionEnabled && !disabled[info.Name]})
	}
	return out, nil
}

func (s *mcpService) UpdateTool(ctx context.Context, userID, id string, req request.UpdateMCPToolRequest) (*response.MCPToolState, error) {
	if req.IsEnabled == nil || strings.TrimSpace(req.Name) == "" || len(req.Name) > 256 || strings.ContainsFunc(req.Name, unicode.IsControl) {
		return nil, apperrors.New(apperrors.CodeBadRequest, "MCP工具开关参数错误")
	}
	c, err := s.owned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	disabled, err := tool.DisabledMCPTools(c.Config)
	if err != nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, err.Error())
	}
	if !*req.IsEnabled && !disabled[req.Name] && len(disabled) >= 128 {
		return nil, apperrors.New(apperrors.CodeBadRequest, "MCP停用工具数不能超过128个")
	}
	if *req.IsEnabled {
		// 启用必须是当前端点实际提供的工具；停用不依赖远端在线。
		list, err := s.Tools(ctx, userID, id)
		if err != nil {
			return nil, err
		}
		found := false
		for _, item := range list.Tools {
			if item.Name == req.Name {
				found = true
				break
			}
		}
		if !found {
			return nil, apperrors.New(apperrors.CodeBadRequest, "MCP未提供此工具，请刷新工具列表")
		}
	}
	if err = s.repo.SetToolEnabled(ctx, userID, id, req.Name, *req.IsEnabled); err != nil {
		if errors.Is(err, repository.ErrMCPToolStateChanged) {
			return nil, apperrors.New(apperrors.CodeBadRequest, err.Error())
		}
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	return &response.MCPToolState{Name: req.Name, IsEnabled: *req.IsEnabled}, nil
}
