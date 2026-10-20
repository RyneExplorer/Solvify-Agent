package tool

import (
	"context"
	"encoding/json"
	"testing"

	"solvify-agent/internal/repository"
)

type citationChunkRepo struct {
	repository.DocumentChunkRepository
	user string
}

func (r *citationChunkRepo) SearchByKeywordInKnowledgeBases(ctx context.Context, user string, kbIDs []string, query string, limit int) ([]repository.DocumentSearchRow, error) {
	r.user = user
	return []repository.DocumentSearchRow{{ID: "chunk-select", DocumentID: "doc-select", KnowledgeBaseID: "kb-go", Title: "select详解", Content: "select正文"}}, nil
}

func TestGrepChunksCarriesActualCitationIdentity(t *testing.T) {
	repo := &citationChunkRepo{}
	tool := NewGrepChunksTool(repo)("trusted-user", []string{"kb-go"})
	raw, err := tool.InvokableRun(context.Background(), `{"keyword":"select"}`)
	var result struct {
		Success bool `json:"success"`
		Data    []struct {
			ChunkID         string `json:"chunk_id"`
			DocumentID      string `json:"document_id"`
			KnowledgeBaseID string `json:"knowledge_base_id"`
			Title           string `json:"title"`
		} `json:"data"`
	}
	if err != nil || json.Unmarshal([]byte(raw), &result) != nil || !result.Success || len(result.Data) != 1 {
		t.Fatal("关键词工具失败", raw, err)
	}
	source := result.Data[0]
	if source.ChunkID != "chunk-select" || source.DocumentID != "doc-select" || source.KnowledgeBaseID != "kb-go" || source.Title != "select详解" || repo.user != "trusted-user" {
		t.Fatalf("来源身份与仓库结果不一致: %+v", source)
	}
}
