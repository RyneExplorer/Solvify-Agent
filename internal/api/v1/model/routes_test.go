package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"solvify-agent/internal/middleware"
	requestdto "solvify-agent/internal/model/dto/request"
	responsedto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/service"
	apperrors "solvify-agent/pkg/errors"
)

// ─── model 模块路由层测试（P0-2 回归）─────────────────────────────────────────
//
// 被钉住的结论是「读写分权」：
//   - 读（List / GetByID）对已登录用户开放 —— 问答页、设置页都要用它选模型，
//     收进 admin 组会把普通用户打断；安全性由 service 层脱敏兜底。
//   - 写（Create / Update / Delete / Test）必须管理员 —— 否则任意登录用户可改删
//     系统模型，或借 /models/test 让服务器向任意 base_url 发出站请求（半个 SSRF）。
//
// 断言口径沿用项目约定：pkg/response 的错误响应 HTTP 状态码**恒为 200**，
// 错误只体现在响应体 code 字段。所以除「路径不存在」断 HTTP 404 外，
// 其余一律断业务码。角色用 int 注入（middleware.GetUserRole 是 role.(int)）。

const testUserID = "d0ec2864-778f-4451-ad16-d1b50b54e67a"

// 角色取值：1 = 普通用户，2 = 管理员（见 middleware.RequireAdmin → RequireRole(2)）。
const (
	roleUser  = 1
	roleAdmin = 2
)

func init() { gin.SetMode(gin.TestMode) }

// stubModelService 记录调用次数，用于验证「被拦截的请求不该触达服务层」。
// 未实现的方法由嵌入接口兜底 —— 用例若走到未实现的方法会直接 panic，
// 比默默返回零值更容易发现用例失效。
type stubModelService struct {
	service.ModelServiceInterface

	createCalled int
	updateCalled int
	deleteCalled int
	testCalled   int
}

func (s *stubModelService) List(context.Context) (responsedto.ListModelsResponse, error) {
	return responsedto.ListModelsResponse{
		Models: []responsedto.ModelInfo{
			{ID: "m-1", Name: "gpt-5.5", Provider: "openai", ModelID: "gpt-5.5", APIKey: "sk-****1b0c"},
		},
	}, nil
}

func (s *stubModelService) GetByID(context.Context, string) (responsedto.ModelInfo, error) {
	return responsedto.ModelInfo{ID: "m-1", Name: "gpt-5.5", APIKey: "sk-****1b0c"}, nil
}

func (s *stubModelService) Create(context.Context, requestdto.CreateModelRequest) (responsedto.ModelInfo, error) {
	s.createCalled++
	return responsedto.ModelInfo{ID: "m-new", APIKey: "sk-****1b0c"}, nil
}

func (s *stubModelService) Update(context.Context, string, requestdto.UpdateModelRequest) error {
	s.updateCalled++
	return nil
}

func (s *stubModelService) Delete(context.Context, string) error {
	s.deleteCalled++
	return nil
}

func (s *stubModelService) Test(context.Context, requestdto.TestModelRequest) (responsedto.TestResult, error) {
	s.testCalled++
	return responsedto.TestResult{Success: true, Message: "模型连接成功"}, nil
}

// newTestRouter 挂载 model 模块路由。role 传 0 表示不注入任何登录态。
func newTestRouter(svc service.ModelServiceInterface, role int) *gin.Engine {
	r := gin.New()
	v1 := r.Group("/api/v1")
	if role != 0 {
		v1.Use(func(c *gin.Context) {
			c.Set(middleware.ContextUserID, testUserID)
			c.Set(middleware.ContextUserRole, role)
			c.Next()
		})
	}
	NewController(svc).RegisterRoutes(v1)
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

// ── 读：普通用户必须能用 ────────────────────────────────────────────────────

func TestModelRoutes_List_AllowsNormalUser(t *testing.T) {
	w := send(newTestRouter(&stubModelService{}, roleUser), http.MethodGet, "/api/v1/models", "")

	e := decode(t, w)
	if e.Code != apperrors.CodeSuccess {
		t.Fatalf("普通用户拉模型列表被拒：code=%d(%s)；问答页/设置页会因此选不到模型", e.Code, e.Message)
	}
	if !strings.Contains(string(e.Data), "gpt-5.5") {
		t.Errorf("data 未携带模型列表: %s", e.Data)
	}
	// 响应里不该出现任何完整密钥形态。
	if strings.Contains(string(e.Data), "sk-live") {
		t.Errorf("列表响应疑似携带明文密钥: %s", e.Data)
	}
}

func TestModelRoutes_GetByID_AllowsNormalUser(t *testing.T) {
	w := send(newTestRouter(&stubModelService{}, roleUser), http.MethodGet, "/api/v1/models/m-1", "")

	e := decode(t, w)
	if e.Code != apperrors.CodeSuccess {
		t.Fatalf("普通用户查单个模型被拒：code=%d(%s)", e.Code, e.Message)
	}
}

// ── 写：普通用户必须被拦，且不得触达服务层 ───────────────────────────────────

func TestModelRoutes_WriteEndpoints_ForbiddenForNormalUser(t *testing.T) {
	cases := []struct {
		name   string
		method string
		target string
		body   string
		called func(*stubModelService) int
	}{
		{"新增系统模型", http.MethodPost, "/api/v1/models", `{"provider":"openai","model_id":"gpt-5.5"}`, func(s *stubModelService) int { return s.createCalled }},
		{"修改系统模型", http.MethodPut, "/api/v1/models/m-1", `{"model_id":"gpt-5.5"}`, func(s *stubModelService) int { return s.updateCalled }},
		{"删除系统模型", http.MethodDelete, "/api/v1/models/m-1", "", func(s *stubModelService) int { return s.deleteCalled }},
		{"探活任意地址(/models/test 可被当 SSRF 用)", http.MethodPost, "/api/v1/models/test", `{"provider":"openai","model_id":"x","base_url":"http://169.254.169.254/"}`, func(s *stubModelService) int { return s.testCalled }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &stubModelService{}
			w := send(newTestRouter(svc, roleUser), c.method, c.target, c.body)

			if w.Code != http.StatusOK {
				t.Fatalf("HTTP 状态码 = %d，期望 200（本项目错误也用 200 承载）", w.Code)
			}
			e := decode(t, w)
			if e.Code != apperrors.CodeForbidden {
				t.Fatalf("业务码 = %d(%s)，期望 %d（无权限）", e.Code, e.Message, apperrors.CodeForbidden)
			}
			if n := c.called(svc); n != 0 {
				t.Fatalf("请求已被拦截，但服务层仍被调用了 %d 次", n)
			}
		})
	}
}

// 没有任何角色上下文（未登录）同样要拦。
func TestModelRoutes_WriteWithoutRole_Forbidden(t *testing.T) {
	svc := &stubModelService{}
	w := send(newTestRouter(svc, 0), http.MethodPost, "/api/v1/models", `{"provider":"openai","model_id":"gpt-5.5"}`)

	e := decode(t, w)
	if e.Code != apperrors.CodeForbidden {
		t.Fatalf("业务码 = %d(%s)，期望 %d", e.Code, e.Message, apperrors.CodeForbidden)
	}
	if svc.createCalled != 0 {
		t.Fatal("无角色上下文时服务层仍被调用")
	}
}

// ── 写：管理员必须能过 ─────────────────────────────────────────────────────
//
// 请求体必须满足 binding 校验，否则会先被 400 拦下、测不到权限分支：
// CreateModelRequest 的 provider/model_id 为 required，max_context_length 为 min=1024。

const validCreateBody = `{"provider":"openai","model_id":"gpt-5.5","max_context_length":8192}`

func TestModelRoutes_Create_AllowsAdmin(t *testing.T) {
	svc := &stubModelService{}
	w := send(newTestRouter(svc, roleAdmin), http.MethodPost, "/api/v1/models", validCreateBody)

	e := decode(t, w)
	if e.Code != apperrors.CodeSuccess {
		t.Fatalf("管理员新增系统模型被拒：code=%d(%s)", e.Code, e.Message)
	}
	if svc.createCalled != 1 {
		t.Fatalf("管理员请求未触达服务层，createCalled=%d", svc.createCalled)
	}
}

func TestModelRoutes_Test_AllowsAdmin(t *testing.T) {
	svc := &stubModelService{}
	w := send(newTestRouter(svc, roleAdmin), http.MethodPost, "/api/v1/models/test", `{"provider":"openai","model_id":"x","base_url":"https://example.com/v1","api_key":"sk-x"}`)

	e := decode(t, w)
	if e.Code != apperrors.CodeSuccess {
		t.Fatalf("管理员测试模型被拒：code=%d(%s)", e.Code, e.Message)
	}
	if svc.testCalled != 1 {
		t.Fatalf("管理员请求未触达服务层，testCalled=%d", svc.testCalled)
	}
}

// ── 不应存在的路径 ─────────────────────────────────────────────────────────

func TestModelRoutes_UnknownPath_Returns404(t *testing.T) {
	w := send(newTestRouter(&stubModelService{}, roleAdmin), http.MethodGet, "/api/v1/models/m-1/extra", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("HTTP 状态码 = %d，期望 404（路由未挂 NoRoute handler）", w.Code)
	}
}
