package rag

import (
	"context"
	"strconv"
	"strings"
)

// EmbeddingFunc 定义文本向量化函数签名
type EmbeddingFunc func(ctx context.Context, text string) ([]float64, error)

// vectorToString 将浮点数切片转换为 PostgresSQL vector 字符串格式
func vectorToString(vec []float64) string {
	if len(vec) == 0 {
		return "[]"
	}
	// 预分配 buffer：每个数最多 20 字符 + 逗号 + 括号
	var sb strings.Builder
	sb.Grow(len(vec)*21 + 2)
	sb.WriteString("[")
	for i, v := range vec {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatFloat(v, 'f', 6, 64))
	}
	sb.WriteString("]")
	return sb.String()
}
