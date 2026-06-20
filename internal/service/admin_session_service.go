package service

import (
	"context"
	"time"

	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/repository"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/response"
)

// adminSessionService 管理员会话服务实现
type adminSessionService struct {
	sessionRepo repository.ChatSessionRepo
	messageRepo repository.ChatMessageRepo
}

// NewAdminSessionService 创建管理员会话服务
func NewAdminSessionService(sessionRepo repository.ChatSessionRepo, messageRepo repository.ChatMessageRepo) AdminSessionServiceInterface {
	return &adminSessionService{
		sessionRepo: sessionRepo,
		messageRepo: messageRepo,
	}
}

// List 管理员分页查询会话列表
func (s *adminSessionService) List(ctx context.Context, req *requestdto.AdminSessionListRequest) (*response.PageResponse, error) {
	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := (page - 1) * pageSize
	rows, total, err := s.sessionRepo.AdminList(ctx, offset, pageSize, req.Keyword, req.Status)
	if err != nil {
		return nil, err
	}

	list := make([]*dto.AdminSessionListItem, 0, len(rows))
	for _, row := range rows {
		list = append(list, &dto.AdminSessionListItem{
			ID:           row.ID,
			UserID:       row.UserID,
			Username:     row.Username,
			Title:        row.Title,
			ModelID:      row.ModelID,
			Status:       row.Status,
			MessageCount: row.MessageCount,
			CreatedAt:    row.CreatedAt,
			UpdatedAt:    row.UpdatedAt,
		})
	}

	return response.NewPageResponse(list, total, page, pageSize), nil
}

// Delete 管理员删除指定会话
func (s *adminSessionService) Delete(ctx context.Context, sessionID string) error {
	session, err := s.sessionRepo.FindByID(ctx, sessionID)
	if err != nil {
		return err
	}
	if session == nil {
		return apperrors.New(apperrors.CodeSessionNotFound, "会话不存在")
	}

	if err := s.messageRepo.DeleteBySessionID(ctx, sessionID); err != nil {
		return err
	}
	return s.sessionRepo.Delete(ctx, sessionID)
}

// CleanupExpired 清理过期会话，返回删除数量
func (s *adminSessionService) CleanupExpired(ctx context.Context, retentionDays int) (int64, error) {
	if retentionDays < 1 {
		retentionDays = 90
	}
	before := time.Now().AddDate(0, 0, -retentionDays)

	ids, err := s.sessionRepo.ListExpired(ctx, before)
	if err != nil {
		return 0, err
	}

	var deleted int64
	for _, id := range ids {
		if err := s.Delete(ctx, id); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}
