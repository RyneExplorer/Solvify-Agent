package service

import (
	"context"

	"solvify-agent/internal/llm"
	requestdto "solvify-agent/internal/model/dto/request"
	responsedto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	apperrors "solvify-agent/pkg/errors"
)

// modelService 封装系统模型管理业务用例实现
type modelService struct {
	modelRepo repository.ModelRepo
}

// NewModelService 创建系统模型服务
func NewModelService(modelRepo repository.ModelRepo) ModelServiceInterface {
	return &modelService{modelRepo: modelRepo}
}

// Create 创建系统模型
func (s *modelService) Create(ctx context.Context, req requestdto.CreateModelRequest) (responsedto.ModelInfo, error) {
	if err := validateModelSettings(req.MaxContextLength, req.Config); err != nil {
		return responsedto.ModelInfo{}, err
	}
	if !llm.SupportsProvider(req.Provider) {
		return responsedto.ModelInfo{}, apperrors.New(apperrors.CodeInvalidParam, "仅支持已实现的 OpenAI 兼容协议")
	}
	// 检查 model_id 是否已存在
	exists, err := s.modelRepo.ExistsByModelID(ctx, req.ModelID, "")
	if err != nil {
		return responsedto.ModelInfo{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	if exists {
		return responsedto.ModelInfo{}, apperrors.NewDefault(apperrors.CodeModelExists)
	}

	model := &entity.Model{
		Name:             req.ModelID,
		Provider:         req.Provider,
		ModelID:          req.ModelID,
		BaseURL:          req.BaseURL,
		APIKey:           req.APIKey,
		IsEnabled:        true,
		MaxContextLength: req.MaxContextLength,
		Config:           toJSONB(req.Config),
	}

	if err := verifyModelForSave(ctx, requestdto.TestModelRequest{Provider: model.Provider, ModelID: model.ModelID, BaseURL: model.BaseURL, APIKey: model.APIKey, MaxContextLength: model.MaxContextLength, Config: decodeSettings(model.Config)}); err != nil {
		return responsedto.ModelInfo{}, err
	}
	if err := s.modelRepo.Create(ctx, model); err != nil {
		return responsedto.ModelInfo{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return toModelInfo(*model), nil
}

// Update 更新系统模型
func (s *modelService) Update(ctx context.Context, id string, req requestdto.UpdateModelRequest) error {
	model, err := s.modelRepo.GetByID(ctx, id)
	if err != nil {
		return apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	candidateIdentityModel, candidateIdentityURL, candidateIdentityProtocol := model.ModelID, model.BaseURL, model.Provider
	candidate := *model
	model = &candidate
	// 上游不可用时仍允许停用；停用不改变模型能力。
	if req.IsEnabled != nil && !*req.IsEnabled && req.Provider == nil && req.ModelID == nil && req.BaseURL == nil && req.APIKey == nil && req.Config == nil && req.MaxContextLength == nil {
		model.IsEnabled = false
		if err := s.modelRepo.Update(ctx, model); err != nil {
			return apperrors.WrapDefault(apperrors.CodeInternalError, err)
		}
		return nil
	}
	// 如果修改了 model_id，检查是否重复
	if req.ModelID != nil && *req.ModelID != model.ModelID {
		exists, err := s.modelRepo.ExistsByModelID(ctx, *req.ModelID, id)
		if err != nil {
			return apperrors.WrapDefault(apperrors.CodeInternalError, err)
		}
		if exists {
			return apperrors.NewDefault(apperrors.CodeModelExists)
		}
	}

	if req.Provider != nil {
		model.Provider = *req.Provider
	}
	if req.ModelID != nil {
		model.ModelID = *req.ModelID
		model.Name = *req.ModelID
	}
	if req.BaseURL != nil {
		model.BaseURL = *req.BaseURL
	}
	if req.APIKey != nil && *req.APIKey != "" {
		model.APIKey = *req.APIKey
	}
	if req.IsEnabled != nil {
		model.IsEnabled = *req.IsEnabled
	}
	if req.MaxContextLength != nil {
		model.MaxContextLength = *req.MaxContextLength
	}
	if req.Config != nil {
		if err := validateModelDraftSettings(model.MaxContextLength, req.Config); err != nil {
			return err
		}
		model.Config = toJSONB(req.Config)
	}
	if model.ModelID != candidateIdentityModel || model.BaseURL != candidateIdentityURL || model.Provider != candidateIdentityProtocol {
		if req.ReconfirmCapabilities {
			if req.Config == nil || req.MaxContextLength == nil {
				return apperrors.New(apperrors.CodeInvalidParam, "重新确认需要完整能力配置")
			}
			if err := validateModelSettings(model.MaxContextLength, req.Config); err != nil {
				return err
			}
		} else {
			model.MaxContextLength = 0
			model.Config = toJSONB(map[string]interface{}{})
		}
	}
	if err := validateModelDraft(model.MaxContextLength, model.Config); err != nil {
		return apperrors.New(apperrors.CodeInvalidParam, err.Error())
	}
	if !llm.SupportsProvider(model.Provider) {
		return apperrors.New(apperrors.CodeInvalidParam, "仅支持已实现的 OpenAI 兼容协议")
	}

	if err := verifyModelForSave(ctx, requestdto.TestModelRequest{Provider: model.Provider, ModelID: model.ModelID, BaseURL: model.BaseURL, APIKey: model.APIKey, MaxContextLength: model.MaxContextLength, Config: decodeSettings(model.Config)}); err != nil {
		return err
	}

	if err := s.modelRepo.Update(ctx, model); err != nil {
		return apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return nil
}

// Delete 删除系统模型
func (s *modelService) Delete(ctx context.Context, id string) error {
	if err := s.modelRepo.Delete(ctx, id); err != nil {
		return apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	return nil
}

// List 获取所有可用系统模型
func (s *modelService) List(ctx context.Context) (responsedto.ListModelsResponse, error) {
	models, err := s.modelRepo.List(ctx)
	if err != nil {
		return responsedto.ListModelsResponse{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	result := make([]responsedto.ModelInfo, len(models))
	for i, m := range models {
		result[i] = toModelInfo(m)
	}

	return responsedto.ListModelsResponse{Models: result}, nil
}

// GetByID 根据 ID 获取系统模型
func (s *modelService) GetByID(ctx context.Context, id string) (responsedto.ModelInfo, error) {
	model, err := s.modelRepo.GetByID(ctx, id)
	if err != nil {
		return responsedto.ModelInfo{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	return toModelInfo(*model), nil
}

// GetForEdit 仅供管理员编辑入口调用，返回配置凭据。
func (s *modelService) GetForEdit(ctx context.Context, id string) (responsedto.ModelInfo, error) {
	m, err := s.modelRepo.GetByID(ctx, id)
	if err != nil {
		return responsedto.ModelInfo{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	info := toModelInfo(*m)
	info.APIKey = m.APIKey
	return info, nil
}

// Test 测试模型连接
func (s *modelService) Test(ctx context.Context, req requestdto.TestModelRequest) (responsedto.TestResult, error) {
	return testModelSettings(ctx, req)
}

// toModelInfo 把系统模型实体转为响应 DTO。
//
// 注意 APIKey 走 maskAPIKey 脱敏：系统模型是平台级共享凭据（所有用户共用一个上游密钥），
// 原样返回意味着任意登录用户 GET /api/v1/models 就能拿到 OpenAI / DeepSeek 等全部上游
// 密钥并盗用配额，密钥还会顺带进入浏览器内存、前端缓存与反向代理响应日志。
//
// 内部解析凭据不经过本函数（chat_service 直接用 modelRepo 取 entity），故脱敏无副作用；
// 管理台通过独立且受管理员权限保护的 GetForEdit 回读，通用列表/详情继续脱敏。
func toModelInfo(m entity.Model) responsedto.ModelInfo {
	return responsedto.ModelInfo{
		CapabilityStatus: modelCapabilityStatus(m.Config, m.MaxContextLength),
		ID:               m.ID,
		Name:             m.Name,
		Provider:         m.Provider,
		ModelID:          m.ModelID,
		BaseURL:          m.BaseURL,
		APIKey:           maskAPIKey(m.APIKey),
		IsEnabled:        m.IsEnabled,
		MaxContextLength: m.MaxContextLength,
		Config:           decodeModelConfig(m.Config),
	}
}
