package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/datatypes"

	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/tool"
	apperrors "solvify-agent/pkg/errors"
)

type toolProviderService struct {
	repo     repository.ToolProviderRepository
	typeRepo repository.ToolTypeRepository
	registry tool.ProviderRegistry
}

// NewToolProviderService 创建工具供应商服务实例
func NewToolProviderService(
	repo repository.ToolProviderRepository,
	typeRepo repository.ToolTypeRepository,
	registry tool.ProviderRegistry,
) ToolProviderService {
	return &toolProviderService{
		repo:     repo,
		typeRepo: typeRepo,
		registry: registry,
	}
}

func (s *toolProviderService) Create(ctx context.Context, req request.CreateToolProviderRequest) (*response.ToolProviderInfo, error) {
	// 检查工具类型是否存在
	_, err := s.typeRepo.GetByID(ctx, req.ToolTypeID)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeToolTypeNotFound)
	}

	// 检查 provider_type 是否已注册
	if s.registry.Get(req.ProviderType) == nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, "供应商类型未注册")
	}

	// 检查 provider_key 在当前 tool_type 下是否已存在
	exists, err := s.repo.ExistsByKey(ctx, req.ToolTypeID, req.ProviderKey)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	if exists {
		return nil, apperrors.New(apperrors.CodeToolProviderExists, "供应商标识已存在")
	}

	// 规范化 provider_config：MCP 类型自动包装到 {mcp: ...} 结构并校验
	providerConfig := req.ProviderConfig
	if req.ProviderType == "mcp" && len(req.ProviderConfig) > 0 && string(req.ProviderConfig) != "null" {
		wrapped, err := normalizeMCPProviderConfig(req.ProviderConfig, s.registry)
		if err != nil {
			return nil, apperrors.New(apperrors.CodeBadRequest, "MCP 配置错误: "+err.Error())
		}
		providerConfig = wrapped
	}

	provider := &entity.ToolProvider{
		ToolTypeID:     req.ToolTypeID,
		ProviderKey:    req.ProviderKey,
		Name:           req.Name,
		Description:    req.Description,
		ProviderType:   req.ProviderType,
		ConfigSchema:   datatypes.JSON(req.ConfigSchema),
		InputSchema:    datatypes.JSON(req.InputSchema),
		ProviderConfig: datatypes.JSON(providerConfig),
		AdminConfig:    datatypes.JSON(req.AdminConfig),
		RateLimit:      datatypes.JSON(req.RateLimit),
		IsEnabled:      true,
	}

	if err := s.repo.Create(ctx, provider); err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return s.toProviderInfo(provider), nil
}

func (s *toolProviderService) Update(ctx context.Context, id string, req request.UpdateToolProviderRequest) (*response.ToolProviderInfo, error) {
	provider, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeToolProviderNotFound)
	}

	// 系统预置保护：不允许修改 provider_type 和 provider_config
	if provider.IsSystem {
		if req.ProviderType != nil || req.ProviderConfig != nil {
			return nil, apperrors.New(apperrors.CodeBadRequest, "系统预置供应商不支持修改类型和连接配置")
		}
	}

	if req.Name != nil {
		provider.Name = *req.Name
	}
	if req.Description != nil {
		provider.Description = *req.Description
	}
	if req.ProviderType != nil {
		// 检查 provider_type 是否已注册
		if s.registry.Get(*req.ProviderType) == nil {
			return nil, apperrors.New(apperrors.CodeBadRequest, "供应商类型未注册")
		}
		provider.ProviderType = *req.ProviderType
	}
	if req.ConfigSchema != nil {
		provider.ConfigSchema = datatypes.JSON(*req.ConfigSchema)
	}
	if req.InputSchema != nil {
		provider.InputSchema = datatypes.JSON(*req.InputSchema)
	}
	if req.ProviderConfig != nil {
		pc := *req.ProviderConfig
		// MCP 类型更新 provider_config 时，统一做规范化 + 校验
		effectiveType := req.ProviderType
		if effectiveType == nil {
			t := provider.ProviderType
			effectiveType = &t
		}
		if *effectiveType == "mcp" && len(pc) > 0 && string(pc) != "null" {
			wrapped, err := normalizeMCPProviderConfig(pc, s.registry)
			if err != nil {
				return nil, apperrors.New(apperrors.CodeBadRequest, "MCP 配置错误: "+err.Error())
			}
			pc = wrapped
		}
		provider.ProviderConfig = datatypes.JSON(pc)
	}
	if req.AdminConfig != nil {
		provider.AdminConfig = datatypes.JSON(*req.AdminConfig)
	}
	if req.RateLimit != nil {
		provider.RateLimit = datatypes.JSON(*req.RateLimit)
	}
	if req.IsEnabled != nil {
		provider.IsEnabled = *req.IsEnabled
	}

	if err := s.repo.Update(ctx, provider); err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return s.toProviderInfo(provider), nil
}

func (s *toolProviderService) Delete(ctx context.Context, id string) error {
	provider, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return apperrors.NewDefault(apperrors.CodeToolProviderNotFound)
	}

	// 系统预置保护：不允许删除系统预置供应商
	if provider.IsSystem {
		return apperrors.New(apperrors.CodeBadRequest, "系统预置供应商不支持删除")
	}

	return s.repo.Delete(ctx, id)
}

func (s *toolProviderService) GetByID(ctx context.Context, id string) (*response.ToolProviderInfo, error) {
	provider, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeToolProviderNotFound)
	}
	return s.toProviderInfo(provider), nil
}

func (s *toolProviderService) ListByToolTypeID(ctx context.Context, toolTypeID string) (*response.ListToolProvidersResponse, error) {
	providers, err := s.repo.ListByToolTypeID(ctx, toolTypeID)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	infos := make([]response.ToolProviderInfo, len(providers))
	for i, p := range providers {
		infos[i] = *s.toProviderInfo(&p)
	}

	return &response.ListToolProvidersResponse{Providers: infos}, nil
}

func (s *toolProviderService) ListEnabledByToolTypeID(ctx context.Context, toolTypeID string) (*response.ListToolProvidersResponse, error) {
	providers, err := s.repo.ListEnabledByToolTypeID(ctx, toolTypeID)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	infos := make([]response.ToolProviderInfo, len(providers))
	for i, p := range providers {
		infos[i] = *s.toProviderInfo(&p)
	}

	return &response.ListToolProvidersResponse{Providers: infos}, nil
}

// ListProviderTypes 返回所有已注册的供应商类型
func (s *toolProviderService) ListProviderTypes() []string {
	if s.registry == nil {
		return nil
	}
	return s.registry.Keys()
}

// Test 测试工具连接
// 若 ProviderConfig 为空且 ProviderID 不为空，则从数据库查询 provider_config（用户测试场景）
// 否则使用前端直接传入的 ProviderConfig（管理员测试场景）
// 若 ToolInput 为空，HTTP 场景会自动从 URL 和 BodyTemplate 提取占位符并生成默认测试值
// MCP 场景：工具执行时自动调用 tools/list 并返回工具清单；裸 MCP 配置会自动包装为 {mcp: ...}
func (s *toolProviderService) Test(ctx context.Context, req request.TestToolRequest) (*response.TestResult, error) {
	start := time.Now()

	provider := s.registry.Get(req.ProviderType)
	if provider == nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, "供应商类型未注册")
	}

	var providerConfig *tool.ProviderConfig
	if req.ProviderConfig != nil {
		if req.ProviderType == "mcp" {
			// MCP 管理员测试场景：可能传入裸 MCP 配置，自动规范化
			raw, err := json.Marshal(req.ProviderConfig)
			if err != nil {
				return nil, apperrors.New(apperrors.CodeBadRequest, "provider_config 格式错误")
			}
			normalized, err := normalizeMCPProviderConfig(raw, s.registry)
			if err != nil {
				return &response.TestResult{
					Success:      false,
					Message:      "配置验证失败",
					Error:        err.Error(),
					ResponseTime: time.Since(start).Milliseconds(),
				}, nil
			}
			providerConfig = &tool.ProviderConfig{}
			if err := json.Unmarshal(normalized, providerConfig); err != nil {
				return nil, apperrors.New(apperrors.CodeBadRequest, "provider_config 格式错误")
			}
		} else {
			data, err := json.Marshal(req.ProviderConfig)
			if err != nil {
				return nil, apperrors.New(apperrors.CodeBadRequest, "provider_config 格式错误")
			}
			if err := json.Unmarshal(data, &providerConfig); err != nil {
				return nil, apperrors.New(apperrors.CodeBadRequest, "provider_config 格式错误")
			}
		}
	} else if req.ProviderID != "" {
		// 用户测试场景：从数据库查询供应商配置
		p, err := s.repo.GetByID(ctx, req.ProviderID)
		if err != nil {
			return nil, apperrors.NewDefault(apperrors.CodeToolProviderNotFound)
		}
		if len(p.ProviderConfig) == 0 || string(p.ProviderConfig) == "null" {
			return &response.TestResult{
				Success:      false,
				Message:      "配置验证失败",
				Error:        "供应商未配置 provider_config",
				ResponseTime: time.Since(start).Milliseconds(),
			}, nil
		}
		if err := json.Unmarshal(p.ProviderConfig, &providerConfig); err != nil {
			return nil, apperrors.New(apperrors.CodeBadRequest, "provider_config 格式错误")
		}
	}

	// 如果 tool_input 为空，自动生成默认测试参数
	toolInput := req.ToolInput
	if toolInput == nil || len(toolInput) == 0 {
		toolInput = generateDefaultToolInput(providerConfig, req.UserConfig, req.AdminConfig)
	}

	executeConfig := &tool.ExecuteConfig{
		ToolInput:      toolInput,
		UserConfig:     req.UserConfig,
		ProviderConfig: providerConfig,
		AdminConfig:    req.AdminConfig,
	}

	if err := provider.Validate(executeConfig); err != nil {
		elapsed := time.Since(start)
		return &response.TestResult{
			Success:      false,
			Message:      "配置验证失败",
			Error:        err.Error(),
			ResponseTime: elapsed.Milliseconds(),
		}, nil
	}

	result, err := provider.Execute(ctx, executeConfig)

	elapsed := time.Since(start)

	if err != nil {
		return &response.TestResult{
			Success:      false,
			Message:      "工具调用失败",
			Error:        err.Error(),
			ResponseTime: elapsed.Milliseconds(),
			Details:      result,
		}, nil
	}

	testResult := &response.TestResult{
		Success:      true,
		Message:      "工具调用成功",
		ResponseTime: elapsed.Milliseconds(),
		Details:      result,
	}

	// MCP 场景：额外填充工具清单
	if req.ProviderType == "mcp" {
		if multi, ok := provider.(tool.MultiToolProvider); ok {
			mcpTools, toolsErr := multi.GetTools(ctx, providerConfig)
			if toolsErr == nil && len(mcpTools) > 0 {
				testResult.MCPToolCount = len(mcpTools)
				testResult.MCPTools = make([]response.MCPToolInfo, 0, len(mcpTools))
				for _, mt := range mcpTools {
					tInfo, ierr := mt.Info(ctx)
					if ierr != nil {
						continue
					}
					testResult.MCPTools = append(testResult.MCPTools, response.MCPToolInfo{
						Name:        tInfo.Name,
						Description: tInfo.Desc,
					})
				}
				testResult.Message = fmt.Sprintf("MCP 连接成功，发现 %d 个工具", testResult.MCPToolCount)
			} else if toolsErr != nil {
				testResult.Message = "MCP 连接成功，但拉取工具清单失败"
				testResult.Details += "; GetToolsErr: " + toolsErr.Error()
			}
		}
	}

	return testResult, nil
}

func (s *toolProviderService) toProviderInfo(p *entity.ToolProvider) *response.ToolProviderInfo {
	return &response.ToolProviderInfo{
		ID:             p.ID,
		ToolTypeID:     p.ToolTypeID,
		ProviderKey:    p.ProviderKey,
		Name:           p.Name,
		Description:    p.Description,
		ProviderType:   p.ProviderType,
		ConfigSchema:   json.RawMessage(p.ConfigSchema),
		InputSchema:    json.RawMessage(p.InputSchema),
		ProviderConfig: json.RawMessage(p.ProviderConfig),
		AdminConfig:    json.RawMessage(p.AdminConfig),
		RateLimit:      json.RawMessage(p.RateLimit),
		IsEnabled:      p.IsEnabled,
		IsSystem:       p.IsSystem,
		DisplayOrder:   p.DisplayOrder,
	}
}

// generateDefaultToolInput 从 ProviderConfig 的 URL 和 BodyTemplate 中提取占位符并生成默认测试值
func generateDefaultToolInput(pc *tool.ProviderConfig, userConfig, adminConfig map[string]interface{}) map[string]interface{} {
	if pc == nil {
		return nil
	}

	// 收集所有已提供的配置键
	provided := make(map[string]bool)
	for k := range userConfig {
		provided[k] = true
	}
	for k := range adminConfig {
		provided[k] = true
	}

	// 从 URL 提取占位符
	placeholders := extractTestPlaceholders(pc.URL)

	// 从 BodyTemplate 提取占位符
	if pc.BodyTemplate != nil {
		for _, ph := range extractTestPlaceholdersFromMap(pc.BodyTemplate) {
			if _, ok := provided[ph]; !ok {
				placeholders[ph] = true
			}
		}
	}

	// 为剩余占位符生成默认值
	result := make(map[string]interface{})
	for ph := range placeholders {
		if provided[ph] {
			continue
		}
		result[ph] = getDefaultPlaceholderValue(ph)
	}

	return result
}

// extractTestPlaceholders 从字符串中提取 {{xxx}} 占位符
func extractTestPlaceholders(s string) map[string]bool {
	result := make(map[string]bool)
	for {
		start := strings.Index(s, "{{")
		if start == -1 {
			break
		}
		end := strings.Index(s[start+2:], "}}")
		if end == -1 {
			break
		}
		ph := s[start+2 : start+2+end]
		if ph != "" {
			result[ph] = true
		}
		s = s[start+2+end+2:]
	}
	return result
}

// extractTestPlaceholdersFromMap 从 map 中递归提取占位符
func extractTestPlaceholdersFromMap(m map[string]interface{}) []string {
	var result []string
	seen := make(map[string]bool)
	var walk func(interface{})
	walk = func(x interface{}) {
		switch val := x.(type) {
		case string:
			for ph := range extractTestPlaceholders(val) {
				if !seen[ph] {
					result = append(result, ph)
					seen[ph] = true
				}
			}
		case map[string]interface{}:
			for _, child := range val {
				walk(child)
			}
		case []interface{}:
			for _, child := range val {
				walk(child)
			}
		}
	}
	walk(m)
	return result
}

// getDefaultPlaceholderValue 根据占位符名称返回默认测试值
func getDefaultPlaceholderValue(ph string) interface{} {
	switch ph {
	case "query", "q", "keyword", "search", "term":
		return "test"
	case "city", "location", "address":
		return "Beijing"
	case "lat", "latitude":
		return 39.9042
	case "lng", "longitude":
		return 116.4074
	case "days", "count":
		return 3
	case "format":
		return "json"
	case "stock_code":
		return "sh600001"
	case "currency":
		return "usd"
	default:
		return "test"
	}
}

// normalizeMCPProviderConfig 规范化 MCP 供应商的 provider_config。
// 管理员前端可能传入两种格式：
//  A. 裸 MCP 配置：{"transport":"stdio","command":"uvx","args":[...]}  → 自动包装为标准结构
//  B. 已包装格式：{"mcp":{"transport":"stdio",...}}  → 直接校验
// 返回已包装的标准 ProviderConfig JSON。
func normalizeMCPProviderConfig(raw json.RawMessage, registry tool.ProviderRegistry) (json.RawMessage, error) {
	// 先尝试解析为标准 ProviderConfig（包含 mcp 字段）
	var stdCfg tool.ProviderConfig
	if err := json.Unmarshal(raw, &stdCfg); err != nil {
		return nil, fmt.Errorf("provider_config JSON 格式错误: %w", err)
	}

	if stdCfg.MCP != nil {
		// 情况 B：已经是标准结构，直接 Validate
		exeCfg := &tool.ExecuteConfig{
			ProviderConfig: &stdCfg,
		}
		provider := registry.Get("mcp")
		if provider != nil {
			if err := provider.Validate(exeCfg); err != nil {
				return nil, fmt.Errorf("配置校验失败: %w", err)
			}
		}
		return raw, nil
	}

	// 情况 A：尝试把 raw 解析为 MCPProviderConfig（裸配置）
	var mcpCfg tool.MCPProviderConfig
	if err := json.Unmarshal(raw, &mcpCfg); err != nil {
		return nil, fmt.Errorf("无法解析为 MCP 配置: %w", err)
	}
	// 至少 transport 存在，才认定是裸 MCP 配置
	if mcpCfg.Transport == "" {
		return nil, fmt.Errorf("MCP 配置缺少 transport 字段（stdio/sse/http）")
	}
	wrapped := tool.ProviderConfig{MCP: &mcpCfg}
	exeCfg := &tool.ExecuteConfig{ProviderConfig: &wrapped}
	provider := registry.Get("mcp")
	if provider != nil {
		if err := provider.Validate(exeCfg); err != nil {
			return nil, fmt.Errorf("配置校验失败: %w", err)
		}
	}
	out, err := json.Marshal(wrapped)
	if err != nil {
		return nil, fmt.Errorf("序列化 provider_config 失败: %w", err)
	}
	return out, nil
}
