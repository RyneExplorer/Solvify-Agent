package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"solvify-agent/internal/middleware"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/service"
	apperrors "solvify-agent/pkg/errors"
)

// ─── chat 模块路由层测试 ────────────────────────────────────────────────────
//
// 覆盖 AGENTS.md 对路由测试的四项要求：正常路径、参数格式错误、关键业务错误、
// 不应存在的路径；另补两条边界：未登录、管理员路由角色不足。
//
// 断言口径说明：pkg/response 的 HTTP 状态码**恒为 200**，错误只体现在响应体的 code
// 字段（见 pkg/response.Error）。所以除「路径不存在」那条断 HTTP 404 外，
// 其余一律断业务码，不断 HTTP 状态码 —— 断 HTTP 码只会永远通过，等于没测。
//
// 实现细节上不依赖具体提示文案（并发重构正在把「请求体格式错误」统一成「请求参数错误」），
// 只钉业务码与「错误信息被原样透传」，避免测试绑死在会漂移的字符串上。

const (
	testUserID    = "d0ec2864-778f-4451-ad16-d1b50b54e67a"
	testSessionID = "bd7468b1-0000-4000-8000-000000000000"
)

func init() { gin.SetMode(gin.TestMode) }

// stubChatService 只实现本文件真正走到的方法，其余由嵌入的接口兜底。
// 这样一旦用例走到了未实现的方法会直接 panic —— 比默默返回零值更容易发现用例失效。
type stubChatService struct {
	service.ChatServiceInterface

	sessions []dto.SessionResponse
	getErr   error
}

func (s *stubChatService) ListSessions(context.Context, string) ([]dto.SessionResponse, error) {
	return s.sessions, nil
}

func (s *stubChatService) GetSession(context.Context, string, string) (dto.SessionResponse, error) {
	return dto.SessionResponse{}, s.getErr
}

// newTestRouter 挂载 chat 模块路由。role 传 0 表示不注入登录态（模拟未登录）。
func newTestRouter(svc service.ChatServiceInterface, role int) *gin.Engine {
	r := gin.New()
	v1 := r.Group("/api/v1")
	if role != 0 {
		v1.Use(func(c *gin.Context) {
			c.Set(middleware.ContextUserID, testUserID)
			c.Set(middleware.ContextUserRole, role)
			c.Next()
		})
	}
	NewController(svc, nil).RegisterRoutes(v1)
	return r
}

func send(r *gin.Engine, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func decode(t *testing.T, w *httptest.ResponseRecorder) envelope {
	t.Helper()
	var e envelope
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("响应不是合法 JSON: %s", w.Body.String())
	}
	return e
}

// 正常路径：已登录 + 合法路由 → 业务码 0，且返回体里带回服务层的数据。
func TestChatRoutes_ListSessions_HappyPath(t *testing.T) {
	svc := &stubChatService{sessions: []dto.SessionResponse{{ID: testSessionID, Title: "会话一"}}}

	w := send(newTestRouter(svc, 1), http.MethodGet, "/api/v1/chat/sessions", "")
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP 状态码 = %d，期望 200", w.Code)
	}

	e := decode(t, w)
	if e.Code != apperrors.CodeSuccess {
		t.Fatalf("业务码 = %d(%s)，期望 %d", e.Code, e.Message, apperrors.CodeSuccess)
	}
	// 控制器把列表包在 {"sessions": [...]} 里
	if !strings.Contains(string(e.Data), testSessionID) {
		t.Errorf("data 未携带服务层返回的会话，实际: %s", e.Data)
	}
}

// 参数格式错误：路径参数不是 UUID → 400，且不该触达服务层。
func TestChatRoutes_InvalidSessionID_ReturnsBadRequest(t *testing.T) {
	svc := &stubChatService{getErr: apperrors.New(apperrors.CodeSessionNotFound, "不该被调用到")}

	w := send(newTestRouter(svc, 1), http.MethodGet, "/api/v1/chat/sessions/not-a-uuid", "")
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP 状态码 = %d，期望 200（本项目错误也用 200 承载）", w.Code)
	}

	e := decode(t, w)
	if e.Code != apperrors.CodeBadRequest {
		t.Fatalf("业务码 = %d(%s)，期望 %d", e.Code, e.Message, apperrors.CodeBadRequest)
	}
	if e.Message == "不该被调用到" {
		t.Error("路径参数非法时仍调用了服务层")
	}
}

// 参数格式错误：请求体不是合法 JSON → 400。
func TestChatRoutes_MalformedJSONBody_ReturnsBadRequest(t *testing.T) {
	w := send(newTestRouter(&stubChatService{}, 1), http.MethodPost, "/api/v1/chat/sessions", "{")

	e := decode(t, w)
	if e.Code != apperrors.CodeBadRequest {
		t.Fatalf("业务码 = %d(%s)，期望 %d", e.Code, e.Message, apperrors.CodeBadRequest)
	}
}

// 关键业务错误：服务层返回 BizError → 业务码与文案原样透传，不被当成 500。
func TestChatRoutes_BizError_PassesCodeAndMessage(t *testing.T) {
	svc := &stubChatService{getErr: apperrors.New(apperrors.CodeSessionNotFound, "会话不存在")}

	w := send(newTestRouter(svc, 1), http.MethodGet, "/api/v1/chat/sessions/"+testSessionID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("HTTP 状态码 = %d，期望 200", w.Code)
	}

	e := decode(t, w)
	if e.Code != apperrors.CodeSessionNotFound {
		t.Fatalf("业务码 = %d，期望 %d", e.Code, apperrors.CodeSessionNotFound)
	}
	if e.Message != "会话不存在" {
		t.Errorf("业务文案 = %q，期望原样透传 %q", e.Message, "会话不存在")
	}
}

// 不应存在的路径：既不加路由，也不落到别的 handler 上。
func TestChatRoutes_UnknownPath_Returns404(t *testing.T) {
	w := send(newTestRouter(&stubChatService{}, 1), http.MethodGet, "/api/v1/chat/nope", "")

	if w.Code != http.StatusNotFound {
		t.Fatalf("HTTP 状态码 = %d，期望 404（该路径本就不该存在，不能悄悄命中别的 handler）", w.Code)
	}
}

// 未登录：CurrentUserID 写 401 并中止，服务层不被调用。
func TestChatRoutes_Unauthenticated_Returns401(t *testing.T) {
	svc := &stubChatService{sessions: []dto.SessionResponse{{ID: testSessionID}}}

	e := decode(t, send(newTestRouter(svc, 0), http.MethodGet, "/api/v1/chat/sessions", ""))
	if e.Code != apperrors.CodeUnauthorized {
		t.Fatalf("业务码 = %d(%s)，期望 %d", e.Code, e.Message, apperrors.CodeUnauthorized)
	}
}

// 管理员路由：普通用户（role=1）被 RequireAdmin 挡在 handler 之外 → 403。
func TestChatRoutes_AdminRoute_RequiresAdminRole(t *testing.T) {
	e := decode(t, send(newTestRouter(&stubChatService{}, 1), http.MethodGet, "/api/v1/admin/sessions", ""))
	if e.Code != apperrors.CodeForbidden {
		t.Fatalf("业务码 = %d(%s)，期望 %d", e.Code, e.Message, apperrors.CodeForbidden)
	}
}
