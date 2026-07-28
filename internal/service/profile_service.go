package service

import (
	"context"
	"encoding/json"

	"solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	apperrors "solvify-agent/pkg/errors"
)

type userPreferenceService struct {
	repo repository.UserPreferenceRepository
}

func NewUserPreferenceService(repo repository.UserPreferenceRepository) UserPreferenceService {
	return &userPreferenceService{repo: repo}
}

func (s *userPreferenceService) GetByUserID(_ context.Context, userID string) (*entity.UserPreference, error) {
	p, err := s.repo.FindByUserID(userID)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	if p == nil {
		p = s.defaultPreference(userID)
	}
	return p, nil
}

func (s *userPreferenceService) Upsert(_ context.Context, userID string, req *request.UpdateUserPreferenceRequest) (*dto.UserPreferenceResponse, error) {
	cur, err := s.repo.FindByUserID(userID)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	if cur == nil {
		cur = s.defaultPreference(userID)
	}
	if req.DefaultModelID != nil {
		cur.DefaultModelID = *req.DefaultModelID
	}
	if req.PreferredKBIDs != nil {
		kbJSON, _ := json.Marshal(req.PreferredKBIDs)
		cur.PreferredKBIDs = kbJSON
	}
	if req.AnswerStyle != "" {
		cur.AnswerStyle = req.AnswerStyle
	}
	if req.AutoDeepMode != nil {
		cur.AutoDeepMode = *req.AutoDeepMode
	}
	if req.AutoDeepThreshold != nil {
		cur.AutoDeepThreshold = *req.AutoDeepThreshold
	}
	if req.UseMarkdownTable != nil {
		cur.UseMarkdownTable = *req.UseMarkdownTable
	}
	if req.CitationStyle != "" {
		cur.CitationStyle = req.CitationStyle
	}

	if err := s.repo.Upsert(cur); err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	updated, _ := s.repo.FindByUserID(userID)
	return s.ToDTO(updated), nil
}

func (s *userPreferenceService) ToDTO(p *entity.UserPreference) *dto.UserPreferenceResponse {
	if p == nil {
		return s.ToDTO(s.defaultPreference(""))
	}
	kbs := []string{}
	if len(p.PreferredKBIDs) > 0 {
		_ = json.Unmarshal(p.PreferredKBIDs, &kbs)
	}
	updated := ""
	if !p.UpdatedAt.IsZero() {
		updated = p.UpdatedAt.Format("2006-01-02 15:04:05")
	}
	return &dto.UserPreferenceResponse{
		UserID:            p.UserID,
		DefaultModelID:    p.DefaultModelID,
		PreferredKBIDs:    kbs,
		AnswerStyle:       p.AnswerStyle,
		AutoDeepMode:      p.AutoDeepMode,
		AutoDeepThreshold: p.AutoDeepThreshold,
		UseMarkdownTable:  p.UseMarkdownTable,
		CitationStyle:     p.CitationStyle,
		UpdatedAt:         updated,
	}
}

func (*userPreferenceService) defaultPreference(userID string) *entity.UserPreference {
	return &entity.UserPreference{
		UserID:            userID,
		AnswerStyle:       "balanced",
		AutoDeepMode:      false,
		AutoDeepThreshold: 2,
		UseMarkdownTable:  true,
		CitationStyle:     "section_title",
	}
}
