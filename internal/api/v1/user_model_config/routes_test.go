package user_model_config

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

type testModelService struct {
	service.UserModelConfigServiceInterface
	userID string
	calls  int
	err    error
}

func (s *testModelService) Test(_ context.Context, userID string, req requestdto.TestModelRequest) (responsedto.TestResult, error) {
	s.calls++
	s.userID = userID
	return responsedto.TestResult{Success: true}, s.err
}

func TestUserModelTestRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const owner = "d0ec2864-778f-4451-ad16-d1b50b54e67a"
	for _, tc := range []struct {
		name, path, body             string
		authenticated, businessError bool
		wantStatus, wantCalls        int
	}{
		{"normal", "/api/v1/user/model-configs/test", `{"provider":"openai","model_id":"m","base_url":"https://example.invalid/v1","config_id":"d0ec2864-778f-4451-ad16-d1b50b54e67a"}`, true, false, 200, 1},
		{"bad_id", "/api/v1/user/model-configs/test", `{"provider":"openai","model_id":"m","base_url":"https://example.invalid","config_id":"bad"}`, true, false, 200, 0},
		{"bad_json", "/api/v1/user/model-configs/test", `{`, true, false, 200, 0},
		{"unauthenticated", "/api/v1/user/model-configs/test", `{}`, false, false, 200, 0},
		{"business_error", "/api/v1/user/model-configs/test", `{"provider":"openai","model_id":"m","base_url":"https://example.invalid"}`, true, true, 200, 1},
		{"wrong_path", "/api/v1/user/model-config/test", `{}`, true, false, 404, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &testModelService{}
			if tc.businessError {
				svc.err = apperrors.New(apperrors.CodeInvalidParam, "当前部署缺少能力资料")
			}
			r := gin.New()
			v1 := r.Group("/api/v1")
			if tc.authenticated {
				v1.Use(func(c *gin.Context) { c.Set(middleware.ContextUserID, owner); c.Next() })
			}
			NewController(svc).RegisterRoutes(v1)
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.wantStatus || svc.calls != tc.wantCalls {
				t.Fatal(w.Code, svc.calls, w.Body.String())
			}
			if svc.calls > 0 && svc.userID != owner {
				t.Fatal("未传可信用户身份")
			}
			if tc.wantStatus == 200 {
				var body struct {
					Code int `json:"code"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if tc.name == "normal" {
					if body.Code != 0 {
						t.Fatal(w.Body.String())
					}
				} else if body.Code == 0 {
					t.Fatal("错误响应无业务码")
				}
			}
		})
	}
}
