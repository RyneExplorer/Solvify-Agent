package service

import "context"

// EmbeddingServiceInterface 定义文本向量生成服务接口
type EmbeddingServiceInterface interface {
	EmbedTexts(ctx context.Context, texts []string) ([][]float32, error)
	Model() string
	Dimension() int
}
