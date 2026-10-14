package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/entity"
	"strings"
	"testing"
)

func admissionServer(t *testing.T, scenario string, expectedKey ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(expectedKey) > 0 && r.Header.Get("Authorization") != "Bearer "+expectedKey[0] {
			t.Error("未使用候选配置密钥验证")
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/models") {
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		if scenario == "unauthorized" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"message":"upstream-secret"}}`))
			return
		}
		var p struct {
			Stream   bool  `json:"stream"`
			Tools    []any `json:"tools"`
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
			return
		}
		if p.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			delta := map[string]any{"role": "assistant", "content": "ok"}
			if len(p.Tools) > 0 && scenario != "ignores_tools" {
				value := "capability-ok"
				if scenario == "bad_arguments" {
					value = "wrong"
				}
				args, _ := json.Marshal(map[string]string{"value": value})
				delta = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "echo-1", "type": "function", "function": map[string]any{"name": "model_capability_echo", "arguments": string(args)}}}}
			}
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			return
		}
		answer := "capability-ok"
		if scenario == "bad_followup" && p.Messages[len(p.Messages)-1].Role == "tool" {
			answer = "unknown"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": answer}, "finish_reason": "stop"}}})
	}))
}

func (r *capabilityModelRepo) ExistsByModelID(context.Context, string, string) (bool, error) {
	return false, nil
}
func (r *capabilityModelRepo) Create(_ context.Context, m *entity.Model) error {
	r.stored = m
	r.updates++
	return nil
}
func (r *capabilityUserModelRepo) ExistsByModelID(context.Context, string, string, string) (bool, error) {
	return false, nil
}
func (r *capabilityUserModelRepo) Create(_ context.Context, m *entity.UserModelConfig) error {
	r.stored = m
	r.updates++
	return nil
}

func TestSaveVerifiesActualToolsBeforeWriting(t *testing.T) {
	for _, scenario := range []string{"passed", "ignores_tools", "bad_arguments", "bad_followup", "unauthorized", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			srv := admissionServer(t, scenario)
			defer srv.Close()
			for _, operation := range []string{"create", "update"} {
				sys := &capabilityModelRepo{stored: &entity.Model{Provider: "openai", ModelID: "test", BaseURL: srv.URL, APIKey: "synthetic", MaxContextLength: 8192, Config: toJSONB(settingsFixture())}}
				usr := &capabilityUserModelRepo{stored: &entity.UserModelConfig{APIFormat: "openai", ModelID: "test", BaseURL: srv.URL, APIKey: "synthetic", MaxContextLength: 8192, Config: toJSONB(settingsFixture())}}
				oldSystem, oldUser := sys.stored, usr.stored
				ctx := context.Background()
				if scenario == "cancelled" {
					c, cancel := context.WithCancel(ctx)
					cancel()
					ctx = c
				}
				var e1, e2 error
				if operation == "create" {
					_, e1 = NewModelService(sys).Create(ctx, requestdto.CreateModelRequest{Provider: "openai", ModelID: "test", BaseURL: srv.URL, APIKey: "synthetic", MaxContextLength: 8192, Config: settingsFixture()})
					_, e2 = NewUserModelConfigService(usr).Create(ctx, "owner", requestdto.CreateUserModelConfigRequest{APIFormat: "openai", ModelID: "test", BaseURL: srv.URL, APIKey: "synthetic", MaxContextLength: 8192, Config: settingsFixture()})
				} else {
					e1 = NewModelService(sys).Update(ctx, "m", requestdto.UpdateModelRequest{Config: settingsFixture()})
					_, e2 = NewUserModelConfigService(usr).Update(ctx, "owner", "m", requestdto.UpdateUserModelConfigRequest{Config: settingsFixture()})
				}
				if scenario == "passed" {
					if e1 != nil || e2 != nil || sys.updates != 1 || usr.updates != 1 {
						t.Fatal("通过工具测试未保存", e1, e2)
					}
				} else {
					if e1 == nil || e2 == nil || sys.updates != 0 || usr.updates != 0 || sys.stored != oldSystem || usr.stored != oldUser {
						t.Fatal("失败后保存或污染旧记录", scenario, operation, e1, e2)
					}
					if strings.Contains(e1.Error()+e2.Error(), "upstream-secret") {
						t.Fatal("上游错误泄露")
					}
				}
			}
		})
	}
}

func TestSaveUsesCandidateKeyAndAllowsDisablingUnavailableModel(t *testing.T) {
	srv := admissionServer(t, "passed", "candidate-key")
	defer srv.Close()
	key := "candidate-key"
	sys := &capabilityModelRepo{stored: &entity.Model{Provider: "openai", ModelID: "test", BaseURL: srv.URL, APIKey: "old-key", MaxContextLength: 8192, Config: toJSONB(settingsFixture())}}
	usr := &capabilityUserModelRepo{stored: &entity.UserModelConfig{APIFormat: "openai", ModelID: "test", BaseURL: srv.URL, APIKey: "old-key", MaxContextLength: 8192, Config: toJSONB(settingsFixture())}}
	if err := NewModelService(sys).Update(context.Background(), "m", requestdto.UpdateModelRequest{APIKey: &key}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewUserModelConfigService(usr).Update(context.Background(), "u", "m", requestdto.UpdateUserModelConfigRequest{APIKey: &key}); err != nil {
		t.Fatal(err)
	}
	if sys.stored.APIKey != key || usr.stored.APIKey != key {
		t.Fatal("未保存已验证的候选密钥")
	}
	stop := false
	disabled := &capabilityModelRepo{stored: &entity.Model{Provider: "openai", BaseURL: "https://unavailable.invalid", IsEnabled: true, Config: toJSONB(map[string]interface{}{"tool_calling": false})}}
	if err := NewModelService(disabled).Update(context.Background(), "m", requestdto.UpdateModelRequest{IsEnabled: &stop}); err != nil || disabled.stored.IsEnabled {
		t.Fatal("无法停用无效旧配置", err)
	}
	if _, err := NewModelService(nil).Create(context.Background(), requestdto.CreateModelRequest{Provider: "openai"}); err == nil {
		t.Fatal("未知能力保存未拒绝")
	}
}
