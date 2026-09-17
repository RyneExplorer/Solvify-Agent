package shared

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"solvify-agent/internal/middleware"
	apperrors "solvify-agent/pkg/errors"
)

const (
	testUserID = "d0ec2864-778f-4451-ad16-d1b50b54e67a"
	testUUID   = "bd7468b1-0000-4000-8000-000000000000"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newCtx 构造带路径参数/请求体的 gin 上下文，返回上下文与其响应记录器。
func newCtx(params gin.Params, method, target, body string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	c.Request = req
	c.Params = params
	return c, w
}

// respCode 取出统一响应体里的业务码；未写响应时返回 -1。
// 注意 pkg/response 的 HTTP 状态码恒为 200，错误只能从业务码判断。
func respCode(t *testing.T, w *httptest.ResponseRecorder) int {
	t.Helper()
	if w.Body.Len() == 0 {
		return -1
	}
	var payload struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("响应不是合法 JSON: %s", w.Body.String())
	}
	return payload.Code
}

func TestUUIDParam(t *testing.T) {
	t.Run("合法 UUID 时不写响应", func(t *testing.T) {
		c, w := newCtx(gin.Params{{Key: "id", Value: testUUID}}, "GET", "/", "")
		got, ok := UUIDParam(c, "id", "知识库")
		if !ok || got != testUUID {
			t.Fatalf("期望 (%q, true)，实际 (%q, %v)", testUUID, got, ok)
		}
		if code := respCode(t, w); code != -1 {
			t.Fatalf("合法参数不应写响应，实际业务码=%d", code)
		}
	})

	t.Run("非 UUID 时返回 400", func(t *testing.T) {
		c, w := newCtx(gin.Params{{Key: "id", Value: "not-a-uuid"}}, "GET", "/", "")
		got, ok := UUIDParam(c, "id", "知识库")
		if ok || got != "" {
			t.Fatalf("非 UUID 应返回 ok=false，实际 (%q, %v)", got, ok)
		}
		if code := respCode(t, w); code != apperrors.CodeBadRequest {
			t.Fatalf("期望业务码 %d，实际 %d", apperrors.CodeBadRequest, code)
		}
	})

	t.Run("缺少路径参数时返回 400", func(t *testing.T) {
		c, w := newCtx(nil, "GET", "/", "")
		if _, ok := UUIDParam(c, "id", "知识库"); ok {
			t.Fatal("缺少参数应返回 ok=false")
		}
		if code := respCode(t, w); code != apperrors.CodeBadRequest {
			t.Fatalf("期望业务码 %d，实际 %d", apperrors.CodeBadRequest, code)
		}
	})
}

func TestUserAndUUIDParam(t *testing.T) {
	t.Run("未登录时返回 401", func(t *testing.T) {
		c, w := newCtx(gin.Params{{Key: "id", Value: testUUID}}, "GET", "/", "")
		if _, _, ok := UserAndUUIDParam(c, "id", "会话"); ok {
			t.Fatal("未登录应返回 ok=false")
		}
		if code := respCode(t, w); code != apperrors.CodeUnauthorized {
			t.Fatalf("期望业务码 %d，实际 %d", apperrors.CodeUnauthorized, code)
		}
	})

	t.Run("已登录且参数合法时不写响应", func(t *testing.T) {
		c, w := newCtx(gin.Params{{Key: "id", Value: testUUID}}, "GET", "/", "")
		c.Set(middleware.ContextUserID, testUserID)

		userID, value, ok := UserAndUUIDParam(c, "id", "会话")
		if !ok || userID != testUserID || value != testUUID {
			t.Fatalf("期望 (%q, %q, true)，实际 (%q, %q, %v)", testUserID, testUUID, userID, value, ok)
		}
		if code := respCode(t, w); code != -1 {
			t.Fatalf("合法请求不应写响应，实际业务码=%d", code)
		}
	})

	t.Run("已登录但路径参数非法时返回 400", func(t *testing.T) {
		c, w := newCtx(gin.Params{{Key: "id", Value: "bad"}}, "GET", "/", "")
		c.Set(middleware.ContextUserID, testUserID)

		if _, _, ok := UserAndUUIDParam(c, "id", "会话"); ok {
			t.Fatal("非法路径参数应返回 ok=false")
		}
		if code := respCode(t, w); code != apperrors.CodeBadRequest {
			t.Fatalf("期望业务码 %d，实际 %d", apperrors.CodeBadRequest, code)
		}
	})
}
