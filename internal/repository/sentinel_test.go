package repository

import (
	"errors"
	"fmt"
	"testing"

	"gorm.io/gorm"

	apperrors "solvify-agent/pkg/errors"
)

// TestRecordNotFoundSentinelIsRegistered 守住 sentinel.go 的 init。
//
// 登记一旦被删掉或挪走，「查不到记录 → 404」的整条链路会静默失效（又回到 500），
// 而 service / response 的测试因为自己也会触发 registration 而未必发现 ——
// 所以在这里单独钉一条，让「删掉登记」直接变红。
func TestRecordNotFoundSentinelIsRegistered(t *testing.T) {
	if !apperrors.IsNotFound(gorm.ErrRecordNotFound) {
		t.Fatal("gorm.ErrRecordNotFound 未被登记为「不存在」哨兵：查不到记录会重新退化成 500")
	}
}

func TestSentinelSurvivesWrapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"fmt.Errorf 包装", fmt.Errorf("repo 包装后: %w", gorm.ErrRecordNotFound), true},
		{"业务错误再包装", apperrors.WrapDefault(apperrors.CodeInternalError, gorm.ErrRecordNotFound), true},
		{"对照组：连接失败", errors.New("connection refused"), false},
		{"对照组：nil", nil, false},
	}
	for _, tc := range cases {
		if got := apperrors.IsNotFound(tc.err); got != tc.want {
			t.Errorf("%s: IsNotFound = %v，期望 %v", tc.name, got, tc.want)
		}
	}
}
