package mcp

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"solvify-agent/internal/middleware"
	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/service"
	apperrors "solvify-agent/pkg/errors"
	"strings"
	"testing"
)

type templateRouteService struct {
	service.MCPTemplateService
	calls int
	err   error
}

func (s *templateRouteService) Create(context.Context, request.CreateMCPTemplateRequest) (*response.MCPTemplateAdminInfo, error) {
	s.calls++
	return &response.MCPTemplateAdminInfo{ID: "template"}, s.err
}
func (s *templateRouteService) Update(context.Context, string, request.UpdateMCPTemplateRequest) (*response.MCPTemplateAdminInfo, error) {
	s.calls++
	return &response.MCPTemplateAdminInfo{ID: "template"}, s.err
}
func (s *templateRouteService) Probe(context.Context, request.ProbeMCPTemplateRequest) (*response.TestResult, error) {
	s.calls++
	return &response.TestResult{Success: true}, s.err
}
func TestMCPTemplateRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, path, body, method string
		role, calls              int
		err                      error
		success                  bool
	}{
		{"创建模板", "", `{"key":"search","name":"搜索","connection":{"transport":"http","url":"https://example.invalid/mcp"}}`, http.MethodPost, 2, 1, nil, true},
		{"坏JSON", "", `{`, http.MethodPost, 2, 0, nil, false},
		{"缺少标识", "", `{"name":"搜索","connection":{}}`, http.MethodPost, 2, 0, nil, false},
		{"非法超时类型", "/template", `{"connection":{"call_timeout":"bad"}}`, http.MethodPut, 2, 1, apperrors.New(apperrors.CodeBadRequest, "MCP配置无效"), false},
		{"业务错误", "/template", `{"is_enabled":false}`, http.MethodPut, 2, 1, apperrors.New(apperrors.CodeBadRequest, "预置不可用"), false},
		{"超时更新", "/template", `{"connection":{"connect_timeout":90,"call_timeout":120}}`, http.MethodPut, 2, 1, nil, true},
		{"发现工具", "/probe", `{"connection":{"transport":"http","url":"https://example.invalid/mcp"}}`, http.MethodPost, 2, 1, nil, true},
		{"普通用户禁止管理", "", `{"key":"search","name":"搜索","connection":{}}`, http.MethodPost, 1, 0, nil, false},
		{"超大请求", "/template", `{"connection":{"headers":{"X":"` + strings.Repeat("x", 256<<10) + `"}}}`, http.MethodPut, 2, 0, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &templateRouteService{err: tc.err}
			r := gin.New()
			g := r.Group("/api/v1")
			g.Use(func(c *gin.Context) { c.Set(middleware.ContextUserRole, tc.role); c.Next() })
			NewTemplateController(s).RegisterRoutes(g)
			req := httptest.NewRequest(tc.method, "/api/v1/admin/mcp-templates"+tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			var out struct {
				Code int `json:"code"`
			}
			if json.Unmarshal(w.Body.Bytes(), &out) != nil {
				t.Fatal(w.Body.String())
			}
			if (out.Code == 0) != tc.success || s.calls != tc.calls {
				t.Fatalf("code=%d calls=%d", out.Code, s.calls)
			}
		})
	}
}
func TestRemovedToolManagementRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/v1")
	NewTemplateController(nil).RegisterRoutes(g)
	NewController(nil).RegisterRoutes(g)
	for _, path := range []string{"/admin/tool-types", "/admin/provider-types", "/admin/mcp-servers", "/admin/tool-types/type/providers", "/admin/tools/test", "/admin/tools/test/probe", "/user/tool-configs", "/user/tools/templates"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, "/api/v1"+path, nil))
			if w.Code != http.StatusNotFound {
				t.Fatalf("废弃路由仍存在：%s %s", method, path)
			}
		}
	}
}
