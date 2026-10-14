package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/datatypes"
	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
)

func settingsFixture() map[string]interface{} {
	return map[string]interface{}{"max_input_tokens": 4096, "max_output_tokens": 1024, "max_completion_tokens": 128, "output_token_field": "max_tokens", "tool_calling": true, "reasoning_capability": "none", "reasoning_mode": "default"}
}

func TestSharedModelCapabilityProbe(t *testing.T) {
	for _, scenario := range []string{"passed", "wrong_arguments", "wrong_answer", "connection_failed", "metadata_only"} {
		t.Run(scenario, func(t *testing.T) {
			completionCalls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/models") {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":[{"id":"test","context_window":8192,"max_input_tokens":4096,"max_output_tokens":1024,"max_tokens":999999}]}`))
					return
				}
				completionCalls++
				var payload struct {
					Stream   bool  `json:"stream"`
					Tools    []any `json:"tools"`
					Messages []struct {
						Role string `json:"role"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if scenario == "connection_failed" {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error":{"message":"secret-api-key"}}`))
					return
				}
				if payload.Stream {
					w.Header().Set("Content-Type", "text/event-stream")
					delta := map[string]any{"role": "assistant", "content": "连接成功"}
					if len(payload.Tools) > 0 {
						value := "capability-ok"
						if scenario == "wrong_arguments" {
							value = "bad"
						}
						args, _ := json.Marshal(map[string]string{"value": value})
						delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call-1", "type": "function", "function": map[string]any{"name": "model_capability_echo", "arguments": string(args)}}}}
					}
					chunk, _ := json.Marshal(map[string]any{"id": "test", "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": delta}}})
					_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
					return
				}
				content := "capability-ok"
				if scenario == "wrong_answer" {
					content = "无关回答"
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
			}))
			defer server.Close()
			req := requestdto.TestModelRequest{Provider: "openai", ModelID: "test", BaseURL: server.URL, APIKey: "synthetic", MaxContextLength: 8192, Config: settingsFixture()}
			if scenario == "metadata_only" {
				req.Config = nil
				req.MaxContextLength = 0
			}
			result, err := testModelSettings(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if result.DetectedMaxContextLength != 8192 || result.DetectedMaxInputTokens != 4096 || result.DetectedMaxOutputTokens != 1024 {
				t.Fatalf("元数据混淆: %+v", result)
			}
			if result.Success != (scenario == "passed") {
				t.Fatalf("能力验证错误: %+v", result)
			}
			if scenario == "metadata_only" && completionCalls != 0 {
				t.Fatal("未确认能力时调用模型")
			}
			if strings.Contains(result.Message+result.Details, "secret-api-key") {
				t.Fatal("泄露上游错误")
			}
			if scenario == "wrong_arguments" || scenario == "wrong_answer" {
				if result.ToolCallingStatus != "failed" {
					t.Fatal(result)
				}
			}
		})
	}
}

func TestModelConfigValidationSharedByBothEntrypoints(t *testing.T) {
	if err := validateModelSettings(8192, settingsFixture()); err != nil {
		t.Fatal(err)
	}
	if err := validateModelSettings(0, settingsFixture()); err == nil {
		t.Fatal("窗口被默认填入")
	}
	if err := validateModelSettings(8192, nil); err == nil {
		t.Fatal("旧配置被默认为可用")
	}
}

type capabilityModelRepo struct {
	repository.ModelRepo
	stored  *entity.Model
	updates int
}

func (r *capabilityModelRepo) GetByID(context.Context, string) (*entity.Model, error) {
	return r.stored, nil
}
func (r *capabilityModelRepo) Update(_ context.Context, m *entity.Model) error {
	r.stored = m
	r.updates++
	return nil
}

type capabilityUserModelRepo struct {
	repository.UserModelConfigRepo
	stored  *entity.UserModelConfig
	updates int
}

func (r *capabilityUserModelRepo) GetByID(context.Context, string, string) (*entity.UserModelConfig, error) {
	return r.stored, nil
}
func (r *capabilityUserModelRepo) Update(_ context.Context, m *entity.UserModelConfig) error {
	r.stored = m
	r.updates++
	return nil
}

func TestModelUpdatesRejectInvalidCandidateWithoutChangingRecord(t *testing.T) {
	srv := admissionServer(t, "passed")
	defer srv.Close()
	raw, _ := json.Marshal(settingsFixture())
	zero, window, empty := -1, 8192, ""
	systemRepo := &capabilityModelRepo{stored: &entity.Model{ID: "system", Provider: "openai", ModelID: "test", BaseURL: srv.URL, APIKey: "sk-original-key", MaxContextLength: 8192, Config: datatypes.JSON(raw)}}
	userRepo := &capabilityUserModelRepo{stored: &entity.UserModelConfig{ID: "user-model", APIFormat: "openai", ModelID: "test", BaseURL: srv.URL, APIKey: "sk-original-key", MaxContextLength: 8192, Config: datatypes.JSON(raw)}}
	if err := NewModelService(systemRepo).Update(context.Background(), "system", requestdto.UpdateModelRequest{MaxContextLength: &zero}); err == nil {
		t.Fatal("非法系统配置保存")
	}
	if _, err := NewUserModelConfigService(userRepo).Update(context.Background(), "u", "user-model", requestdto.UpdateUserModelConfigRequest{MaxContextLength: &zero}); err == nil {
		t.Fatal("非法用户配置保存")
	}
	if systemRepo.stored.MaxContextLength != 8192 || userRepo.stored.MaxContextLength != 8192 || systemRepo.updates != 0 || userRepo.updates != 0 {
		t.Fatal("校验失败污染了记录")
	}
	if err := NewModelService(systemRepo).Update(context.Background(), "system", requestdto.UpdateModelRequest{MaxContextLength: &window, APIKey: &empty}); err != nil {
		t.Fatal(err)
	}
	result, err := NewUserModelConfigService(userRepo).Update(context.Background(), "u", "user-model", requestdto.UpdateUserModelConfigRequest{MaxContextLength: &window, APIKey: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if systemRepo.stored.APIKey != "sk-original-key" || userRepo.stored.APIKey != "sk-original-key" {
		t.Fatal("编辑留空清掉了凭据")
	}
	if result.APIKey == "sk-original-key" {
		t.Fatal("配置响应泄露密钥")
	}
}
