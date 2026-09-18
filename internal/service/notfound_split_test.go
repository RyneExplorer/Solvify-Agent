package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"testing"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	apperrors "solvify-agent/pkg/errors"
)

// ─── P1-6：service 层必须把「记录不存在」与「查询失败」分开 ─────────────────
//
// 此前这些位置的写法是 `if err != nil { return 工具类型不存在 }` ——
// 数据库故障也会回报成「不存在」，用户据此重试却永远不会成功；
// 另一批位置反过来，一律包成「服务内部错误」，于是查不到记录也返回 500。
//
// 这组用例**两个方向都断言**：只测「不存在 → 业务码」的话，
// 「一律当不存在」的实现照样通过，等于没测。

type fakeToolTypeRepo struct {
	getByIDErr error
	getByIDVal *entity.ToolType
}

func (f *fakeToolTypeRepo) Create(context.Context, *entity.ToolType) error { return nil }
func (f *fakeToolTypeRepo) Update(context.Context, *entity.ToolType) error { return nil }
func (f *fakeToolTypeRepo) Delete(context.Context, string) error           { return nil }

func (f *fakeToolTypeRepo) GetByID(context.Context, string) (*entity.ToolType, error) {
	if f.getByIDErr != nil {
		return nil, f.getByIDErr
	}
	if f.getByIDVal != nil {
		return f.getByIDVal, nil
	}
	return &entity.ToolType{ID: "t-1", Name: "示例类型", ToolKey: "demo"}, nil
}

func (f *fakeToolTypeRepo) GetByKey(context.Context, string) (*entity.ToolType, error) {
	return nil, nil
}
func (f *fakeToolTypeRepo) List(context.Context) ([]entity.ToolType, error)        { return nil, nil }
func (f *fakeToolTypeRepo) ListEnabled(context.Context) ([]entity.ToolType, error) { return nil, nil }
func (f *fakeToolTypeRepo) ExistsByKey(context.Context, string) (bool, error)      { return false, nil }
func (f *fakeToolTypeRepo) GetProviderCounts(context.Context) (map[string]int, error) {
	return map[string]int{}, nil
}

var _ repository.ToolTypeRepository = (*fakeToolTypeRepo)(nil)

func TestToolTypeServiceGetByIDSplitsNotFoundFromQueryFailure(t *testing.T) {
	cases := []struct {
		name    string
		repoErr error
		want    int
	}{
		{
			name:    "记录不存在 → 工具类型不存在",
			repoErr: gorm.ErrRecordNotFound,
			want:    apperrors.CodeToolTypeNotFound,
		},
		{
			name:    "包装过的不存在 → 仍识别为工具类型不存在",
			repoErr: fmt.Errorf("query tool_type: %w", gorm.ErrRecordNotFound),
			want:    apperrors.CodeToolTypeNotFound,
		},
		{
			name:    "查询失败 → 服务内部错误（不再伪装成「不存在」）",
			repoErr: stderrors.New("connection refused"),
			want:    apperrors.CodeInternalError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewToolTypeService(&fakeToolTypeRepo{getByIDErr: tc.repoErr})

			_, err := svc.GetByID(context.Background(), "t-1")
			if err == nil {
				t.Fatal("期望返回错误，实际 nil")
			}

			var bizErr *apperrors.BizError
			if !stderrors.As(err, &bizErr) {
				t.Fatalf("期望 *BizError，实际 %T: %v", err, err)
			}
			if bizErr.Code != tc.want {
				t.Fatalf("业务码 = %d，期望 %d", bizErr.Code, tc.want)
			}
			// 排障要求：原始错误必须留在链上，否则「服务内部错误」永远查不出真因。
			if !stderrors.Is(err, tc.repoErr) {
				t.Error("原始错误丢失：错误链里找不到 repo 返回的那个错误")
			}
		})
	}
}

// 对照组：查询成功时不应产生任何错误码 —— 防止上面的分流写成「总是报错」。
func TestToolTypeServiceGetByIDSuccessPath(t *testing.T) {
	svc := NewToolTypeService(&fakeToolTypeRepo{})

	info, err := svc.GetByID(context.Background(), "t-1")
	if err != nil {
		t.Fatalf("查询成功路径不应报错，实际 %v", err)
	}
	if info == nil {
		t.Fatal("查询成功路径应返回工具类型")
	}
}
