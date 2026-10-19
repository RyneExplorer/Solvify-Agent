package service

import (
	"context"
	"strings"

	"solvify-agent/internal/llm"
	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/entity"
	apperrors "solvify-agent/pkg/errors"
)

type modelDeploymentReader interface {
	List(context.Context) ([]entity.Model, error)
}

func hasDeploymentOutput(settings map[string]interface{}) bool {
	p, err := llm.ParseModelDraft(toJSONB(managedModelSettings(settings)), 0)
	return err == nil && p.MaxOutputTokens > 0
}

// 普通用户只提供模式；不把其传入的额度、协议或能力声明作为部署资料。
func userDeploymentSettings(ctx context.Context, req requestdto.TestModelRequest, existing map[string]interface{}, profiles modelDeploymentReader) (map[string]interface{}, error) {
	settings := make(map[string]interface{}, len(existing))
	for key, value := range existing {
		settings[key] = value
	}
	if req.Config["tool_calling"] == false {
		return nil, apperrors.New(apperrors.CodeInvalidParam, "项目模型必须支持工具调用，请更换模型")
	}
	if !hasDeploymentOutput(settings) && profiles != nil {
		models, err := profiles.List(ctx)
		if err != nil {
			return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
		}
		for _, m := range models {
			if m.IsEnabled && m.ModelID == req.ModelID && strings.TrimRight(m.BaseURL, "/") == strings.TrimRight(req.BaseURL, "/") {
				settings = decodeSettings(m.Config)
				break
			}
		}
	}
	if !hasDeploymentOutput(settings) {
		result, err := acquireModelCapabilities(ctx, req)
		if err != nil {
			return nil, err
		}
		if !result.Success || result.DetectedMaxOutputTokens <= 0 {
			return nil, apperrors.New(apperrors.CodeInvalidParam, "当前部署缺少输出能力资料，请先获取能力或联系管理员配置该部署")
		}
		settings = result.SuggestedConfig
		settings["max_output_tokens"] = result.DetectedMaxOutputTokens
		if result.DetectedMaxInputTokens > 0 {
			settings["max_input_tokens"] = result.DetectedMaxInputTokens
		}
		settings["capability_origin"] = map[string]interface{}{"source": result.CapabilitySource, "url": result.CapabilityURL, "checked_at": result.CapabilityCheckedAt}
	}
	// 未选择时保留旧模式；新部署使用服务商默认行为。
	if mode, ok := req.Config["reasoning_mode"]; ok {
		settings["reasoning_mode"] = mode
	} else if mode, ok := existing["reasoning_mode"]; ok {
		settings["reasoning_mode"] = mode
	}
	return managedModelSettings(settings), nil
}
