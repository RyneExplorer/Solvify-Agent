package service

import (
	"context"

	"solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
)

// UserPreferenceService 用户偏好服务接口
type UserPreferenceService interface {
	GetByUserID(ctx context.Context, userID string) (*entity.UserPreference, error)
	Upsert(ctx context.Context, userID string, req *request.UpdateUserPreferenceRequest) (*dto.UserPreferenceResponse, error)
	ToDTO(p *entity.UserPreference) *dto.UserPreferenceResponse
}
