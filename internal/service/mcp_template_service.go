package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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
	"solvify-agent/pkg/logger"
)

type mcpTemplateService struct {
	repo   repository.MCPTemplateRepository
	client tool.MCPClient
}

func NewMCPTemplateService(repo repository.MCPTemplateRepository, client tool.MCPClient) MCPTemplateService {
	return &mcpTemplateService{repo, client}
}

var mcpTemplateKey = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,50}$`)

func templateInfo(t *entity.MCPTemplate, detail bool) *response.MCPTemplateAdminInfo {
	out := &response.MCPTemplateAdminInfo{ID: t.ID, Key: t.Key, Name: t.Name, Description: t.Description, ConfigSchema: json.RawMessage(t.ConfigSchema), IsEnabled: t.IsEnabled, IsSystem: t.IsSystem, MCPToolManifest: json.RawMessage(t.MCPToolManifest)}
	if detail {
		out.Connection = json.RawMessage(t.Connection)
	}
	return out
}
func (s *mcpTemplateService) validate(t *entity.MCPTemplate) error {
	if strings.TrimSpace(t.Name) == "" || len([]rune(t.Name)) > 100 {
		return apperrors.New(apperrors.CodeBadRequest, "请填写MCP预置名称（不超过100字）")
	}
	var cfg tool.MCPConfig
	if json.Unmarshal(t.Connection, &cfg) != nil || strings.Contains(cfg.URL, "{{") {
		return apperrors.New(apperrors.CodeBadRequest, "MCP预置地址必须固定，连接配置须为JSON对象")
	}
	if err := s.client.Validate(&cfg); err != nil {
		return apperrors.New(apperrors.CodeBadRequest, "MCP连接配置无效，请检查地址、请求头及超时")
	}
	if len(t.ConfigSchema) > 0 && string(t.ConfigSchema) != "null" {
		var schema struct {
			Type       string `json:"type"`
			Properties map[string]struct {
				Type string `json:"type"`
			} `json:"properties"`
			Required []string `json:"required"`
		}
		if json.Unmarshal(t.ConfigSchema, &schema) != nil || schema.Type != "object" {
			return apperrors.New(apperrors.CodeBadRequest, "MCP用户字段定义必须为对象Schema")
		}
		for key, f := range schema.Properties {
			if !regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`).MatchString(key) || (f.Type != "string" && f.Type != "number" && f.Type != "integer" && f.Type != "boolean") {
				return apperrors.New(apperrors.CodeBadRequest, "MCP用户字段名称或类型无效")
			}
		}
		for _, key := range schema.Required {
			if _, ok := schema.Properties[key]; !ok {
				return apperrors.New(apperrors.CodeBadRequest, "MCP必填字段未定义")
			}
		}
	}
	return nil
}
func (s *mcpTemplateService) Create(ctx context.Context, req request.CreateMCPTemplateRequest) (*response.MCPTemplateAdminInfo, error) {
	if !mcpTemplateKey.MatchString(req.Key) {
		return nil, apperrors.New(apperrors.CodeBadRequest, "MCP标识须为1至50个字母、数字、下划线或连字符")
	}
	connection, err := objectJSON(req.Connection)
	if err != nil {
		return nil, err
	}
	t := &entity.MCPTemplate{Key: req.Key, ToolPrefix: "mcp_" + req.Key, Name: strings.TrimSpace(req.Name), Description: req.Description, Connection: connection, ConfigSchema: datatypes.JSON(req.ConfigSchema), IsEnabled: true}
	if err = s.validate(t); err != nil {
		return nil, err
	}
	if err = s.repo.Create(ctx, t); err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, apperrors.New(apperrors.CodeMCPTemplateExists, "MCP预置标识已存在")
		}
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	return templateInfo(t, false), nil
}
func (s *mcpTemplateService) get(ctx context.Context, id string) (*entity.MCPTemplate, error) {
	t, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, apperrors.NotFoundOrInternal(apperrors.CodeMCPTemplateNotFound, err)
	}
	return t, nil
}
func (s *mcpTemplateService) Update(ctx context.Context, id string, req request.UpdateMCPTemplateRequest) (*response.MCPTemplateAdminInfo, error) {
	t, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.IsSystem && (req.Name != nil || req.Description != nil || req.ConfigSchema != nil) {
		return nil, apperrors.New(apperrors.CodeBadRequest, "部署预置仅允许修改超时和启停状态")
	}
	if req.Name != nil {
		t.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		t.Description = *req.Description
	}
	if req.ConfigSchema != nil {
		t.ConfigSchema = datatypes.JSON(*req.ConfigSchema)
	}
	if req.Connection != nil {
		raw := *req.Connection
		if t.IsSystem {
			raw, err = mergeSystemMCPTimeouts(t.Connection, raw)
			if err != nil {
				return nil, apperrors.New(apperrors.CodeBadRequest, err.Error())
			}
		}
		t.Connection, err = objectJSON(raw)
		if err != nil {
			return nil, err
		}
	}
	if req.IsEnabled != nil {
		t.IsEnabled = *req.IsEnabled
	}
	// 故障预置仍可停用；修改配置与重新启用必须验证。
	if t.IsEnabled || req.Connection != nil || req.ConfigSchema != nil {
		if err = s.validate(t); err != nil {
			return nil, err
		}
	}
	if err = s.repo.Update(ctx, t); err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	return templateInfo(t, false), nil
}
func (s *mcpTemplateService) Delete(ctx context.Context, id string) error {
	t, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	if t.IsSystem {
		return apperrors.New(apperrors.CodeBadRequest, "部署预置不支持删除")
	}
	// 用户绑定由数据库外键在同一原子操作中级联删除。
	if err = s.repo.Delete(ctx, id); err != nil {
		return apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	return nil
}
func (s *mcpTemplateService) GetByID(ctx context.Context, id string) (*response.MCPTemplateAdminInfo, error) {
	t, err := s.get(ctx, id)
	if err != nil {
		return nil, err
	}
	return templateInfo(t, true), nil
}
func (s *mcpTemplateService) List(ctx context.Context) ([]response.MCPTemplateAdminInfo, error) {
	rows, err := s.repo.List(ctx)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	out := make([]response.MCPTemplateAdminInfo, 0, len(rows))
	for _, t := range rows {
		out = append(out, *templateInfo(&t, false))
	}
	return out, nil
}
func (s *mcpTemplateService) Probe(ctx context.Context, req request.ProbeMCPTemplateRequest) (*response.TestResult, error) {
	raw := req.Connection
	if req.ID != "" {
		if len(raw) > 0 {
			return nil, apperrors.New(apperrors.CodeBadRequest, "请选择已保存预置或传入草稿连接")
		}
		t, err := s.get(ctx, req.ID)
		if err != nil {
			return nil, err
		}
		raw = json.RawMessage(t.Connection)
	}
	obj, err := objectJSON(raw)
	if err != nil {
		return nil, err
	}
	var cfg tool.MCPConfig
	if json.Unmarshal(obj, &cfg) != nil || s.client.Validate(&cfg) != nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, "MCP连接配置无效")
	}
	for _, value := range cfg.Headers {
		if strings.Contains(value, "{{") {
			return nil, apperrors.New(apperrors.CodeBadRequest, "预置需要用户凭据，请在个人MCP配置中测试")
		}
	}
	cfg.ScopeKey = "admin-probe"
	start := time.Now()
	tools, err := s.client.GetTools(ctx, &cfg)
	out := &response.TestResult{Success: err == nil, ResponseTime: time.Since(start).Milliseconds(), MCPTools: []response.MCPToolInfo{}}
	if err != nil {
		out.Message = "MCP连接或工具发现失败，请检查地址、凭据及服务状态"
		out.Error = "连接失败"
		return out, nil
	}
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil || info == nil {
			return nil, apperrors.New(apperrors.CodeInternalError, "MCP工具信息无效")
		}
		out.MCPTools = append(out.MCPTools, response.MCPToolInfo{Name: info.Name, Description: info.Desc})
	}
	out.MCPToolCount = len(out.MCPTools)
	if req.ID != "" {
		manifest, _ := json.Marshal(out.MCPTools)
		if err := s.repo.UpdateManifest(ctx, req.ID, obj, manifest); err != nil {
			logger.Warnf("保存MCP工具清单失败: template_id=%s", req.ID)
		}
	}
	out.Message = fmt.Sprintf("连接成功，发现%d个工具；尚未执行工具调用", len(out.MCPTools))
	return out, nil
}
