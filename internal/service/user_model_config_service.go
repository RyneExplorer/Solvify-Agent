package service

import (
	"context"
	"encoding/json"
	"errors"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	requestdto "solvify-agent/internal/model/dto/request"
	responsedto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/utils"
)

// userModelConfigService 封装用户模型配置业务用例实现
type userModelConfigService struct {
	repo repository.UserModelConfigRepo
}

// NewUserModelConfigService 创建用户模型配置服务
func NewUserModelConfigService(repo repository.UserModelConfigRepo) UserModelConfigServiceInterface {
	return &userModelConfigService{repo: repo}
}

// Create 创建用户模型配置
func (s *userModelConfigService) Create(ctx context.Context, userID string, req requestdto.CreateUserModelConfigRequest) (responsedto.UserModelConfigInfo, error) {
	// 检查 model_id 是否已存在
	exists, err := s.repo.ExistsByModelID(ctx, userID, req.ModelID, "")
	if err != nil {
		return responsedto.UserModelConfigInfo{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	if exists {
		return responsedto.UserModelConfigInfo{}, apperrors.NewDefault(apperrors.CodeModelConfigExists)
	}

	config := &entity.UserModelConfig{
		UserID:      userID,
		DisplayName: req.ModelID,
		APIFormat:   req.APIFormat,
		BaseURL:     req.BaseURL,
		ModelID:     req.ModelID,
		APIKey:      req.APIKey,
		Config:      toJSONB(req.Config),
	}

	if err := s.repo.Create(ctx, config); err != nil {
		return responsedto.UserModelConfigInfo{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return toModelConfigInfo(config), nil
}

// Update 更新用户模型配置
func (s *userModelConfigService) Update(ctx context.Context, userID string, configID string, req requestdto.UpdateUserModelConfigRequest) error {
	config, err := s.repo.GetByID(ctx, configID, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperrors.New(apperrors.CodeModelConfigNotFound, "模型配置不存在")
		}
		return apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	// 如果修改了 model_id，检查是否重复
	if req.ModelID != nil && *req.ModelID != config.ModelID {
		exists, err := s.repo.ExistsByModelID(ctx, userID, *req.ModelID, configID)
		if err != nil {
			return apperrors.WrapDefault(apperrors.CodeInternalError, err)
		}
		if exists {
			return apperrors.NewDefault(apperrors.CodeModelConfigExists)
		}
	}

	if req.APIFormat != nil {
		config.APIFormat = *req.APIFormat
	}
	if req.ModelID != nil {
		config.ModelID = *req.ModelID
		config.DisplayName = *req.ModelID
	}
	// 只有当 APIKey 非空且不是脱敏格式时才更新
	if req.APIKey != nil && *req.APIKey != "" && !utils.IsMaskedAPIKey(*req.APIKey) {
		config.APIKey = *req.APIKey
	}
	if req.BaseURL != nil {
		config.BaseURL = *req.BaseURL
	}
	if req.Config != nil {
		config.Config = toJSONB(req.Config)
	}

	if err := s.repo.Update(ctx, config); err != nil {
		return apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return nil
}

// Delete 删除用户模型配置
func (s *userModelConfigService) Delete(ctx context.Context, userID string, configID string) error {
	_, err := s.repo.GetByID(ctx, configID, userID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
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
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return responsedto.UserModelConfigInfo{}, apperrors.New(apperrors.CodeModelConfigNotFound, "模型配置不存在")
		}
		return responsedto.UserModelConfigInfo{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return toModelConfigInfo(config), nil
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
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	return config, nil
}

func toModelConfigInfo(config *entity.UserModelConfig) responsedto.UserModelConfigInfo {
	info := responsedto.UserModelConfigInfo{
		ID:          config.ID,
		DisplayName: config.DisplayName,
		APIFormat:   config.APIFormat,
		ModelID:     config.ModelID,
		BaseURL:     config.BaseURL,
		APIKey:      utils.MaskAPIKey(config.APIKey),
		CreatedAt:   config.CreatedAt,
		UpdatedAt:   config.UpdatedAt,
	}

	if len(config.Config) > 0 {
		var v interface{}
		if err := json.Unmarshal(config.Config, &v); err == nil {
			info.Config = v
		}
	}

	return info
}

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
