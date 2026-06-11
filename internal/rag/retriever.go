package rag

import "context"

// Retriever 定义检索器接口
type Retriever interface {
	Retrieve(ctx context.Context, query Query) (Result, error)
}

// Query 描述检索请求
type Query struct {
	Question         string
	TopK             int
	KnowledgeBaseIDs []string
	UserID           string
}

// Result 描述检索结果
type Result struct {
	Hit       bool
	Documents []Document
}

// Document 描述检索到的文档片段
type Document struct {
	ID              string
	KnowledgeBaseID string
	DocumentID      string
	Title           string
	Content         string
	Score           float64
}
