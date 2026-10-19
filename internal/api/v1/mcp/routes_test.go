package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"solvify-agent/internal/middleware"
	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/service"
	apperrors "solvify-agent/pkg/errors"
)

type routeService struct {
	service.MCPService
	owner string
	calls int
	err   error
}

func (s *routeService) Create(_ context.Context, userID string, _ request.CreateMCPConnectionRequest) (*response.MCPConnectionInfo, error) {
	s.owner = userID
	s.calls++
	return &response.MCPConnectionInfo{ID: "connection"}, s.err
}
func (s *routeService) Probe(_ context.Context, userID string, _ request.ProbeMCPConnectionRequest) (*response.TestResult, error) {
	s.owner = userID
	s.calls++
	return &response.TestResult{Success: true}, s.err
}

func (s *routeService) Tools(_ context.Context, userID, id string) (*response.MCPConnectionTools, error) {
	s.owner = userID
	s.calls++
	return &response.MCPConnectionTools{Tools: []response.MCPConnectionTool{{Name: "search", IsEnabled: true}}}, s.err
}
func (s *routeService) UpdateTool(_ context.Context, userID, id string, req request.UpdateMCPToolRequest) (*response.MCPToolState, error) {
	s.owner = userID
	s.calls++
	return &response.MCPToolState{Name: req.Name, IsEnabled: *req.IsEnabled}, s.err
}

func TestMCPToolRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		method, path, body  string
		business            error
		code, status, calls int
	}{
		{http.MethodGet, "/connection/tools", "", nil, 0, 200, 1},
		{http.MethodPut, "/connection/tools", `{"name":"search","is_enabled":false,"user_id":"attacker"}`, nil, 0, 200, 1},
		{http.MethodPut, "/connection/tools", `{"name":"search"}`, nil, apperrors.CodeBadRequest, 200, 0},
		{http.MethodPut, "/connection/tools", `{"name":"search","is_enabled":"false"}`, nil, apperrors.CodeBadRequest, 200, 0},
		{http.MethodPut, "/connection/tools", `{`, nil, apperrors.CodeBadRequest, 200, 0},
		{http.MethodGet, "/connection/tools", "", apperrors.New(apperrors.CodeBadRequest, "连接不可用"), apperrors.CodeBadRequest, 200, 1},
		{http.MethodPut, "/connection/tools", `{"name":"search","is_enabled":true}`, apperrors.New(apperrors.CodeBadRequest, "不属于当前用户"), apperrors.CodeBadRequest, 200, 1},
		{http.MethodPost, "/connection/tools/search/execute", `{}`, nil, 0, 404, 0},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			s := &routeService{err: tc.business}
			r := gin.New()
			group := r.Group("/api/v1")
			group.Use(func(c *gin.Context) { c.Set(middleware.ContextUserID, "f4d214b3-1720-48c1-ad67-9e048ef10ca2") })
			NewController(s).RegisterRoutes(group)
			req := httptest.NewRequest(tc.method, "/api/v1/user/mcp-connections"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.status || s.calls != tc.calls {
				t.Fatalf("status=%d calls=%d body=%s", w.Code, s.calls, w.Body.String())
			}
			if tc.status == 200 {
				var envelope struct {
					Code int `json:"code"`
				}
				if json.Unmarshal(w.Body.Bytes(), &envelope) != nil || envelope.Code != tc.code {
					t.Fatalf("响应错误：%s", w.Body.String())
				}
			}
			if s.calls > 0 && s.owner != "f4d214b3-1720-48c1-ad67-9e048ef10ca2" {
				t.Fatal("未使用可信身份")
			}
		})
	}
}
func TestMCPRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, path, body string
		err              error
		want             int
		calls            int
	}{
		{"创建个人连接", "", "{\"display_name\":\"搜索\",\"connection\":{\"transport\":\"http\",\"url\":\"https://example.com/mcp\"},\"user_id\":\"attacker\"}", nil, 0, 1},
		{"超大请求", "", `{"display_name":"搜索","connection":{"url":"` + strings.Repeat("x", 256<<10) + `"}}`, nil, apperrors.CodeBadRequest, 0},
		{"坏JSON", "", "{", nil, apperrors.CodeBadRequest, 0},
		{"缺名称", "", "{}", nil, apperrors.CodeBadRequest, 0},
		{"业务错误", "", "{\"display_name\":\"搜索\"}", apperrors.New(apperrors.CodeBadRequest, "MCP预置不可用"), apperrors.CodeBadRequest, 1},
		{"已保存配置探测", "/probe", `{"id":"f8e939d4-6326-4e63-9113-734308a09921"}`, nil, 0, 1},
		{"坏配置ID", "/probe", `{"id":"invalid"}`, nil, apperrors.CodeBadRequest, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &routeService{err: tc.err}
			r := gin.New()
			group := r.Group("/api/v1")
			group.Use(func(c *gin.Context) {
				c.Set(middleware.ContextUserID, "f4d214b3-1720-48c1-ad67-9e048ef10ca2")
				c.Next()
			})
			NewController(s).RegisterRoutes(group)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/user/mcp-connections"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			var envelope struct {
				Code int `json:"code"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Code != tc.want || s.calls != tc.calls {
				t.Fatalf("code=%d calls=%d body=%s", envelope.Code, s.calls, w.Body.String())
			}
			if s.calls > 0 && s.owner != "f4d214b3-1720-48c1-ad67-9e048ef10ca2" {
				t.Fatal("接受了请求中的伪造身份")
			}
		})
	}
}
