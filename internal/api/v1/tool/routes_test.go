package tool

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

// ─── tool 模块路由层测试 ─────────────────────────────────────────────────────
//
// 只钉住新增的用户侧 MCP 探测路由 /api/v1/user/tools/mcp/probe：
//   - 普通用户必须可用 —— 设置页 MCP 标签页靠它刷新工具清单
//   - provider_type 缺失时被 400（业务码）拦下，不得触达服务层
//
// 断言口径沿用项目约定：pkg/response 的错误响应 HTTP 状态码恒为 200。
// 角色 int 注入（1 = 普通用户），与 model 模块路由测试一致。

const testUserID = "d0ec2864-778f-4451-ad16-d1b50b54e67a"

func init() { gin.SetMode(gin.TestMode) }

type stubToolServices struct {
	provider service.ToolProviderService
	probe    *stubProviderService
}

type stubTypeService struct{ service.ToolTypeService }
type stubUserConfigService struct{ service.UserToolConfigService }

type stubProviderService struct {
	service.ToolProviderService
	probeCalled int
}

func (s *stubProviderService) ProbeMCPTools(_ context.Context, _ requestdto.TestToolRequest) (*responsedto.TestResult, error) {
	s.probeCalled++
	return &responsedto.TestResult{Success: true, Message: "探测成功"}, nil
}

func newTestRouter(svc *stubToolServices, role int) *gin.Engine {
	r := gin.New()
	v1 := r.Group("/api/v1")
	if role != 0 {
		v1.Use(func(c *gin.Context) {
			c.Set(middleware.ContextUserID, testUserID)
			c.Set(middleware.ContextUserRole, role)
			c.Next()
		})
	}
	NewController(&stubTypeService{}, svc.provider, &stubUserConfigService{}).RegisterRoutes(v1)
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

// 普通用户探测 MCP 工具清单必须可用（设置页 MCP 标签页依赖此接口）
func TestToolRoutes_UserMCPProbe_AllowsNormalUser(t *testing.T) {
	probe := &stubProviderService{}
	w := send(newTestRouter(&stubToolServices{provider: probe}, 1), http.MethodPost, "/api/v1/user/tools/mcp/probe", `{"provider_type":"mcp","provider_id":"p-1"}`)

	e := decode(t, w)
	if e.Code != apperrors.CodeSuccess {
		t.Fatalf("普通用户探测 MCP 被拒：code=%d(%s)", e.Code, e.Message)
	}
	if probe.probeCalled != 1 {
		t.Fatalf("服务层探测未被调用，probeCalled=%d", probe.probeCalled)
	}
}

// provider_type 缺失时被参数校验拦下，服务层不得被触达
func TestToolRoutes_UserMCPProbe_MissingProviderType_BadRequest(t *testing.T) {
	probe := &stubProviderService{}
	w := send(newTestRouter(&stubToolServices{provider: probe}, 1), http.MethodPost, "/api/v1/user/tools/mcp/probe", `{"provider_id":"p-1"}`)

	e := decode(t, w)
	if e.Code != apperrors.CodeBadRequest {
		t.Fatalf("业务码 = %d(%s)，期望 %d（参数错误）", e.Code, e.Message, apperrors.CodeBadRequest)
	}
	if probe.probeCalled != 0 {
		t.Fatal("参数已被拦截，但服务层仍被调用")
	}
}