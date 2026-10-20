package tool

import (
	"context"
	"encoding/json"
	"testing"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
)

type listScopeKBRepo struct {
	repository.KnowledgeBaseRepository
	count int
}

func (r *listScopeKBRepo) ListNormal(context.Context, string, int) ([]entity.KnowledgeBase, error) {
	r.count++
	return []entity.KnowledgeBase{{ID: "a", Name: "所选库"}, {ID: "b", Name: "其他库的敏感描述"}}, nil
}

func TestKnowledgeBaseListingDoesNotExpandSelection(t *testing.T) {
	for _, scope := range [][]string{{"a"}, {"missing"}, nil} {
		repo := &listScopeKBRepo{}
		selected := append([]string(nil), scope...)
		tool := NewListKnowledgeBasesTool(repo)("u", selected)
		if len(selected) > 0 {
			selected[0] = "b"
		}
		raw, err := tool.InvokableRun(context.Background(), `{"include_stats":false}`)
		var result struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err != nil || json.Unmarshal([]byte(raw), &result) != nil {
			t.Fatalf("列表调用失败：%s %v", raw, err)
		}
		if len(scope) == 1 && scope[0] == "a" {
			if len(result.Data) != 1 || result.Data[0].ID != "a" {
				t.Fatalf("列表泄露未选中的库：%s", raw)
			}
		} else if len(result.Data) != 0 {
			t.Fatalf("空/不存在范围仍返回库：%s", raw)
		}
		if len(scope) == 0 && repo.count != 0 {
			t.Fatal("空范围仍查询列表")
		}
	}
}
