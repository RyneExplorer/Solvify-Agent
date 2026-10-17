package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/tool"
	apperrors "solvify-agent/pkg/errors"
)

type mcpTemplateStore interface {
	GetByID(ctx context.Context, id string) (*entity.ToolProvider, error)
	ListMCPProviders(ctx context.Context) ([]repository.MCPServerRow, error)
}

type mcpService struct {
	repo      repository.MCPRepository
	templates mcpTemplateStore
	provider  tool.MultiToolProvider
}

func NewMCPService(repo repository.MCPRepository, templates mcpTemplateStore, provider tool.MultiToolProvider) MCPService {
	return &mcpService{repo, templates, provider}
}
func (s *mcpService) Templates(ctx context.Context) ([]response.MCPTemplateInfo, error) {
	rows, err := s.templates.ListMCPProviders(ctx)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	out := make([]response.MCPTemplateInfo, 0, len(rows))
	for _, row := range rows {
		p := &row.ToolProvider
		if p.IsEnabled && p.ToolType.IsEnabled {
			out = append(out, response.MCPTemplateInfo{ID: p.ID, Name: p.Name, Description: p.Description, ConfigSchema: json.RawMessage(p.ConfigSchema)})
		}
	}
	return out, nil
}
func (s *mcpService) List(ctx context.Context, userID string) ([]response.MCPConnectionInfo, error) {
	rows, err := s.repo.List(ctx, userID)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	out := make([]response.MCPConnectionInfo, 0, len(rows))
	for _, c := range rows {
		out = append(out, *mcpInfo(&c, false))
	}
	return out, nil
}
func mcpInfo(c *entity.MCPConnection, detail bool) *response.MCPConnectionInfo {
	out := &response.MCPConnectionInfo{ID: c.ID, TemplateID: c.TemplateID, DisplayName: c.DisplayName, IsEnabled: c.IsEnabled, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
	if detail {
		out.Connection = json.RawMessage(c.Connection)
		out.Config = json.RawMessage(c.Config)
	}
	return out
}
func (s *mcpService) owned(ctx context.Context, userID, id string) (*entity.MCPConnection, error) {
	c, err := s.repo.Get(ctx, userID, id)
	if err != nil {
		return nil, apperrors.NotFoundOrInternal(apperrors.CodeBadRequest, err)
	}
	if c.UserID != userID {
		return nil, apperrors.NewDefault(apperrors.CodeBadRequest)
	}
	return c, nil
}
func (s *mcpService) Get(ctx context.Context, userID, id string) (*response.MCPConnectionInfo, error) {
	c, err := s.owned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return mcpInfo(c, true), nil
}
func objectJSON(raw []byte) (datatypes.JSON, error) {
	if len(raw) == 0 {
		return datatypes.JSON(`{}`), nil
	}
	var value map[string]interface{}
	if len(raw) > 65536 || json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, "MCP配置必须为JSON对象，且不超过64KB")
	}
	canonical, err := json.Marshal(value)
	return datatypes.JSON(canonical), err
}
func (s *mcpService) validate(ctx context.Context, c *entity.MCPConnection) (*tool.MCPProviderConfig, error) {
	if strings.TrimSpace(c.DisplayName) == "" || len([]rune(c.DisplayName)) > 100 {
		return nil, apperrors.New(apperrors.CodeBadRequest, "请填写MCP名称（不超过100字）")
	}
	if c.TemplateID != nil {
		if string(c.Connection) != "{}" {
			return nil, apperrors.New(apperrors.CodeBadRequest, "预置MCP只能填写配置字段，不能覆盖连接地址")
		}
		p, err := s.templates.GetByID(ctx, *c.TemplateID)
		if err != nil {
			return nil, apperrors.NotFoundOrInternal(apperrors.CodeToolProviderNotFound, err)
		}
		c.Template = p
	}
	cfg, _, err := tool.ResolveMCPConnection(c)
	if err != nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, err.Error())
	}
	if err = s.provider.Validate(&tool.ExecuteConfig{ProviderConfig: &tool.ProviderConfig{MCP: cfg}}); err != nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, "MCP连接配置无效")
	}
	return cfg, nil
}
func (s *mcpService) draft(ctx context.Context, userID string, req request.CreateMCPConnectionRequest) (*entity.MCPConnection, *tool.MCPProviderConfig, error) {
	conn, err := objectJSON(req.Connection)
	if err != nil {
		return nil, nil, err
	}
	fields, err := objectJSON(req.Config)
	if err != nil {
		return nil, nil, err
	}
	c := &entity.MCPConnection{UserID: userID, TemplateID: req.TemplateID, DisplayName: strings.TrimSpace(req.DisplayName), Connection: conn, Config: fields, IsEnabled: true}
	// 草稿身份只用于隔离探测会话，不写数据库。
	c.ID = "draft"
	cfg, err := s.validate(ctx, c)
	return c, cfg, err
}
func (s *mcpService) Create(ctx context.Context, userID string, req request.CreateMCPConnectionRequest) (*response.MCPConnectionInfo, error) {
	c, _, err := s.draft(ctx, userID, req)
	if err != nil {
		return nil, err
	}
	c.ID = ""
	if err = s.repo.Create(ctx, c); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, apperrors.New(apperrors.CodeBadRequest, "此MCP预置已配置，请编辑现有连接")
		}
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	return mcpInfo(c, false), nil
}
func (s *mcpService) Update(ctx context.Context, userID, id string, req request.UpdateMCPConnectionRequest) (*response.MCPConnectionInfo, error) {
	c, err := s.owned(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if req.DisplayName != nil {
		c.DisplayName = strings.TrimSpace(*req.DisplayName)
	}
	if req.Connection != nil {
		c.Connection, err = objectJSON(*req.Connection)
		if err != nil {
			return nil, err
		}
	}
	if req.Config != nil {
		c.Config, err = objectJSON(*req.Config)
		if err != nil {
			return nil, err
		}
	}
	if req.IsEnabled != nil {
		c.IsEnabled = *req.IsEnabled
	}
	// 停用和删除不能因远端预置失效而被阻断；修改或重新启用必须重新验证。
	if c.IsEnabled || req.Config != nil || req.Connection != nil {
		if _, err = s.validate(ctx, c); err != nil {
			return nil, err
		}
	}
	if err = s.repo.Update(ctx, c); err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	return mcpInfo(c, false), nil
}
func (s *mcpService) Delete(ctx context.Context, userID, id string) error {
	if _, err := s.owned(ctx, userID, id); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, userID, id); err != nil {
		return apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	return nil
}
func (s *mcpService) Probe(ctx context.Context, userID string, req request.ProbeMCPConnectionRequest) (*response.TestResult, error) {
	start := time.Now()
	var cfg *tool.MCPProviderConfig
	var err error
	if req.ID != "" {
		var c *entity.MCPConnection
		c, err = s.owned(ctx, userID, req.ID)
		if err == nil {
			cfg, err = s.validate(ctx, c)
		}
	} else {
		_, cfg, err = s.draft(ctx, userID, request.CreateMCPConnectionRequest{DisplayName: req.DisplayName, TemplateID: req.TemplateID, Connection: req.Connection, Config: req.Config})
	}
	if err != nil {
		return nil, err
	}
	tools, err := s.provider.GetTools(ctx, &tool.ProviderConfig{MCP: cfg})
	out := &response.TestResult{Success: err == nil, ResponseTime: time.Since(start).Milliseconds()}
	if err != nil {
		out.Message = "MCP连接或工具发现失败，请检查地址、凭据及服务状态"
		out.Error = "连接失败"
		return out, nil
	}
	out.MCPTools = make([]response.MCPToolInfo, 0, len(tools))
	for _, t := range tools {
		info, e := t.Info(ctx)
		if e != nil || info == nil {
			return nil, apperrors.New(apperrors.CodeInternalError, "MCP工具信息无效")
		}
		out.MCPTools = append(out.MCPTools, response.MCPToolInfo{Name: info.Name, Description: info.Desc})
	}
	out.MCPToolCount = len(out.MCPTools)
	out.Message = fmt.Sprintf("连接成功，发现%d个工具；尚未执行工具调用", out.MCPToolCount)
	return out, nil
}
