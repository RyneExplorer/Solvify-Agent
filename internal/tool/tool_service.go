package tool

import (
	"context"
	"encoding/json"
	"fmt"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"

	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/logger"
)

// ToolInstance 工具实例——绑定工具类型定义 + 供应商能力 + 用户配置
type ToolInstance struct {
	ToolType   *entity.ToolType
	Provider   Provider
	UserConfig *entity.UserToolConfig
}

// NewToolInstance 创建工具实例
func NewToolInstance(toolType *entity.ToolType, provider Provider, config *entity.UserToolConfig) *ToolInstance {
	return &ToolInstance{
		ToolType:   toolType,
		Provider:   provider,
		UserConfig: config,
	}
}

// AgentTool 给 eino ReAct Agent 注册的工具包装器
// Info() 只暴露 tool_type.input_schema，LLM 看不到 api_key 等配置
type AgentTool struct {
	instance *ToolInstance
}

// NewAgentTool 创建 Agent 工具
func NewAgentTool(instance *ToolInstance) *AgentTool {
	return &AgentTool{instance: instance}
}

// Info 返回工具元信息——input_schema 从 Provider 取，不暴露用户配置
func (t *AgentTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	tt := t.instance.ToolType

	// Agent 参数从 Provider 代码取（固定，不存 DB）
	inputSchema := t.instance.Provider.GetInputSchema()
	paramsSchema := buildParamsSchema(inputSchema)

	desc := tt.Description
	if desc == "" {
		desc = fmt.Sprintf("通过 %s 调用 %s", t.instance.Provider.Name(), tt.Name)
	}

	return &schema.ToolInfo{
		Name:        tt.ToolKey,
		Desc:        desc,
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(paramsSchema),
	}, nil
}

// InvokableRun 执行工具调用
// 参数分离：LLM 参数（toolInput）+ 用户配置（config）→ Provider.Execute
func (t *AgentTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einoTool.Option) (string, error) {
	var toolInput map[string]interface{}
	if err := json.Unmarshal([]byte(argumentsInJSON), &toolInput); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(t.instance.UserConfig.Config, &config); err != nil {
		return "", fmt.Errorf("配置解析失败: %w", err)
	}

	// 解析管理员配置的业务参数
	var adminConfig map[string]interface{}
	if len(t.instance.UserConfig.ToolProvider.AdminConfig) > 0 {
		json.Unmarshal(t.instance.UserConfig.ToolProvider.AdminConfig, &adminConfig)
	}

	return t.instance.Provider.Execute(ctx, toolInput, config, adminConfig)
}

// ========== ToolFactory ==========

// toolFactory 工具工厂实现
//
//	读路径：Redis 缓存 → miss → DB（通过 CachedRepository）
//	写路径：DB → 删除缓存 → 下次读自动回填
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
//
//	先从 Redis 缓存读 → 未命中则查 DB → 回填缓存
//	DB 写入（Create/Update/Delete）时缓存已被清除，保证数据一致
func (f *toolFactory) CreateAgentTools(ctx context.Context, userID string) []einoTool.BaseTool {
	configs, err := f.configRepo.ListEnabledByUserID(ctx, userID)
	if err != nil {
		logger.Errorf("[ToolFactory] 加载用户工具配置失败: userID=%s, err=%v", userID, err)
		return nil
	}

	logger.Infof("[ToolFactory] userID=%s 查到 %d 条启用的工具配置", userID, len(configs))

	tools := make([]einoTool.BaseTool, 0, len(configs))
	for i := range configs {
		config := &configs[i]

		logger.Infof("[ToolFactory] 处理配置 #%d: toolKey=%s, providerKey=%s",
			i+1, config.ToolType.ToolKey, config.ToolProvider.ProviderKey)

		toolType, err := f.typeRepo.GetByKey(ctx, config.ToolType.ToolKey)
		if err != nil {
			logger.Warnf("[ToolFactory] 工具类型不存在，跳过: toolKey=%s, err=%v", config.ToolType.ToolKey, err)
			continue
		}

		providerKey := config.ToolProvider.ProviderKey
		provider := f.registry.Get(providerKey)
		if provider == nil {
			logger.Warnf("[ToolFactory] 供应商未注册，跳过: providerKey=%s, userID=%s", providerKey, userID)
			continue
		}

		instance := NewToolInstance(toolType, provider, config)
		agentTool := NewAgentTool(instance)
		tools = append(tools, agentTool)
		logger.Infof("[ToolFactory] 工具加载成功: toolKey=%s, provider=%s, toolName=%s",
			toolType.ToolKey, provider.Name(), toolType.Name)
	}

	logger.Infof("[ToolFactory] userID=%s 最终加载 %d 个工具", userID, len(tools))
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
