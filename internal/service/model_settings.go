package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"solvify-agent/internal/llm"
	requestdto "solvify-agent/internal/model/dto/request"
	responsedto "solvify-agent/internal/model/dto/response"
	apperrors "solvify-agent/pkg/errors"
)

func validateModelSettings(window int, settings map[string]interface{}) error {
	raw, err := json.Marshal(settings)
	if err != nil {
		return apperrors.New(apperrors.CodeInvalidParam, "模型配置格式无效")
	}
	p, err := llm.ParseModelOptions(raw, window)
	if err != nil {
		return apperrors.New(apperrors.CodeInvalidParam, err.Error())
	}
	if err := p.CheckTools(); err != nil {
		return apperrors.New(apperrors.CodeInvalidParam, err.Error())
	}
	return nil
}

// 保存使用当前候选配置实测，不接受前端传入的测试成功标记。
func verifyModelForSave(ctx context.Context, req requestdto.TestModelRequest) error {
	if err := validateModelSettings(req.MaxContextLength, req.Config); err != nil {
		return err
	}
	result, err := testModelSettings(ctx, req)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if !result.Success || result.ToolCallingStatus != "passed" {
		message := "模型工具调用验证未通过，配置未保存；请更换工具模型或检查当前部署"
		if result.ConnectionStatus != "passed" {
			message = "模型连接验证未完成，配置未保存；请检查地址、密钥、网络与配额后重试"
		} else if result.StreamingStatus != "passed" {
			message = "模型流式验证未通过，配置未保存；请检查当前部署后重试"
		} else if req.Config["reasoning_mode"] == "on" {
			message = "当前思考模式的工具调用验证未通过，配置未保存；可关闭思考后重新验证"
		}
		return apperrors.New(apperrors.CodeInvalidParam, message)
	}
	return nil
}

func validateModelDraftSettings(window int, settings map[string]interface{}) error {
	raw, err := json.Marshal(settings)
	if err != nil {
		return apperrors.New(apperrors.CodeInvalidParam, "模型配置格式无效")
	}
	return validateModelDraft(window, raw)
}

func validateModelDraft(window int, raw []byte) error {
	p, err := llm.ParseModelDraft(raw, window)
	if err != nil {
		return apperrors.New(apperrors.CodeInvalidParam, err.Error())
	}
	if p.ToolCalling != nil && !*p.ToolCalling {
		return apperrors.New(apperrors.CodeInvalidParam, "项目模型必须支持工具调用，请更换模型")
	}
	if p.ReasoningCapability != "" && p.ReasoningCapability != "none" && p.ReasoningMode != "off" && p.ToolsWithReasoning != nil && !*p.ToolsWithReasoning {
		return apperrors.New(apperrors.CodeInvalidParam, "当前思考设置不支持工具调用，请关闭思考或更换模型")
	}
	return nil
}

func decodeModelConfig(raw []byte) any {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}

// 两种配置入口共享同一测试；仅使用合成echo工具，不执行用户工具。
func testModelSettings(ctx context.Context, req requestdto.TestModelRequest) (responsedto.TestResult, error) {
	if req.Operation == "capabilities" {
		return acquireModelCapabilities(ctx, req)
	}
	if req.Operation != "" && req.Operation != "test" {
		return responsedto.TestResult{}, apperrors.New(apperrors.CodeInvalidParam, "不支持的模型操作")
	}
	settings, prepareErr := prepareAdminModelSettings(ctx, req)
	if prepareErr != nil {
		return responsedto.TestResult{}, prepareErr
	}
	req.Config = settings
	started := time.Now()
	result := responsedto.TestResult{ConnectionStatus: "unknown", ToolCallingStatus: "unknown", StreamingStatus: "unknown"}
	if !llm.SupportsProvider(req.Provider) {
		return result, apperrors.New(apperrors.CodeInvalidParam, "仅支持已实现的OpenAI兼容协议")
	}
	testCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	metadataCtx, metadataCancel := context.WithTimeout(testCtx, 5*time.Second)
	metadata, metadataErr := llm.DetectModelMetadata(metadataCtx, req.BaseURL, req.APIKey, req.ModelID)
	metadataCancel()
	if metadataErr == nil {
		result.DetectedMaxContextLength = metadata.ContextWindow
		result.DetectedMaxInputTokens = metadata.MaxInputTokens
		result.DetectedMaxOutputTokens = metadata.MaxOutputTokens
		result.CapabilitySource = "provider_metadata"
	}
	raw, _ := json.Marshal(req.Config)
	options, err := llm.ParseModelOptions(raw, req.MaxContextLength)
	if err != nil {
		result.Message = "请先确认输入/输出与能力配置；服务商返回值仅作为参考"
		result.ResponseTime = time.Since(started).Milliseconds()
		return result, nil
	}
	client, err := llm.NewOpenAIClientDirect(testCtx, llm.OpenAIClientConfig{APIKey: req.APIKey, BaseURL: req.BaseURL, Model: req.ModelID, MaxContextLength: req.MaxContextLength, Config: raw})
	if err != nil {
		result.Message = "模型配置无效，请检查设置"
		return result, nil
	}
	cm := client.ChatModel()
	input := []*schema.Message{schema.UserMessage("回复连接成功。")}
	if _, err = cm.Generate(testCtx, input, model.WithTools([]*schema.ToolInfo{})); err != nil {
		result.ConnectionStatus = "failed"
		result.Message = "模型连接测试失败，请检查服务地址、密钥与调用参数"
		result.ResponseTime = time.Since(started).Milliseconds()
		return result, nil
	}
	result.ConnectionStatus = "passed"
	stream, streamErr := cm.Stream(testCtx, input, model.WithTools([]*schema.ToolInfo{}))
	if streamErr == nil {
		_, streamErr = schema.ConcatMessageStream(stream)
	}
	result.StreamingStatus = "passed"
	if streamErr != nil {
		result.StreamingStatus = "failed"
	}
	if options.ToolCalling != nil && *options.ToolCalling {
		toolInfo := &schema.ToolInfo{Name: "model_capability_echo", Desc: "测试工具，返回传入的value，无外部副作用", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"value": {Type: schema.String, Required: true}})}
		toolInput := []*schema.Message{schema.UserMessage("调用model_capability_echo，value为capability-ok；收到结果后回答该值。")}
		// 走流式工具输出，验证拼接后的参数和后续tool response协议。
		toolStream, toolErr := cm.Stream(testCtx, toolInput, model.WithTools([]*schema.ToolInfo{toolInfo}))
		var callMessage *schema.Message
		if toolErr == nil {
			callMessage, toolErr = schema.ConcatMessageStream(toolStream)
		}
		if toolErr == nil && callMessage != nil && len(callMessage.ToolCalls) == 1 {
			call := callMessage.ToolCalls[0]
			var args struct {
				Value string `json:"value"`
			}
			if call.ID != "" && call.Function.Name == toolInfo.Name && json.Unmarshal([]byte(call.Function.Arguments), &args) == nil && args.Value == "capability-ok" {
				toolInput = append(toolInput, callMessage, schema.ToolMessage(`{"value":"capability-ok"}`, call.ID))
				answer, answerErr := cm.Generate(testCtx, toolInput, model.WithTools([]*schema.ToolInfo{}))
				if answerErr == nil && answer != nil && len(answer.ToolCalls) == 0 && strings.Contains(answer.Content, "capability-ok") {
					result.ToolCallingStatus = "passed"
				}
			}
		}
		if result.ToolCallingStatus != "passed" {
			result.ToolCallingStatus = "failed"
		}
	} else {
		result.ToolCallingStatus = "not_requested"
	}
	result.Success = result.StreamingStatus == "passed" && (result.ToolCallingStatus == "passed" || result.ToolCallingStatus == "not_requested")
	result.Message = "模型能力测试完成"
	result.Details = fmt.Sprintf("连接=%s，流式=%s，工具=%s；此结果只验证当前配置，不证明服务商的最大窗口", result.ConnectionStatus, result.StreamingStatus, result.ToolCallingStatus)
	result.ResponseTime = time.Since(started).Milliseconds()
	return result, nil
}

func modelCapabilityStatus(raw []byte, window int) string {
	p, err := llm.ParseModelOptions(raw, window)
	if err != nil {
		return "pending"
	}
	if p.CheckTools() != nil {
		if p.ToolCalling != nil && *p.ToolCalling && p.ToolsWithReasoning == nil {
			return "pending"
		}
		return "text_only"
	}
	return "ready"
}

func decodeSettings(raw []byte) map[string]interface{} {
	var settings map[string]interface{}
	if json.Unmarshal(raw, &settings) != nil {
		return nil
	}
	return settings
}
