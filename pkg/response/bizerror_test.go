package response

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	// 空导入以触发 internal/repository 的 init —— 那里把 gorm.ErrRecordNotFound
	// 登记为「不存在」哨兵。本文件因此测的是**真实链路**，
	// 而不是在测试里另注册一遍（后者会掩盖「登记被删掉」这种失效）。
	_ "solvify-agent/internal/repository"

	apperrors "solvify-agent/pkg/errors"
)

// apiEnvelope 对应 Response 的 JSON 形态。
type apiEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	return c, w
}

func decodeEnvelope(t *testing.T, w *httptest.ResponseRecorder) apiEnvelope {
	t.Helper()
	var env apiEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("响应体不是合法 JSON: %v，原文=%s", err, w.Body.String())
	}
	return env
}

// TestBizErrorMapsRecordNotFoundTo404 是 P1-6 的出口级真回归。
//
// BizError 是全部错误的唯一出口，所以「repository 的哨兵跨过 service 之后
// 还会不会退化成 500」只需要在这里验证一次。此前 10 处 repository 原样上抛，
// 出口一律兜成 500「服务内部错误」，前端永远走错分支。
func TestBizErrorMapsRecordNotFoundTo404(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"裸 gorm.ErrRecordNotFound", gorm.ErrRecordNotFound, apperrors.CodeNotFound},
		{"service 用 fmt.Errorf 包了一层", fmt.Errorf("查询消息失败: %w", gorm.ErrRecordNotFound), apperrors.CodeNotFound},
		{"被兜底成服务内部错误（兜底码让位于已知事实）", apperrors.WrapDefault(apperrors.CodeInternalError, gorm.ErrRecordNotFound), apperrors.CodeNotFound},
		{"对照组：真未知故障仍是 500", errors.New("connection refused"), apperrors.CodeInternalError},
		{"对照组：具体业务码不被篡改", apperrors.NewDefault(apperrors.CodeSessionNotFound), apperrors.CodeSessionNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, w := newTestContext(t)
			BizError(c, tc.err)

			env := decodeEnvelope(t, w)
			if env.Code != tc.want {
				t.Fatalf("业务码 = %d，期望 %d（响应体=%s）", env.Code, tc.want, w.Body.String())
			}
		})
	}
}

// 出口的 HTTP 状态码恒为 200，错误只在响应体 code 里 —— 别顺手改成 404。
// 这条同时是给「路由层测试只断业务码」这条项目约定的守卫。
func TestBizErrorKeepsHTTPStatusOK(t *testing.T) {
	c, w := newTestContext(t)
	BizError(c, gorm.ErrRecordNotFound)

	if w.Code != 200 {
		t.Fatalf("BizError 的 HTTP 状态码应恒为 200，实际 %d", w.Code)
	}
}
