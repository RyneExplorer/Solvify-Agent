package tool

import (
	"context"
	"encoding/json"
	"testing"

	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/service"
	apperrors "solvify-agent/pkg/errors"
)

type timeoutProviderService struct {
	service.ToolProviderService
	raw   json.RawMessage
	err   error
	calls int
}

func (s *timeoutProviderService) Create(_ context.Context, req request.CreateToolProviderRequest) (*response.ToolProviderInfo, error) {
	s.calls++
	s.raw = req.ProviderConfig
	return &response.ToolProviderInfo{ProviderConfig: req.ProviderConfig}, s.err
}
func (s *timeoutProviderService) Update(_ context.Context, _ string, req request.UpdateToolProviderRequest) (*response.ToolProviderInfo, error) {
	s.calls++
	if req.ProviderConfig != nil {
		s.raw = *req.ProviderConfig
	}
	return &response.ToolProviderInfo{ProviderConfig: s.raw}, s.err
}

func TestProviderTimeoutUsesExistingAdminRoutes(t *testing.T) {
	for _, kind := range []string{"mcp"} {
		for _, method := range []string{"POST", "PUT"} {
			pc := `{"timeout":60}`
			if kind == "mcp" {
				pc = `{"mcp":{"connect_timeout":90,"call_timeout":120}}`
			}
			body := `{"tool_type_id":"type","provider_key":"key","name":"test","provider_type":"` + kind + `","provider_config":` + pc + `}`
			path := "/api/v1/admin/tool-types/type/providers"
			if method == "PUT" {
				path += "/provider"
			}
			for _, role := range []int{0, 1, 2} {
				s := &timeoutProviderService{}
				r := newTestRouter(&stubToolServices{provider: s}, role)
				e := decode(t, send(r, method, path, body))
				if role == 2 {
					if e.Code != 0 || s.calls != 1 || string(s.raw) != pc {
						t.Fatalf("现有路由丢失时限: %+v %s", e, s.raw)
					}
				} else if e.Code == 0 || s.calls != 0 {
					t.Fatal("普通用户修改管理员时限")
				}
			}
			s := &timeoutProviderService{err: apperrors.New(apperrors.CodeBadRequest, "非法工具时限")}
			e := decode(t, send(newTestRouter(&stubToolServices{provider: s}, 2), method, path, body))
			if e.Code != apperrors.CodeBadRequest {
				t.Fatal(e)
			}
			s = &timeoutProviderService{}
			e = decode(t, send(newTestRouter(&stubToolServices{provider: s}, 2), method, path, `{`))
			if e.Code != apperrors.CodeBadRequest || s.calls != 0 {
				t.Fatal("非法参数未拦截")
			}
		}
	}
	r := newTestRouter(&stubToolServices{provider: &timeoutProviderService{}}, 2)
	for _, method := range []string{"GET", "PUT"} {
		if w := send(r, method, "/api/v1/admin/tools/timeout-policy", ""); w.Code != 404 {
			t.Fatal("独立时限策略路由未移除")
		}
	}
}
