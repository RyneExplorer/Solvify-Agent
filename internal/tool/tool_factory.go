package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

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
				// 必须 TrimSpace：模板里写 {{ query }} 会提取出 " query "，
				// 既可能违反 LLM 函数名约束，也会让 filterProvidedPlaceholders 的
				// 精确匹配失效——用户明明配过的值仍会被要求 LLM 再填一次。
				ph := strings.TrimSpace(s[start+2 : start+2+end])
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
	// map 遍历顺序随机，不排序的话每次生成的 schema required 数组顺序都不同，
	// 导致同一份配置产出不稳定的工具定义（影响缓存与行为确定性）。
	sort.Strings(result)
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
// userConfigIDs 非空时仅加载指定 ID 的启用配置，用于对话级 MCP 白名单
func (f *toolFactory) CreateAgentTools(ctx context.Context, userID string, userConfigIDs ...string) []einoTool.BaseTool {
	configs, err := f.configRepo.ListEnabledByUserID(ctx, userID)
	if err != nil {
		logger.Errorf("[ToolFactory] 加载用户工具配置失败: userID=%s, err=%v", userID, err)
		return nil
	}

	// 白名单过滤：非空时只保留指定 ID
	if len(userConfigIDs) > 0 {
		idSet := make(map[string]struct{}, len(userConfigIDs))
		for _, id := range userConfigIDs {
			idSet[id] = struct{}{}
		}
		filtered := make([]entity.UserToolConfig, 0, len(configs))
		for _, c := range configs {
			if _, ok := idSet[c.ID]; ok {
				filtered = append(filtered, c)
			}
		}
		configs = filtered
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

		// MCP 场景：多工具供应商，一个 MCP Server 提供多个工具
		if multi, ok := provider.(MultiToolProvider); ok && providerConfig.MCP != nil {
			mcpTools, err := multi.GetTools(ctx, &providerConfig)
			if err != nil {
				logger.Warnf("[ToolFactory] MCP 拉取工具列表失败，跳过: providerKey=%s, err=%v",
					config.ToolProvider.ProviderKey, err)
				continue
			}

			// 前缀需带上 provider_key，否则多个 MCP Server 出现同名工具（如都有 search_files）
			// 时后者会覆盖前者，Agent 实际只能调到其中一个。
			// 最终形态：{tool_key}_{provider_key}_{tool_name}，例如 mcp_filesystem_read_file
			prefix := mcpToolPrefix(config.ToolType.ToolKey, config.ToolProvider.ProviderKey)
			callTimeout := mcpCallTimeout(providerConfig.MCP)
			for _, mt := range mcpTools {
				tools = append(tools, &prefixedTool{
					BaseTool: mt,
					prefix:   prefix,
					timeout:  callTimeout,
				})
			}
			logger.Infof("[ToolFactory] MCP 工具加载成功: providerKey=%s, prefix=%s, tools=%d, callTimeout=%v",
				config.ToolProvider.ProviderKey, prefix, len(mcpTools), callTimeout)
			continue
		}

		// 原有逻辑：单工具供应商（HTTP 等）
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

// mcpToolPrefix 构造 MCP 工具名前缀：{tool_key}_{provider_key}
// provider_key 由管理员填写，可能含空格等非法字符，需清洗后使用，
// 保证最终工具名满足模型厂商对函数名的约束（通常只允许字母、数字、下划线、连字符）。
func mcpToolPrefix(toolKey, providerKey string) string {
	pk := sanitizeToolNamePart(providerKey)
	if pk == "" {
		return toolKey
	}
	return toolKey + "_" + pk
}

// sanitizeToolNamePart 将任意字符串清洗为合法的工具名片段
func sanitizeToolNamePart(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// defaultMCPCallTimeout 未配置 timeout 时的单次 MCP 工具调用超时兜底
// MCP Server 多为外部进程/远程服务，可能卡死或长时间无响应；
// 没有兜底的话一次 tools/call 会把整个 Agent 请求无限期挂住。
const defaultMCPCallTimeout = 300 * time.Second

// maxTimeoutSeconds 超时秒数上限，超过后 int64 纳秒溢出会让 WithTimeout 立即触发
const maxTimeoutSeconds = 24 * 60 * 60

// mcpCallTimeout 计算单次 MCP 工具调用超时
// 优先使用管理员配置的 Timeout（秒），未配置时回落到兜底值
func mcpCallTimeout(cfg *MCPProviderConfig) time.Duration {
	if cfg == nil || cfg.Timeout <= 0 {
		return defaultMCPCallTimeout
	}
	sec := cfg.Timeout
	if sec > maxTimeoutSeconds {
		logger.Warnf("[ToolFactory] MCP timeout 配置值 %d 超过上限，已钳制为 %d 秒", sec, maxTimeoutSeconds)
		sec = maxTimeoutSeconds
	}
	return time.Duration(sec) * time.Second
}

// prefixedTool 为 BaseTool 的工具名添加前缀，避免不同 MCP Server 工具名冲突
// 例如 MCP Server "filesystem" 的 read_file 工具 → Agent 看到的工具名为 "mcp_filesystem_read_file"
//
// ⚠️ 本类型**只实现 InvokableRun，刻意不实现 StreamableRun**。别"顺手补上"，补上会让危险工具审批失效：
//
//	eino v0.9.1 compose/tool_node.go:524-527
//	  实现了 StreamableTool → streamable = wrapStreamToolCall(它, params.middlewares.streamable)
//	  —— 只套「流式」中间件列表；本项目只注册了 ToolMiddleware{Invokable: ...}，流式列表为空 → 审批被静默跳过。
//	eino v0.9.1 compose/tool_node.go:566-571
//	  未实现 StreamableTool → streamable = invokableToStreamable(已包 Invokable 中间件的端点)
//	  —— 流式端点由已包中间件的 invokable 端点派生，审批/澄清正常工作。
//
// 而且 invokableToStreamable 的语义与手写降级分支完全等价（调 InvokableRun 后把结果包成
// 单元素 StreamReader），MCP 调用超时也仍由 InvokableRun 内的 callContext 施加 —— 所以
// "不实现" 既不会让 MCP 工具在流式链路报错，也是唯一能让审批生效的写法。
// 回归闸：internal/tool/prefixed_tool_test.go 的 TestPrefixedTool_StreamPath_AppliesInvokableMiddleware。
type prefixedTool struct {
	einoTool.BaseTool
	prefix string
	// timeout 单次调用超时，<=0 表示不额外限制（仅由上游 ctx 控制）
	timeout time.Duration
}

// callContext 为单次工具调用施加超时保护
func (t *prefixedTool) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if t.timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, t.timeout)
}

func (t *prefixedTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	info, err := t.BaseTool.Info(ctx)
	if err != nil {
		return nil, err
	}
	// 拷贝 ToolInfo，避免修改底层工具缓存的 ToolInfo（否则每次调用都会叠加前缀）
	infoCopy := *info
	infoCopy.Name = t.prefix + "_" + info.Name
	return &infoCopy, nil
}

// InvokableRun 转发到底层工具的 InvokableRun（若底层实现了 InvokableTool）
func (t *prefixedTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einoTool.Option) (string, error) {
	if inv, ok := t.BaseTool.(einoTool.InvokableTool); ok {
		runCtx, cancel := t.callContext(ctx)
		defer cancel()
		return inv.InvokableRun(runCtx, argumentsInJSON, opts...)
	}
	return "", fmt.Errorf("工具 %s 不支持 InvokableRun", t.prefix)
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
