package service

import (
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/datatypes"

	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/tool"
	apperrors "solvify-agent/pkg/errors"
)

type userToolConfigService struct {
	repo         repository.UserToolConfigRepository
	typeRepo     repository.ToolTypeRepository
	providerRepo repository.ToolProviderRepository
	registry     tool.ProviderRegistry
}

// NewUserToolConfigService 创建用户工具配置服务实例
func NewUserToolConfigService(
	repo repository.UserToolConfigRepository,
	typeRepo repository.ToolTypeRepository,
	providerRepo repository.ToolProviderRepository,
	registry tool.ProviderRegistry,
) UserToolConfigService {
	return &userToolConfigService{
		repo:         repo,
		typeRepo:     typeRepo,
		providerRepo: providerRepo,
		registry:     registry,
	}
}

func (s *userToolConfigService) Create(ctx context.Context, userID string, req request.CreateUserToolConfigRequest) (*response.UserToolConfigInfo, error) {
	// 检查工具类型是否存在
	toolType, err := s.typeRepo.GetByID(ctx, req.ToolTypeID)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeToolTypeNotFound)
	}

	// 检查供应商是否存在
	provider, err := s.providerRepo.GetByID(ctx, req.ProviderID)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeToolProviderNotFound)
	}

	// 检查是否已配置该供应商
	existing, _ := s.repo.GetByUserAndProvider(ctx, userID, req.ProviderID)
	if existing != nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, "该供应商已配置，请编辑现有配置")
	}

	// 解析用户配置
	var configMap map[string]interface{}
	if err := json.Unmarshal(req.Config, &configMap); err != nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, "配置格式错误")
	}

	// 验证 config_schema 中的 required 字段
	if err := s.validateConfigSchema(provider.ConfigSchema, configMap); err != nil {
		return nil, err
	}

	// 解析管理员业务参数
	var adminConfigMap map[string]interface{}
	_ = json.Unmarshal(provider.AdminConfig, &adminConfigMap)

	// 解析供应商配置
	var providerCfg tool.ProviderConfig
	if len(provider.ProviderConfig) > 0 {
		if err := json.Unmarshal(provider.ProviderConfig, &providerCfg); err != nil {
			return nil, apperrors.New(apperrors.CodeBadRequest, "供应商 HTTP 配置格式错误，请联系管理员检查供应商配置")
		}
	}
	if provider.ProviderType == "http" && (providerCfg.URL == "" || providerCfg.Method == "") {
		return nil, apperrors.New(apperrors.CodeBadRequest, "供应商 HTTP 配置不完整，请先在后台管理中配置请求 URL 和方法")
	}

	// 调用 Provider 的 Validate 方法进行业务验证
	if err := s.validateProviderConfig(provider.ProviderType, &tool.ExecuteConfig{
		ToolInput:      make(map[string]interface{}),
		UserConfig:     configMap,
		ProviderConfig: &providerCfg,
		AdminConfig:    adminConfigMap,
	}); err != nil {
		return nil, err
	}

	config := &entity.UserToolConfig{
		UserID:      userID,
		ToolTypeID:  req.ToolTypeID,
		ProviderID:  req.ProviderID,
		DisplayName: req.DisplayName,
		Config:      datatypes.JSON(req.Config),
		IsEnabled:   true,
	}

	if config.DisplayName == "" {
		config.DisplayName = provider.Name
	}

	if err := s.repo.Create(ctx, config); err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	if err := s.repo.DisableOthersByToolType(ctx, userID, req.ToolTypeID, config.ID); err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return s.toConfigInfo(config, toolType, provider), nil
}

func (s *userToolConfigService) Update(ctx context.Context, userID, id string, req request.UpdateUserToolConfigRequest) (*response.UserToolConfigInfo, error) {
	config, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeBadRequest)
	}

	// 验证归属
	if config.UserID != userID {
		return nil, apperrors.NewDefault(apperrors.CodeBadRequest)
	}

	// 获取当前供应商（用于后续验证）
	currentProvider, err := s.providerRepo.GetByID(ctx, config.ProviderID)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeToolProviderNotFound)
	}

	if req.ProviderID != nil {
		// 验证新供应商是否存在
		newProvider, err := s.providerRepo.GetByID(ctx, *req.ProviderID)
		if err != nil {
			return nil, apperrors.New(apperrors.CodeToolProviderNotFound, "新供应商不存在")
		}

		// 验证新供应商是否属于同一个工具类型
		if newProvider.ToolTypeID != config.ToolTypeID {
			return nil, apperrors.New(apperrors.CodeBadRequest, "新供应商不属于当前工具类型，无法切换")
		}

		if existing, _ := s.repo.GetByUserAndProvider(ctx, userID, *req.ProviderID); existing != nil && existing.ID != config.ID {
			return nil, apperrors.New(apperrors.CodeBadRequest, "该供应商已配置，请编辑现有配置")
		}

		config.ProviderID = *req.ProviderID
		currentProvider = newProvider
	}
	if req.DisplayName != nil {
		config.DisplayName = *req.DisplayName
	}
	if req.Config != nil {
		config.Config = datatypes.JSON(*req.Config)

		// 验证新配置
		var configMap map[string]interface{}
		if err := json.Unmarshal(*req.Config, &configMap); err != nil {
			return nil, apperrors.New(apperrors.CodeBadRequest, "配置格式错误")
		}

		// 验证 config_schema
		if err := s.validateConfigSchema(currentProvider.ConfigSchema, configMap); err != nil {
			return nil, err
		}

		// 解析管理员业务参数与供应商配置
		var adminConfigMap map[string]interface{}
		_ = json.Unmarshal(currentProvider.AdminConfig, &adminConfigMap)
		var providerCfg tool.ProviderConfig
		if len(currentProvider.ProviderConfig) > 0 {
			if err := json.Unmarshal(currentProvider.ProviderConfig, &providerCfg); err != nil {
				return nil, apperrors.New(apperrors.CodeBadRequest, "供应商 HTTP 配置格式错误，请联系管理员检查供应商配置")
			}
		}
		if currentProvider.ProviderType == "http" && (providerCfg.URL == "" || providerCfg.Method == "") {
			return nil, apperrors.New(apperrors.CodeBadRequest, "供应商 HTTP 配置不完整，请先在后台管理中配置请求 URL 和方法")
		}

		// 验证 Provider 业务逻辑
		if err := s.validateProviderConfig(currentProvider.ProviderType, &tool.ExecuteConfig{
			ToolInput:      make(map[string]interface{}),
			UserConfig:     configMap,
			ProviderConfig: &providerCfg,
			AdminConfig:    adminConfigMap,
		}); err != nil {
			return nil, err
		}
	}
	if req.IsEnabled != nil {
		config.IsEnabled = *req.IsEnabled
	}

	if err := s.repo.Update(ctx, config); err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	if config.IsEnabled {
		if err := s.repo.DisableOthersByToolType(ctx, userID, config.ToolTypeID, config.ID); err != nil {
			return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
		}
	}

	// 重新加载关联数据（使用 Preload）
	updatedConfig, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return s.toConfigInfo(updatedConfig, &updatedConfig.ToolType, &updatedConfig.ToolProvider), nil
}

func (s *userToolConfigService) Delete(ctx context.Context, userID, id string) error {
	config, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return apperrors.NewDefault(apperrors.CodeBadRequest)
	}

	if config.UserID != userID {
		return apperrors.NewDefault(apperrors.CodeBadRequest)
	}

	return s.repo.Delete(ctx, id)
}

func (s *userToolConfigService) Get(ctx context.Context, userID, id string) (*response.UserToolConfigInfo, error) {
	config, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeBadRequest)
	}

	if config.UserID != userID {
		return nil, apperrors.NewDefault(apperrors.CodeBadRequest)
	}

	// 使用 Preload 的关联数据，避免额外查询
	return s.toConfigInfo(config, &config.ToolType, &config.ToolProvider), nil
}

func (s *userToolConfigService) List(ctx context.Context, userID string) (*response.ListUserToolConfigsResponse, error) {
	configs, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	infos := make([]response.UserToolConfigInfo, len(configs))
	for i, c := range configs {
		// 使用 Preload 的关联数据，避免 N+1 查询
		infos[i] = *s.toConfigInfo(&c, &c.ToolType, &c.ToolProvider)
	}

	return &response.ListUserToolConfigsResponse{Configs: infos}, nil
}

func (s *userToolConfigService) ListEnabled(ctx context.Context, userID string) (*response.ListUserToolConfigsResponse, error) {
	configs, err := s.repo.ListEnabledByUserID(ctx, userID)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	infos := make([]response.UserToolConfigInfo, len(configs))
	for i, c := range configs {
		// 使用 Preload 的关联数据，避免 N+1 查询
		infos[i] = *s.toConfigInfo(&c, &c.ToolType, &c.ToolProvider)
	}

	return &response.ListUserToolConfigsResponse{Configs: infos}, nil
}

func (s *userToolConfigService) toConfigInfo(
	config *entity.UserToolConfig,
	toolType *entity.ToolType,
	provider *entity.ToolProvider,
) *response.UserToolConfigInfo {
	info := &response.UserToolConfigInfo{
		ID:          config.ID,
		ToolTypeID:  config.ToolTypeID,
		ProviderID:  config.ProviderID,
		DisplayName: config.DisplayName,
		Config:      json.RawMessage(config.Config),
		IsEnabled:   config.IsEnabled,
		CreatedAt:   config.CreatedAt,
		UpdatedAt:   config.UpdatedAt,
	}

	if toolType != nil {
		info.ToolTypeName = toolType.Name
		info.ToolTypeKey = toolType.ToolKey
	}
	if provider != nil {
		info.ProviderName = provider.Name
	}

	return info
}

// validateConfigSchema 验证配置是否符合 config_schema 中的 required 字段
func (s *userToolConfigService) validateConfigSchema(schemaJSON datatypes.JSON, config map[string]interface{}) error {
	if len(schemaJSON) == 0 {
		return nil
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return nil // schema 格式错误，跳过验证
	}

	// 检查 required 字段
	required, ok := schema["required"].([]interface{})
	if !ok {
		return nil
	}

	for _, field := range required {
		fieldName, ok := field.(string)
		if !ok {
			continue
		}

		value, exists := config[fieldName]
		if !exists || value == nil || value == "" {
			return apperrors.New(apperrors.CodeBadRequest, fmt.Sprintf("缺少必填字段: %s", fieldName))
		}
	}

	return nil
}

// validateProviderConfig 调用 Provider 的 Validate 方法进行业务验证
func (s *userToolConfigService) validateProviderConfig(providerType string, config *tool.ExecuteConfig) error {
	if s.registry == nil {
		return nil
	}

	provider := s.registry.Get(providerType)
	if provider == nil {
		return nil // Provider 未注册，跳过验证
	}

	if err := provider.Validate(config); err != nil {
		return apperrors.New(apperrors.CodeBadRequest, fmt.Sprintf("配置验证失败: %v", err))
	}

	return nil
}
