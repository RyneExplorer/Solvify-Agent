package service

import (
	"context"
	"encoding/json"
	"time"

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
	// 检查 model_id 是否已存在
	exists, err := s.modelRepo.ExistsByModelID(ctx, req.ModelID, "")
	if err != nil {
		return responsedto.ModelInfo{}, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	if exists {
		return responsedto.ModelInfo{}, apperrors.NewDefault(apperrors.CodeModelExists)
	}

	model := &entity.Model{
		Name:      req.ModelID,
		Provider:  req.Provider,
		ModelID:   req.ModelID,
		BaseURL:   req.BaseURL,
		APIKey:    req.APIKey,
		IsEnabled: true,
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
	if req.APIKey != nil {
		model.APIKey = *req.APIKey
	}
	if req.IsEnabled != nil {
		model.IsEnabled = *req.IsEnabled
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

// Test 测试模型连接
func (s *modelService) Test(ctx context.Context, req requestdto.TestModelRequest) (responsedto.TestResult, error) {
	start := time.Now()

	configJSON, _ := json.Marshal(req.Config)

	client, err := llm.NewOpenAIClientDirect(ctx, llm.OpenAIClientConfig{
		APIKey:  req.APIKey,
		BaseURL: req.BaseURL,
		Model:   req.ModelID,
		Config:  configJSON,
	})
	if err != nil {
		elapsed := time.Since(start)
		return responsedto.TestResult{
			Success:      false,
			Message:      "模型连接失败",
			Error:        err.Error(),
			ResponseTime: elapsed.Milliseconds(),
		}, nil
	}

	// 真正发送请求验证连接
	testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if err := client.TestConnection(testCtx); err != nil {
		elapsed := time.Since(start)
		return responsedto.TestResult{
			Success:      false,
			Message:      "模型连接失败",
			Error:        err.Error(),
			ResponseTime: elapsed.Milliseconds(),
		}, nil
	}

	elapsed := time.Since(start)
	return responsedto.TestResult{
		Success:      true,
		Message:      "模型连接成功",
		ResponseTime: elapsed.Milliseconds(),
		Details:      "已发送测试请求并收到响应",
	}, nil
}

func toModelInfo(m entity.Model) responsedto.ModelInfo {
	return responsedto.ModelInfo{
		ID:        m.ID,
		Name:      m.Name,
		Provider:  m.Provider,
		ModelID:   m.ModelID,
		BaseURL:   m.BaseURL,
		APIKey:    m.APIKey,
		IsEnabled: m.IsEnabled,
	}
}
