package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/logger"
)

// AgentToolConfig Agent 工具配置
type AgentToolConfig struct {
	ToolType       *entity.ToolType
	Provider       Provider
	ProviderConfig *ProviderConfig
	InputSchema    json.RawMessage // Agent 调用参数 Schema
	AdminConfig    map[string]interface{}
	UserConfig     map[string]interface{}
}

// AgentTool 给 eino ReAct Agent 注册的工具包装器
type AgentTool struct {
	config *AgentToolConfig
}

// NewAgentTool 创建 Agent 工具
func NewAgentTool(config *AgentToolConfig) *AgentTool {
	return &AgentTool{config: config}
}

// Info 返回工具元信息
func (t *AgentTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	tt := t.config.ToolType

	// 从 ToolType 或 ProviderConfig 获取 input_schema
	inputSchema := t.getInputSchema()
	paramsSchema := buildParamsSchema(inputSchema)

	desc := tt.Description
	if desc == "" {
		desc = fmt.Sprintf("通过 %s 调用 %s", t.config.Provider.Name(), tt.Name)
	}

	return &schema.ToolInfo{
		Name:        tt.ToolKey,
		Desc:        desc,
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(paramsSchema),
	}, nil
}

// getInputSchema 获取 input_schema
// 优先级：ToolType > ToolProvider > 自动从 BodyTemplate 推断
func (t *AgentTool) getInputSchema() map[string]interface{} {
	// 1. 从配置获取
	if inputSchema := t.getConfiguredInputSchema(); inputSchema != nil {
		return inputSchema
	}

	// 2. 自动从 BodyTemplate 推断 LLM 需要提供的参数
	if t.config.ProviderConfig != nil && len(t.config.ProviderConfig.BodyTemplate) > 0 {
		placeholders := extractPlaceholders(t.config.ProviderConfig.BodyTemplate)
		placeholders = filterProvidedPlaceholders(placeholders, t.config.UserConfig, t.config.AdminConfig)

		if len(placeholders) > 0 {
			props := make(map[string]interface{})
			required := make([]interface{}, 0, len(placeholders))
			for _, ph := range placeholders {
				desc := "工具调用参数"
				if ph == "query" {
					desc = "搜索关键词"
				}
				props[ph] = map[string]interface{}{
					"type":        "string",
					"description": desc,
				}
				required = append(required, ph)
			}
			return map[string]interface{}{
				"type":       "object",
				"properties": props,
				"required":   required,
			}
		}
	}

	return nil
}

// getConfiguredInputSchema 从 ToolType 或 ToolProvider 获取已配置的 input_schema
func (t *AgentTool) getConfiguredInputSchema() map[string]interface{} {
	// 优先从 ToolType 获取
	if len(t.config.ToolType.InputSchema) > 0 {
		var s map[string]interface{}
		if err := json.Unmarshal(t.config.ToolType.InputSchema, &s); err == nil {
			return s
		}
	}

	// 从 ToolProvider.InputSchema 获取
	if len(t.config.InputSchema) > 0 {
		var s map[string]interface{}
		if err := json.Unmarshal(t.config.InputSchema, &s); err == nil {
			return s
		}
	}

	return nil
}

// extractPlaceholders 从任意 JSON 结构中提取 {{xxx}} 占位符
func extractPlaceholders(v interface{}) []string {
	seen := make(map[string]bool)
	var walk func(interface{})
	walk = func(x interface{}) {
		switch val := x.(type) {
		case string:
			s := val
			for {
				start := strings.Index(s, "{{")
				if start == -1 {
					break
				}
				end := strings.Index(s[start+2:], "}}")
				if end == -1 {
					break
				}
				ph := s[start+2 : start+2+end]
				if ph != "" {
					seen[ph] = true
				}
				s = s[start+2+end+2:]
			}
		case map[string]interface{}:
			for _, child := range val {
				walk(child)
			}
		case []interface{}:
			for _, child := range val {
				walk(child)
			}
		}
	}
	walk(v)

	result := make([]string, 0, len(seen))
	for k := range seen {
		result = append(result, k)
	}
	return result
}

// filterProvidedPlaceholders 过滤掉已由用户配置或管理员配置提供的占位符
func filterProvidedPlaceholders(placeholders []string, configs ...map[string]interface{}) []string {
	provided := make(map[string]bool)
	for _, cfg := range configs {
		for k := range cfg {
			provided[k] = true
		}
	}

	result := make([]string, 0, len(placeholders))
	for _, ph := range placeholders {
		if !provided[ph] {
			result = append(result, ph)
		}
	}
	return result
}

// InvokableRun 执行工具调用
func (t *AgentTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einoTool.Option) (string, error) {
	// 1. 解析 LLM 参数
	var toolInput map[string]interface{}
	if err := json.Unmarshal([]byte(argumentsInJSON), &toolInput); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	// 2. 调用 Provider.Execute
	result, err := t.config.Provider.Execute(ctx, &ExecuteConfig{
		ToolInput:      toolInput,
		UserConfig:     t.config.UserConfig,
		ProviderConfig: t.config.ProviderConfig,
		AdminConfig:    t.config.AdminConfig,
	})
	if err != nil {
		return "", err
	}

	return result, nil
}

// ========== ToolFactory ==========

// toolFactory 工具工厂实现
type toolFactory struct {
	registry   ProviderRegistry
	configRepo UserToolConfigStore
	typeRepo   ToolTypeStore
}

// NewFactory 创建工具工厂
func NewFactory(registry ProviderRegistry, configRepo UserToolConfigStore, typeRepo ToolTypeStore) ToolFactory {
	return &toolFactory{
		registry:   registry,
		configRepo: configRepo,
		typeRepo:   typeRepo,
	}
}

// CreateAgentTools 根据用户配置创建 Agent 工具列表
func (f *toolFactory) CreateAgentTools(ctx context.Context, userID string) []einoTool.BaseTool {
	configs, err := f.configRepo.ListEnabledByUserID(ctx, userID)
	if err != nil {
		logger.Errorf("[ToolFactory] 加载用户工具配置失败: userID=%s, err=%v", userID, err)
		return nil
	}

	tools := make([]einoTool.BaseTool, 0, len(configs))
	for i := range configs {
		config := &configs[i]

		// 获取 Provider 实例（根据 provider_type）
		providerType := config.ToolProvider.ProviderType
		provider := f.registry.Get(providerType)
		if provider == nil {
			logger.Warnf("[ToolFactory] 供应商类型未注册，跳过: providerType=%s", providerType)
			continue
		}

		// 解析 ProviderConfig
		var providerConfig ProviderConfig
		if len(config.ToolProvider.ProviderConfig) > 0 {
			if err := json.Unmarshal(config.ToolProvider.ProviderConfig, &providerConfig); err != nil {
				logger.Warnf("[ToolFactory] 解析 ProviderConfig 失败，跳过: err=%v", err)
				continue
			}
		}

		// 解析 AdminConfig
		var adminConfig map[string]interface{}
		if len(config.ToolProvider.AdminConfig) > 0 {
			if err := json.Unmarshal(config.ToolProvider.AdminConfig, &adminConfig); err != nil {
				logger.Warnf("[ToolFactory] 解析 AdminConfig 失败: %v", err)
			}
		}

		// 解析 UserConfig
		var userConfig map[string]interface{}
		if len(config.Config) > 0 {
			if err := json.Unmarshal(config.Config, &userConfig); err != nil {
				logger.Warnf("[ToolFactory] 解析 UserConfig 失败: %v", err)
			}
		}

		// 创建 AgentTool
		agentTool := NewAgentTool(&AgentToolConfig{
			ToolType:       &config.ToolType,
			Provider:       provider,
			ProviderConfig: &providerConfig,
			InputSchema:    json.RawMessage(config.ToolProvider.InputSchema),
			AdminConfig:    adminConfig,
			UserConfig:     userConfig,
		})

		tools = append(tools, agentTool)
	}

	return tools
}

// ========== Schema 构建辅助 ==========

// buildParamsSchema 从 map 构建 eino jsonschema.Schema
func buildParamsSchema(def map[string]interface{}) *jsonschema.Schema {
	s := &jsonschema.Schema{Type: "object"}
	if def == nil {
		return s
	}

	if propsRaw, ok := def["properties"].(map[string]interface{}); ok {
		props := jsonschema.NewProperties()
		for name, propDef := range propsRaw {
			pd, ok := propDef.(map[string]interface{})
			if !ok {
				continue
			}
			propSchema := &jsonschema.Schema{}
			if t, ok := pd["type"].(string); ok {
				propSchema.Type = t
			}
			if d, ok := pd["description"].(string); ok {
				propSchema.Description = d
			}
			props.Set(name, propSchema)
		}
		s.Properties = props
	}

	if reqArr, ok := def["required"].([]interface{}); ok {
		required := make([]string, 0, len(reqArr))
		for _, r := range reqArr {
			if rs, ok := r.(string); ok {
				required = append(required, rs)
			}
		}
		s.Required = required
	}

	return s
}
