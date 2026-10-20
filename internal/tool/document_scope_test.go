package tool

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
)

type scopedChunkRepo struct {
	repository.DocumentChunkRepository
	rows        []repository.DocumentSearchRow
	user        string
	kbIDs       []string
	globalCalls int
}

func (r *scopedChunkRepo) SearchByKeyword(context.Context, string, string, int) ([]repository.DocumentSearchRow, error) {
	r.globalCalls++
	return r.rows, nil
}

func (r *scopedChunkRepo) SearchByKeywordInKnowledgeBases(_ context.Context, user string, kbIDs []string, _ string, _ int) ([]repository.DocumentSearchRow, error) {
	r.user, r.kbIDs = user, slices.Clone(kbIDs)
	var rows []repository.DocumentSearchRow
	for _, row := range r.rows {
		if slices.Contains(kbIDs, row.KnowledgeBaseID) {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

func TestGrepChunksSelectedKnowledgeBaseScope(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kbIDs   []string
		wantIDs []string
	}{
		{"单库", []string{"kb-a"}, []string{"chunk-a"}},
		{"多库", []string{"kb-a", "kb-b"}, []string{"chunk-a", "chunk-b"}},
		{"不存在的库", []string{"missing"}, nil},
		{"未选择", nil, nil},
		{"空范围", []string{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &scopedChunkRepo{rows: []repository.DocumentSearchRow{
				{ID: "chunk-a", KnowledgeBaseID: "kb-a", Content: "同一关键词"},
				{ID: "chunk-b", KnowledgeBaseID: "kb-b", Content: "同一关键词"},
			}}
			scope := slices.Clone(tc.kbIDs)
			tool := NewGrepChunksTool(repo)("trusted-user", scope)
			// 工具权限应捕获本次范围，不能随调用方切片变化而扩大。
			if len(scope) > 0 {
				scope[0] = "kb-b"
			}
			raw, err := tool.InvokableRun(context.Background(), `{"keyword":"同一关键词","user_id":"other","kbIDs":["kb-b"]}`)
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Success bool `json:"success"`
				Data    []struct {
					ID string `json:"chunk_id"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(raw), &result); err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, row := range result.Data {
				ids = append(ids, row.ID)
			}
			if !slices.Equal(ids, tc.wantIDs) || repo.globalCalls != 0 {
				t.Fatalf("工具越过所选范围：ids=%v globalCalls=%d result=%s", ids, repo.globalCalls, raw)
			}
			if len(tc.kbIDs) == 0 {
				if result.Success || repo.user != "" {
					t.Fatalf("空范围不应读取资料：%s", raw)
				}
			} else if !result.Success || repo.user != "trusted-user" || !slices.Equal(repo.kbIDs, tc.kbIDs) {
				t.Fatalf("可信身份或所选范围未传递：user=%s kbIDs=%v result=%s", repo.user, repo.kbIDs, raw)
			}
		})
	}
}

type scopedDocumentRepo struct {
	repository.DocumentRepository
	doc   entity.Document
	user  string
	calls int
}

func (r *scopedDocumentRepo) FindByID(_ context.Context, user, id string, _ int) (entity.Document, bool, error) {
	r.user = user
	r.calls++
	return r.doc, r.doc.ID == id && r.doc.UserID == user, nil
}

func TestGetDocumentInfoSelectedKnowledgeBaseScope(t *testing.T) {
	for _, tc := range []struct {
		name    string
		kbIDs   []string
		docUser string
		allowed bool
	}{
		{"所选库", []string{"kb-a"}, "trusted-user", true},
		{"多库", []string{"kb-b", "kb-a"}, "trusted-user", true},
		{"其他库", []string{"kb-b"}, "trusted-user", false},
		{"不存在的库", []string{"missing"}, "trusted-user", false},
		{"未选择", nil, "trusted-user", false},
		{"空范围", []string{}, "trusted-user", false},
		{"其他用户", []string{"kb-a"}, "other-user", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &scopedDocumentRepo{doc: entity.Document{ID: "doc-a", UserID: tc.docUser, KnowledgeBaseID: "kb-a", Title: "仅所选范围可见的标题"}}
			scope := slices.Clone(tc.kbIDs)
			tool := NewGetDocumentInfoTool(repo)("trusted-user", scope)
			if len(scope) > 0 {
				scope[0] = "kb-a"
			}
			raw, err := tool.InvokableRun(context.Background(), `{"document_id":"doc-a","user_id":"other-user","kbIDs":["kb-a"]}`)
			if err != nil {
				t.Fatal(err)
			}
			var result ToolResponse
			if err := json.Unmarshal([]byte(raw), &result); err != nil {
				t.Fatal(err)
			}
			if result.Success != tc.allowed || (!tc.allowed && (result.Data != nil || strings.Contains(raw, repo.doc.Title))) {
				t.Fatalf("文档详情范围检查错误：%s", raw)
			}
			if len(tc.kbIDs) == 0 {
				if repo.calls != 0 {
					t.Fatal("空范围不应读取文档")
				}
			} else if repo.user != "trusted-user" {
				t.Fatal("文档查询未使用可信身份")
			}
		})
	}
}
