package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
)

// ModelExtraConfig 分开表达模型能力和实际调用参数，缺失值不猜测。
// Config 复用既有 JSONB 列，系统模型和用户模型采用同一契约。
type ModelExtraConfig struct {
	CapabilityOrigin    *CapabilityOrigin `json:"capability_origin,omitempty"`
	MaxInputTokens      int               `json:"max_input_tokens"`
	MaxOutputTokens     int               `json:"max_output_tokens"`
	MaxCompletionTokens *int              `json:"max_completion_tokens"`
	OutputTokenField    string            `json:"output_token_field"`
	ToolCalling         *bool             `json:"tool_calling"`
	ReasoningCapability string            `json:"reasoning_capability"`
	ReasoningMode       string            `json:"reasoning_mode"`
	ReasoningProtocol   string            `json:"reasoning_protocol,omitempty"`
	ReasoningEffort     string            `json:"reasoning_effort,omitempty"`
	ToolsWithReasoning  *bool             `json:"tools_with_reasoning,omitempty"`
	Temperature         *float32          `json:"temperature,omitempty"`
	Timeout             *int              `json:"timeout,omitempty"`
}

// 来源用于展示资料出处，不代替运行时能力校验。
type CapabilityOrigin struct {
	Source    string `json:"source"`
	URL       string `json:"url,omitempty"`
	CheckedAt string `json:"checked_at,omitempty"`
}

func ParseModelOptions(raw []byte, window int) (ModelExtraConfig, error) {
	return parseModelOptions(raw, window, true)
}

// ParseModelDraft validates supplied fields without inventing missing capabilities.
func ParseModelDraft(raw []byte, window int) (ModelExtraConfig, error) {
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte(`{}`)
	}
	return parseModelOptions(raw, window, false)
}

func parseModelOptions(raw []byte, window int, ready bool) (ModelExtraConfig, error) {
	var p ModelExtraConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return p, fmt.Errorf("%w: 模型能力配置缺失或包含不支持的字段，请在模型设置中确认", ErrContextConfig)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return p, fmt.Errorf("%w: 模型配置不是单个 JSON 对象", ErrContextConfig)
	}
	if err := p.validate(window, ready); err != nil {
		return p, fmt.Errorf("%w: %v", ErrContextConfig, err)
	}
	return p, nil
}

func (p ModelExtraConfig) Validate(window int) error {
	return p.validate(window, true)
}

func (p ModelExtraConfig) validate(window int, ready bool) error {
	if p.CapabilityOrigin != nil && p.CapabilityOrigin.Source != "manual" && p.CapabilityOrigin.Source != "provider_metadata" && p.CapabilityOrigin.Source != "official_catalog" {
		return fmt.Errorf("能力来源无效")
	}
	if window < 0 || window > 2000000 || (window > 0 && window < 1024) || p.MaxInputTokens < 0 || p.MaxOutputTokens < 0 ||
		(window > 0 && (p.MaxInputTokens > window || p.MaxOutputTokens > window)) ||
		(p.MaxCompletionTokens != nil && (*p.MaxCompletionTokens <= 0 || (p.MaxOutputTokens > 0 && *p.MaxCompletionTokens > p.MaxOutputTokens) || (window > 0 && *p.MaxCompletionTokens >= window))) ||
		(ready && (window == 0 || p.MaxOutputTokens == 0 || p.MaxCompletionTokens == nil)) {
		return fmt.Errorf("请确认总窗口、最大输入、最大生成及本次生成额度；调用额度不能超过模型能力")
	}
	if (ready || p.OutputTokenField != "") && p.OutputTokenField != "max_tokens" && p.OutputTokenField != "max_completion_tokens" {
		return fmt.Errorf("请选择服务商支持的输出参数字段")
	}
	if ready && p.ToolCalling == nil {
		return fmt.Errorf("请确认模型是否支持工具调用")
	}
	if (ready || p.ReasoningCapability != "") && p.ReasoningCapability != "none" && p.ReasoningCapability != "optional" && p.ReasoningCapability != "always" {
		return fmt.Errorf("请确认模型思考能力")
	}
	if (ready || p.ReasoningMode != "") && p.ReasoningMode != "default" && p.ReasoningMode != "off" && p.ReasoningMode != "on" {
		return fmt.Errorf("思考设置无效")
	}
	if (p.ReasoningCapability == "none" && p.ReasoningMode == "on") || (p.ReasoningCapability == "always" && p.ReasoningMode == "off") {
		return fmt.Errorf("思考设置与模型能力不兼容")
	}
	if p.ReasoningProtocol != "" && p.ReasoningProtocol != "thinking" && p.ReasoningProtocol != "enable_thinking" && p.ReasoningProtocol != "reasoning_effort" {
		return fmt.Errorf("思考参数协议不支持")
	}
	if p.ReasoningCapability == "optional" && p.ReasoningMode != "" && p.ReasoningMode != "default" && p.ReasoningProtocol == "" {
		return fmt.Errorf("切换思考模式需要指定服务商参数协议")
	}
	if p.ReasoningEffort != "" && (p.ReasoningProtocol != "reasoning_effort" || (p.ReasoningEffort != "low" && p.ReasoningEffort != "medium" && p.ReasoningEffort != "high") || p.ReasoningMode == "off" || p.ReasoningCapability == "none") {
		return fmt.Errorf("思考强度不适用于当前配置")
	}
	if p.ReasoningProtocol == "reasoning_effort" && p.ReasoningMode != "" && p.ReasoningMode != "default" {
		return fmt.Errorf("reasoning_effort 仅配置强度；开关使用服务商默认行为")
	}
	if p.Temperature != nil && (math.IsNaN(float64(*p.Temperature)) || math.IsInf(float64(*p.Temperature), 0) || *p.Temperature < 0 || *p.Temperature > 2) {
		return fmt.Errorf("温度必须在0到2之间，不支持的模型请留空")
	}
	if p.Timeout != nil && (*p.Timeout <= 0 || *p.Timeout > 300) {
		return fmt.Errorf("请求超时必须在1到300秒之间")
	}
	return nil
}

func (p ModelExtraConfig) CheckTools() error {
	if p.ToolCalling == nil || !*p.ToolCalling {
		return fmt.Errorf("%w: 当前模型未声明支持工具调用，请选择工具模型", ErrContextConfig)
	}
	if p.ReasoningCapability != "none" && p.ReasoningMode != "off" && (p.ToolsWithReasoning == nil || !*p.ToolsWithReasoning) {
		return fmt.Errorf("%w: 请确认当前思考模式支持工具调用，或关闭可切换的思考模式", ErrContextConfig)
	}
	return nil
}
