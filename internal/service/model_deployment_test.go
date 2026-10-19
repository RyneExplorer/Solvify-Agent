package service

import (
	"context"
	"testing"

	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/entity"
	apperrors "solvify-agent/pkg/errors"
)

type deploymentProfiles []entity.Model

func (p deploymentProfiles) List(context.Context) ([]entity.Model, error) { return p, nil }

func TestUserDeploymentRejectsSpoofedCapabilities(t *testing.T) {
	srv := admissionServer(t, "passed")
	defer srv.Close()
	repo := &capabilityUserModelRepo{}
	service := NewUserModelConfigService(repo, nil)
	_, err := service.Create(context.Background(), "owner", requestdto.CreateUserModelConfigRequest{APIFormat: "openai", ModelID: "test", BaseURL: srv.URL, MaxContextLength: 8192, Config: map[string]interface{}{"max_output_tokens": 999999, "reasoning_capability": "optional", "reasoning_protocol": "thinking", "reasoning_mode": "on"}})
	if err == nil || repo.updates != 0 {
		t.Fatal("用户伪造思考能力后保存")
	}
	_, err = service.Create(context.Background(), "owner", requestdto.CreateUserModelConfigRequest{APIFormat: "openai", ModelID: "test", BaseURL: srv.URL, MaxContextLength: 8192, Config: map[string]interface{}{"max_output_tokens": 999999, "reasoning_capability": "none"}})
	if err != nil {
		t.Fatal(err)
	}
	p := decodeSettings(repo.stored.Config)
	if p["max_output_tokens"] != float64(1024) || p["reasoning_capability"] != "unknown" {
		t.Fatal("客户端能力覆盖了服务商资料", p)
	}
}

type ownerDeploymentRepo struct {
	capabilityUserModelRepo
	queriedUser string
}

func (r *ownerDeploymentRepo) GetByID(_ context.Context, _ string, userID string) (*entity.UserModelConfig, error) {
	r.queriedUser = userID
	if userID != "owner" {
		return nil, apperrors.NewDefault(apperrors.CodeModelConfigNotFound)
	}
	return r.stored, nil
}

func TestUserModelProbeUsesOwnedDeployment(t *testing.T) {
	srv := admissionServer(t, "passed")
	defer srv.Close()
	repo := &ownerDeploymentRepo{capabilityUserModelRepo: capabilityUserModelRepo{stored: &entity.UserModelConfig{APIFormat: "openai", ModelID: "test", BaseURL: srv.URL, MaxContextLength: 8192, Config: toJSONB(settingsFixture())}}}
	svc := NewUserModelConfigService(repo, nil)
	req := requestdto.TestModelRequest{ConfigID: "saved", Provider: "openai", ModelID: "test", BaseURL: srv.URL, MaxContextLength: 8192, Config: map[string]interface{}{"reasoning_mode": "default"}}
	if _, err := svc.Test(context.Background(), "another-user", req); err == nil || repo.queriedUser != "another-user" {
		t.Fatal("未验证模型配置归属")
	}
	result, err := svc.Test(context.Background(), "owner", req)
	if err != nil || !result.Success {
		t.Fatal("本人配置测试失败", result, err)
	}
	req.Operation = "capabilities"
	result, err = svc.Test(context.Background(), "owner", req)
	if err != nil || !result.Success || result.SuggestedConfig["reasoning_capability"] != "none" {
		t.Fatal("本人能力资料读取失败", err)
	}
}

func TestThinkingModesAndActualToolCompatibility(t *testing.T) {
	for _, tc := range []struct {
		capability, protocol, mode string
		pass                       bool
	}{
		{"none", "", "default", true}, {"none", "", "on", false},
		{"always", "", "default", true}, {"always", "", "off", false},
		{"unknown", "", "default", true}, {"unknown", "", "off", false},
		{"optional", "", "on", false},
		{"optional", "thinking", "on", false}, {"optional", "thinking", "off", true},
	} {
		t.Run(tc.capability+"_"+tc.protocol+"_"+tc.mode, func(t *testing.T) {
			srv := admissionServer(t, "thinking_conflict")
			defer srv.Close()
			config := settingsFixture()
			config["reasoning_capability"], config["reasoning_protocol"], config["reasoning_mode"], config["tools_with_reasoning"] = tc.capability, tc.protocol, "default", true
			profiles := deploymentProfiles{{IsEnabled: true, ModelID: "test", BaseURL: srv.URL, MaxContextLength: 8192, Config: toJSONB(config)}}
			repo := &capabilityUserModelRepo{}
			_, err := NewUserModelConfigService(repo, profiles).Create(context.Background(), "owner", requestdto.CreateUserModelConfigRequest{APIFormat: "openai", ModelID: "test", BaseURL: srv.URL, MaxContextLength: 8192, Config: map[string]interface{}{"reasoning_mode": tc.mode}})
			if tc.pass && (err != nil || repo.updates != 1) {
				t.Fatal("合法模式未保存", err)
			}
			if !tc.pass && (err == nil || repo.updates != 0) {
				t.Fatal("非法或工具冲突模式保存")
			}
		})
	}
}

func TestDeploymentProfileExactMatchAndMissingLimits(t *testing.T) {
	srv := admissionServer(t, "passed")
	defer srv.Close()
	profiles := deploymentProfiles{{IsEnabled: true, ModelID: "other", BaseURL: srv.URL, Config: toJSONB(settingsFixture())}}
	settings, err := userDeploymentSettings(context.Background(), requestdto.TestModelRequest{Provider: "openai", ModelID: "test", BaseURL: srv.URL}, nil, profiles)
	if err != nil || settings["reasoning_capability"] != "unknown" {
		t.Fatal("同地址其他模型资料被使用", err)
	}
	_, err = userDeploymentSettings(context.Background(), requestdto.TestModelRequest{Provider: "openai", ModelID: "missing", BaseURL: srv.URL, Config: map[string]interface{}{"max_output_tokens": 4096}}, nil, profiles)
	if err == nil {
		t.Fatal("缺少部署资料时采用客户端额度")
	}
}
