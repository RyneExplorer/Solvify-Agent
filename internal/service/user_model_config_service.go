package service

import (
	"context"
	"encoding/json"
	"errors"
	"solvify-agent/internal/llm"
	"strings"

	"gorm.io/datatypes"

	requestdto "solvify-agent/internal/model/dto/request"
	responsedto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	apperrors "solvify-agent/pkg/errors"
)

// isNotFound 判断 Repo 返回的是否为"资源不存在"业务错误（通过错误码匹配，避免 Service 依赖 GORM）
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var biz *apperrors.BizError
	if errors.As(err, &biz) {
		switch biz.Code {
		case apperrors.CodeModelConfigNotFound,
			apperrors.CodeUserNotFound,
			apperrors.CodeKnowledgeBaseNotFound,
			apperrors.CodeDocumentNotFound,
			apperrors.CodeSessionNotFound,
			apperrors.CodeToolTypeNotFound,
			apperrors.CodeToolProviderNotFound,
			apperrors.CodeNotFound:
			return true
		}
	}
	return false
}

// userModelConfigService 封装用户模型配置业务用例实现
type userModelConfigService struct {
	repo     repository.UserModelConfigRepo
	profiles modelDeploymentReader
}

// NewUserModelConfigService 创建用户模型配置服务
func NewUserModelConfigService(repo repository.UserModelConfigRepo, profiles modelDeploymentReader) UserModelConfigServiceInterface {
	return &userModelConfigService{repo: repo, profiles: profiles}
}

// Create 创建用户模型配置
func (s *userModelConfigService) Create(ctx context.Context, userID string, req requestdto.CreateUserModelConfigRequest) (responsedto.UserModelConfigInfo, error) {
	settings, err := userDeploymentSettings(ctx, requestdto.TestModelRequest{Provider: req.APIFormat, ModelID: req.ModelID, BaseURL: req.BaseURL, APIKey: req.APIKey, MaxContextLength: req.MaxContextLength, Config: req.Config}, nil, s.profiles)
	if err != nil {
		return responsedto.UserModelConfigInfo{}, err
	}
	req.Config = settings
	if err := validateModelSettings(req.MaxContextLength, req.Config); err != nil {
		return responsedto.UserModelConfigInfo{}, err
	}
	if req.APIFormat != "openai" {
		return responsedto.UserModelConfigInfo{}, apperrors.New(apperrors.CodeInvalidParam, "仅支持OpenAI兼容协议")
	}
	// 检查 model_id 是否已存在
	exists, err := s.repo.ExistsByModelID(ctx, userID, req.ModelID, "")
	if err != nil {
		return responsedto.UserModelConfigInfo{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	if exists {
		return responsedto.UserModelConfigInfo{}, apperrors.NewDefault(apperrors.CodeModelConfigExists)
	}

	config := &entity.UserModelConfig{
		UserID:           userID,
		DisplayName:      req.ModelID,
		APIFormat:        req.APIFormat,
		BaseURL:          req.BaseURL,
		ModelID:          req.ModelID,
		APIKey:           req.APIKey,
		Config:           toJSONB(req.Config),
		MaxContextLength: req.MaxContextLength,
	}

	if err := verifyModelForSave(ctx, requestdto.TestModelRequest{Provider: config.APIFormat, ModelID: config.ModelID, BaseURL: config.BaseURL, APIKey: config.APIKey, MaxContextLength: config.MaxContextLength, Config: decodeSettings(config.Config)}); err != nil {
		return responsedto.UserModelConfigInfo{}, err
	}
	if err := s.repo.Create(ctx, config); err != nil {
		return responsedto.UserModelConfigInfo{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return toModelConfigInfo(config), nil
}

// Update 更新用户模型配置
func (s *userModelConfigService) Update(ctx context.Context, userID string, configID string, req requestdto.UpdateUserModelConfigRequest) (responsedto.UserModelConfigInfo, error) {
	config, err := s.repo.GetByID(ctx, configID, userID)
	if err != nil {
		if isNotFound(err) {
			return responsedto.UserModelConfigInfo{}, apperrors.New(apperrors.CodeModelConfigNotFound, "模型配置不存在")
		}
		return responsedto.UserModelConfigInfo{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	// 如果修改了 model_id，检查是否重复
	candidateIdentityModel, candidateIdentityURL, candidateIdentityProtocol := config.ModelID, config.BaseURL, config.APIFormat
	candidate := *config
	config = &candidate
	if req.ModelID != nil && *req.ModelID != config.ModelID {
		exists, err := s.repo.ExistsByModelID(ctx, userID, *req.ModelID, configID)
		if err != nil {
			return responsedto.UserModelConfigInfo{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
		}
		if exists {
			return responsedto.UserModelConfigInfo{}, apperrors.NewDefault(apperrors.CodeModelConfigExists)
		}
	}

	if req.APIFormat != nil {
		config.APIFormat = *req.APIFormat
	}
	if req.ModelID != nil {
		config.ModelID = *req.ModelID
		config.DisplayName = *req.ModelID
	}
	// 只有当 APIKey 非空时才更新
	if req.APIKey != nil && *req.APIKey != "" {
		config.APIKey = *req.APIKey
	}
	if req.BaseURL != nil {
		config.BaseURL = *req.BaseURL
	}
	existingSettings := decodeSettings(config.Config)
	if config.ModelID != candidateIdentityModel || config.BaseURL != candidateIdentityURL || config.APIFormat != candidateIdentityProtocol {
		existingSettings = nil
	}
	settings, err := userDeploymentSettings(ctx, requestdto.TestModelRequest{Provider: config.APIFormat, ModelID: config.ModelID, BaseURL: config.BaseURL, APIKey: config.APIKey, Config: req.Config}, existingSettings, s.profiles)
	if err != nil {
		return responsedto.UserModelConfigInfo{}, err
	}
	req.Config = settings
	if err := validateModelDraftSettings(0, req.Config); err != nil {
		return responsedto.UserModelConfigInfo{}, err
	}
	config.Config = toJSONB(req.Config)
	if req.MaxContextLength != nil {
		config.MaxContextLength = *req.MaxContextLength
	}
	if config.ModelID != candidateIdentityModel || config.BaseURL != candidateIdentityURL || config.APIFormat != candidateIdentityProtocol {
		if req.ReconfirmCapabilities {
			if req.MaxContextLength == nil {
				return responsedto.UserModelConfigInfo{}, apperrors.New(apperrors.CodeInvalidParam, "重新确认需要填写窗口并取得当前部署资料")
			}
			if err := validateModelSettings(config.MaxContextLength, req.Config); err != nil {
				return responsedto.UserModelConfigInfo{}, err
			}
		} else {
			config.MaxContextLength = 0
			config.Config = toJSONB(map[string]interface{}{})
		}
	}
	if err := validateModelDraft(config.MaxContextLength, config.Config); err != nil {
		return responsedto.UserModelConfigInfo{}, apperrors.New(apperrors.CodeInvalidParam, err.Error())
	}
	if config.APIFormat != "openai" {
		return responsedto.UserModelConfigInfo{}, apperrors.New(apperrors.CodeInvalidParam, "仅支持OpenAI兼容协议")
	}

	if err := verifyModelForSave(ctx, requestdto.TestModelRequest{Provider: config.APIFormat, ModelID: config.ModelID, BaseURL: config.BaseURL, APIKey: config.APIKey, MaxContextLength: config.MaxContextLength, Config: decodeSettings(config.Config)}); err != nil {
		return responsedto.UserModelConfigInfo{}, err
	}
	if err := s.repo.Update(ctx, config); err != nil {
		return responsedto.UserModelConfigInfo{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return toModelConfigInfo(config), nil
}

// Delete 删除用户模型配置
func (s *userModelConfigService) Delete(ctx context.Context, userID string, configID string) error {
	_, err := s.repo.GetByID(ctx, configID, userID)
	if err != nil {
		if isNotFound(err) {
			return apperrors.New(apperrors.CodeModelConfigNotFound, "模型配置不存在")
		}
		return apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return s.repo.Delete(ctx, configID, userID)
}

// Get 获取单个用户模型配置
func (s *userModelConfigService) Get(ctx context.Context, userID string, configID string) (responsedto.UserModelConfigInfo, error) {
	config, err := s.repo.GetByID(ctx, configID, userID)
	if err != nil {
		if isNotFound(err) {
			return responsedto.UserModelConfigInfo{}, apperrors.New(apperrors.CodeModelConfigNotFound, "模型配置不存在")
		}
		return responsedto.UserModelConfigInfo{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	info := toModelConfigInfo(config)
	info.APIKey = config.APIKey
	return info, nil
}

// List 列出用户所有模型配置
func (s *userModelConfigService) List(ctx context.Context, userID string) (responsedto.ListUserModelConfigsResponse, error) {
	configs, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		return responsedto.ListUserModelConfigsResponse{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	result := make([]responsedto.UserModelConfigInfo, len(configs))
	for i, c := range configs {
		result[i] = toModelConfigInfo(&c)
	}

	return responsedto.ListUserModelConfigsResponse{Models: result}, nil
}

// ResolveModelConfig 解析用户模型配置
func (s *userModelConfigService) ResolveModelConfig(ctx context.Context, userID string, configID string) (*entity.UserModelConfig, error) {
	if configID == "" {
		return nil, nil
	}

	config, err := s.repo.GetByID(ctx, configID, userID)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	return config, nil
}

// Test 测试用户模型连接
func (s *userModelConfigService) Test(ctx context.Context, userID string, req requestdto.TestModelRequest) (responsedto.TestResult, error) {
	var existing map[string]interface{}
	var existingWindow int
	if req.ConfigID != "" {
		stored, err := s.repo.GetByID(ctx, req.ConfigID, userID)
		if err != nil {
			return responsedto.TestResult{}, err
		}
		if stored.ModelID == req.ModelID && stored.BaseURL == req.BaseURL && stored.APIFormat == req.Provider {
			existing, existingWindow = decodeSettings(stored.Config), stored.MaxContextLength
		}
	}
	if req.Operation == "capabilities" {
		if existing == nil && s.profiles != nil {
			models, err := s.profiles.List(ctx)
			if err != nil {
				return responsedto.TestResult{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
			}
			for _, m := range models {
				if m.IsEnabled && m.ModelID == req.ModelID && strings.TrimRight(m.BaseURL, "/") == strings.TrimRight(req.BaseURL, "/") {
					existing, existingWindow = decodeSettings(m.Config), m.MaxContextLength
					break
				}
			}
		}
		if existing != nil {
			raw, _ := json.Marshal(existing)
			p, err := llm.ParseModelDraft(raw, existingWindow)
			if err == nil && p.MaxOutputTokens > 0 {
				return responsedto.TestResult{Success: true, Message: "已读取服务端当前部署资料，保存前仍需验证工具调用", DetectedMaxContextLength: existingWindow, DetectedMaxOutputTokens: p.MaxOutputTokens, DetectedMaxInputTokens: p.MaxInputTokens, CapabilitySource: "manual", SuggestedConfig: existing}, nil
			}
		}
		return acquireModelCapabilities(ctx, req)
	}
	settings, err := userDeploymentSettings(ctx, req, existing, s.profiles)
	if err != nil {
		return responsedto.TestResult{}, err
	}
	req.Config = settings
	return testModelSettings(ctx, req)
}

// toModelConfigInfo 把实体转为响应 DTO
func toModelConfigInfo(config *entity.UserModelConfig) responsedto.UserModelConfigInfo {
	info := responsedto.UserModelConfigInfo{
		CapabilityStatus: modelCapabilityStatus(config.Config, config.MaxContextLength),
		ID:               config.ID,
		DisplayName:      config.DisplayName,
		APIFormat:        config.APIFormat,
		ModelID:          config.ModelID,
		BaseURL:          config.BaseURL,
		APIKey:           maskAPIKey(config.APIKey),
		MaxContextLength: config.MaxContextLength,
		CreatedAt:        config.CreatedAt,
		UpdatedAt:        config.UpdatedAt,
	}

	if len(config.Config) > 0 {
		var v interface{}
		if err := json.Unmarshal(config.Config, &v); err == nil {
			info.Config = v
		}
	}

	return info
}

// toJSONB 把 map 序列化为 datatypes.JSON
func toJSONB(v map[string]interface{}) datatypes.JSON {
	if v == nil {
		return nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return datatypes.JSON(data)
}
