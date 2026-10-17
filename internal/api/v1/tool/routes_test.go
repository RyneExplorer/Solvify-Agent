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
)

// 旧用户工具配置和探测路由必须移除，MCP用户入口由独立模块提供。

const testUserID = "d0ec2864-778f-4451-ad16-d1b50b54e67a"

func init() { gin.SetMode(gin.TestMode) }

type stubToolServices struct {
	provider service.ToolProviderService
	probe    *stubProviderService
}

type stubTypeService struct{ service.ToolTypeService }

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
	NewController(&stubTypeService{}, svc.provider).RegisterRoutes(v1)
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

func TestToolRoutes_RemovedUserToolRoutes(t *testing.T) {
	r := newTestRouter(&stubToolServices{provider: &stubProviderService{}}, 1)
	for _, path := range []string{"/api/v1/user/tools/templates", "/api/v1/user/tool-configs", "/api/v1/user/tools/mcp/probe", "/api/v1/user/tool-configs/test"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if w := send(r, method, path, `{}`); w.Code != 404 {
				t.Fatalf("废弃路由仍存在: %s %s", method, path)
			}
		}
	}
}
