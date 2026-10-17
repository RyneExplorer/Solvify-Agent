package service

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"solvify-agent/internal/llm"
	requestdto "solvify-agent/internal/model/dto/request"
	responsedto "solvify-agent/internal/model/dto/response"
	apperrors "solvify-agent/pkg/errors"
)

// 官方目录只匹配已经核实的部署；同名代理不会继承原厂额度。
func officialModelCapabilities(baseURL, modelID string) responsedto.TestResult {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return responsedto.TestResult{}
	}
	path := strings.TrimRight(u.Path, "/")
	r := responsedto.TestResult{CapabilitySource: "official_catalog", CapabilityCheckedAt: "2026-10-13", SuggestedConfig: map[string]interface{}{
		"tool_calling": true, "reasoning_capability": "optional", "tools_with_reasoning": true, "reasoning_protocol": "thinking", "output_token_field": "max_tokens",
	}}
	switch {
	case u.Host == "api.deepseek.com" && (path == "" || path == "/v1") && (modelID == "deepseek-flash" || modelID == "deepseek-v4-pro"):
		r.DetectedMaxContextLength, r.DetectedMaxOutputTokens = 1048576, 393216
		r.CapabilityURL = "https://api-docs.deepseek.com/quick_start/pricing"
	case u.Host == "open.bigmodel.cn" && path == "/api/paas/v4" && modelID == "glm-4.7-flash":
		r.DetectedMaxContextLength, r.DetectedMaxOutputTokens = 200000, 128000
		r.CapabilityURL = "https://docs.bigmodel.cn/cn/guide/models/free/glm-4.7-flash"
	default:
		return responsedto.TestResult{}
	}
	return r
}

// 只读获取，不发送聊天请求，不测试/推测最大窗口，不保存配置。
func acquireModelCapabilities(ctx context.Context, req requestdto.TestModelRequest) (responsedto.TestResult, error) {
	if !llm.SupportsProvider(req.Provider) {
		return responsedto.TestResult{}, apperrors.New(apperrors.CodeInvalidParam, "仅支持已实现的OpenAI兼容协议")
	}
	started := time.Now()
	r := officialModelCapabilities(req.BaseURL, req.ModelID)
	metadataCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	m, err := llm.DetectModelMetadata(metadataCtx, req.BaseURL, req.APIKey, req.ModelID)
	if err == nil && (m.ContextWindow > 0 || m.MaxInputTokens > 0 || m.MaxOutputTokens > 0) {
		if validateModelDraftSettings(m.ContextWindow, map[string]interface{}{"max_input_tokens": m.MaxInputTokens, "max_output_tokens": m.MaxOutputTokens}) != nil {
			return responsedto.TestResult{Message: "服务商能力元数据不一致，请查阅部署资料后手动确认"}, nil
		}
		// 元数据优先，不混合两种来源的额度。
		suggestions := r.SuggestedConfig
		r = responsedto.TestResult{CapabilitySource: "provider_metadata", CapabilityCheckedAt: time.Now().UTC().Format(time.RFC3339), SuggestedConfig: suggestions,
			DetectedMaxContextLength: m.ContextWindow, DetectedMaxInputTokens: m.MaxInputTokens, DetectedMaxOutputTokens: m.MaxOutputTokens}
		if r.SuggestedConfig == nil {
			r.SuggestedConfig = map[string]interface{}{}
		}
		if m.ReasoningCapability != "" {
			r.SuggestedConfig["reasoning_capability"] = m.ReasoningCapability
		}
		if m.ReasoningProtocol != "" {
			r.SuggestedConfig["reasoning_protocol"] = m.ReasoningProtocol
		}
		if m.OutputTokenField != "" {
			r.SuggestedConfig["output_token_field"] = m.OutputTokenField
		}
		if m.ToolCalling != nil {
			r.SuggestedConfig["tool_calling"] = *m.ToolCalling
		}
		if m.ToolsWithReasoning != nil {
			r.SuggestedConfig["tools_with_reasoning"] = *m.ToolsWithReasoning
		}
	}
	if ctx.Err() != nil {
		return responsedto.TestResult{}, ctx.Err()
	}
	r.Success = r.DetectedMaxContextLength > 0 || r.DetectedMaxInputTokens > 0 || r.DetectedMaxOutputTokens > 0
	r.Message = "已获取能力资料，请确认部署额度；实际输出由系统管理，保存前自动验证工具调用"
	if !r.Success {
		r.Message = "未获取到明确能力，请查阅当前部署资料后手动填写；补齐能力后保存时将自动验证工具调用"
	}
	r.ResponseTime = time.Since(started).Milliseconds()
	if r.Success {
		if r.SuggestedConfig == nil {
			r.SuggestedConfig = map[string]interface{}{}
		}
		if _, ok := r.SuggestedConfig["reasoning_capability"]; !ok {
			r.SuggestedConfig["reasoning_capability"] = "unknown"
		}
		raw, err := json.Marshal(r.SuggestedConfig)
		if err != nil {
			return responsedto.TestResult{}, apperrors.New(apperrors.CodeInvalidParam, "服务商能力资料格式无效")
		}
		if _, err := llm.ParseModelDraft(raw, 0); err != nil {
			return responsedto.TestResult{Message: "服务商思考或工具能力资料无效，请由管理员核实部署配置"}, nil
		}
	}
	return r, nil
}
