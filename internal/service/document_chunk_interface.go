package service

import (
	"context"

	"solvify-agent/internal/model/entity"
)

// DocumentChunkServiceInterface 定义文档分块服务接口
type DocumentChunkServiceInterface interface {
	SupportsFileType(fileType string) bool
	NormalizeContent(content, fileType string) string
	SplitContent(content string) []string
	BuildChunks(ctx context.Context, doc entity.Document, versionID string, contents []string) ([]entity.DocumentChunk, error)
}
