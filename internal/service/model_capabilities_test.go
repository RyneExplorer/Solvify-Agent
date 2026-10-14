package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/entity"
)

func TestOfficialCapabilitiesBoundToDeployment(t *testing.T) {
	for _, base := range []string{"https://proxy.example/v1", "https://api.deepseek.com.evil/v1", "https://api.deepseek.com/other", "http://api.deepseek.com", "https://api.deepseek.com:8443", "https://api.deepseek.com/?route=proxy"} {
		if got := officialModelCapabilities(base, "deepseek-flash"); got.DetectedMaxContextLength != 0 {
			t.Fatalf("套用了原厂额度: %s", base)
		}
	}
	r := officialModelCapabilities("https://api.deepseek.com/v1/", "deepseek-flash")
	if r.DetectedMaxContextLength != 1048576 || r.DetectedMaxInputTokens != 0 || r.CapabilityURL == "" {
		t.Fatal(r)
	}
	if r.SuggestedConfig["max_completion_tokens"] != nil {
		t.Fatal("猜测了实际生成额度")
	}
}

func TestCapabilityAcquisitionNeverCallsChat(t *testing.T) {
	for _, body := range []string{`{"data":[{"id":"m","context_window":16384,"max_output_tokens":4096}]}`, `{"data":[{"id":"m","max_tokens":100000}]}`, `{"data":[]}`} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/models" {
					t.Error("获取能力触发聊天")
				}
				_, _ = w.Write([]byte(body))
			}))
			defer s.Close()
			r, err := testModelSettings(context.Background(), requestdto.TestModelRequest{Operation: "capabilities", Provider: "openai", BaseURL: s.URL, ModelID: "m"})
			if err != nil {
				t.Fatal(err)
			}
			want := body == `{"data":[{"id":"m","context_window":16384,"max_output_tokens":4096}]}`
			if r.Success != want || calls != 1 {
				t.Fatal(r, calls)
			}
			if want && (r.CapabilitySource != "provider_metadata" || r.DetectedMaxInputTokens != 0) {
				t.Fatal(r)
			}
		})
	}
}

func TestDraftAndIdentityChangeBothEntrypoints(t *testing.T) {
	if err := validateModelDraftSettings(0, nil); err != nil {
		t.Fatal(err)
	}
	if modelCapabilityStatus(nil, 0) != "pending" {
		t.Fatal("草稿可运行")
	}
	for _, config := range []map[string]interface{}{{"max_input_tokens": -1}, {"max_output_tokens": "large"}, {"unknown": true}, {"timeout": 0}} {
		if validateModelDraftSettings(0, config) == nil {
			t.Fatal("非法草稿被接受", config)
		}
	}
	base := "https://new.example/v1"
	systemRepo := &capabilityModelRepo{stored: &entity.Model{Provider: "openai", ModelID: "m", BaseURL: "https://old.example/v1", MaxContextLength: 8192, Config: toJSONB(settingsFixture())}}
	userRepo := &capabilityUserModelRepo{stored: &entity.UserModelConfig{APIFormat: "openai", ModelID: "m", BaseURL: "https://old.example/v1", MaxContextLength: 8192, Config: toJSONB(settingsFixture())}}
	if err := NewModelService(systemRepo).Update(context.Background(), "m", requestdto.UpdateModelRequest{BaseURL: &base}); err == nil {
		t.Fatal("未确认身份变化被保存")
	}
	if _, err := NewUserModelConfigService(userRepo).Update(context.Background(), "u", "m", requestdto.UpdateUserModelConfigRequest{BaseURL: &base}); err == nil {
		t.Fatal("未确认身份变化被保存")
	}
	if systemRepo.updates != 0 || userRepo.updates != 0 {
		t.Fatal("未确认写入")
	}
	srv := admissionServer(t, "passed")
	defer srv.Close()
	window := 8192
	base = srv.URL
	if err := NewModelService(systemRepo).Update(context.Background(), "m", requestdto.UpdateModelRequest{BaseURL: &base, MaxContextLength: &window, Config: settingsFixture(), ReconfirmCapabilities: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewUserModelConfigService(userRepo).Update(context.Background(), "u", "m", requestdto.UpdateUserModelConfigRequest{BaseURL: &base, MaxContextLength: &window, Config: settingsFixture(), ReconfirmCapabilities: true}); err != nil {
		t.Fatal(err)
	}
	if systemRepo.stored.MaxContextLength != window || userRepo.stored.MaxContextLength != window {
		t.Fatal("重新确认未保留")
	}
}

func TestModelConfigurationRequiresToolsBeforePersistence(t *testing.T) {
	for _, scenario := range []string{"no_tools", "thinking_conflict"} {
		t.Run(scenario, func(t *testing.T) {
			p := settingsFixture()
			if scenario == "no_tools" {
				p["tool_calling"] = false
			} else {
				p["reasoning_capability"], p["reasoning_mode"], p["tools_with_reasoning"] = "optional", "default", false
			}
			// nil仓库保证创建拒绝发生在任何持久化访问前。
			if _, err := NewModelService(nil).Create(context.Background(), requestdto.CreateModelRequest{Provider: "openai", MaxContextLength: 8192, Config: p}); err == nil {
				t.Fatal("创建了不支持工具的系统模型")
			}
			if _, err := NewUserModelConfigService(nil).Create(context.Background(), "u", requestdto.CreateUserModelConfigRequest{APIFormat: "openai", MaxContextLength: 8192, Config: p}); err == nil {
				t.Fatal("创建了不支持工具的用户模型")
			}
			sys := &capabilityModelRepo{stored: &entity.Model{Provider: "openai", MaxContextLength: 8192, Config: toJSONB(settingsFixture())}}
			usr := &capabilityUserModelRepo{stored: &entity.UserModelConfig{APIFormat: "openai", MaxContextLength: 8192, Config: toJSONB(settingsFixture())}}
			if err := NewModelService(sys).Update(context.Background(), "m", requestdto.UpdateModelRequest{Config: p}); err == nil {
				t.Fatal("更新了不支持工具的系统模型")
			}
			if _, err := NewUserModelConfigService(usr).Update(context.Background(), "u", "m", requestdto.UpdateUserModelConfigRequest{Config: p}); err == nil {
				t.Fatal("更新了不支持工具的用户模型")
			}
			if sys.updates != 0 || usr.updates != 0 || modelCapabilityStatus(sys.stored.Config, 8192) != "ready" || modelCapabilityStatus(usr.stored.Config, 8192) != "ready" {
				t.Fatal("拒绝后污染原记录")
			}
		})
	}
	p := settingsFixture()
	p["reasoning_capability"], p["reasoning_mode"], p["reasoning_protocol"], p["tools_with_reasoning"] = "optional", "off", "thinking", false
	if err := validateModelDraftSettings(8192, p); err != nil {
		t.Fatal("关闭思考后工具模型被拒绝", err)
	}
	p["reasoning_mode"] = "default"
	delete(p, "tools_with_reasoning")
	if modelCapabilityStatus(toJSONB(p), 8192) != "pending" {
		t.Fatal("未知的思考工具兼容能力被标成可用或不支持")
	}
}

func TestEditDetailsReturnCredentialsButGeneralResponsesMask(t *testing.T) {
	sys := &capabilityModelRepo{stored: &entity.Model{APIKey: "synthetic-edit-key"}}
	svc := NewModelService(sys)
	general, err := svc.GetByID(context.Background(), "m")
	if err != nil {
		t.Fatal(err)
	}
	edit, err := svc.GetForEdit(context.Background(), "m")
	if err != nil {
		t.Fatal(err)
	}
	if general.APIKey == "synthetic-edit-key" || edit.APIKey != "synthetic-edit-key" {
		t.Fatal("系统读取边界错误")
	}
	usr := &capabilityUserModelRepo{stored: &entity.UserModelConfig{APIKey: "synthetic-user-key"}}
	info, err := NewUserModelConfigService(usr).Get(context.Background(), "owner", "m")
	if err != nil {
		t.Fatal(err)
	}
	if info.APIKey != "synthetic-user-key" || toModelConfigInfo(usr.stored).APIKey == "synthetic-user-key" {
		t.Fatal("本人编辑详情/列表边界错误")
	}
}
