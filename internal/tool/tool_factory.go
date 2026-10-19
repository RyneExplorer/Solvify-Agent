package tool

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/logger"
)

type toolFactory struct {
	client     MCPClient
	configRepo MCPConnectionStore
}

func NewFactory(client MCPClient, configRepo MCPConnectionStore) ToolFactory {
	return &toolFactory{client, configRepo}
}
func (f *toolFactory) CreateAgentTools(ctx context.Context, userID string, ids ...string) []einoTool.BaseTool {
	configs, err := f.configRepo.ListEnabledByUserID(ctx, userID)
	if err != nil {
		logger.Errorf("加载MCP连接失败: user_id=%s", userID)
		return nil
	}
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	out := make([]einoTool.BaseTool, 0)
	for _, c := range configs {
		if c.UserID != userID || !c.IsEnabled || (len(ids) > 0 && !allowed[c.ID]) {
			continue
		}
		cfg, prefix, err := ResolveMCPConnection(&c)
		if err != nil {
			logger.Warnf("MCP连接配置不可用: connection_id=%s", c.ID)
			continue
		}
		tools, err := f.client.GetTools(ctx, cfg)
		if err != nil {
			logger.Warnf("MCP连接或工具发现失败: connection_id=%s", c.ID)
			continue
		}
		disabled, _ := DisabledMCPTools(c.Config) // ResolveMCPConnection已校验。
		for _, t := range tools {
			info, err := t.Info(ctx)
			if err != nil || info == nil || disabled[info.Name] {
				continue
			}
			expected, _ := json.Marshal(cfg)
			connectionID := c.ID
			toolName := info.Name
			out = append(out, &prefixedTool{BaseTool: t, prefix: prefix, timeout: cfg.ToolCallTimeout(), check: func(checkCtx context.Context) error {
				current, e := f.configRepo.Get(checkCtx, userID, connectionID)
				if e != nil || current.UserID != userID || !current.IsEnabled {
					return fmt.Errorf("MCP连接已停用或不可用")
				}
				disabled, err := DisabledMCPTools(current.Config)
				if err != nil || disabled[toolName] {
					return fmt.Errorf("MCP工具已停用或配置不可用")
				}
				resolved, currentPrefix, e := ResolveMCPConnection(current)
				if e != nil {
					return fmt.Errorf("MCP配置已失效")
				}
				actual, _ := json.Marshal(resolved)
				if string(actual) != string(expected) || currentPrefix != prefix {
					return fmt.Errorf("MCP配置已更新，请重新发起任务")
				}
				return nil
			}})
		}
	}
	return out
}

// MCPToolPrefix 构造稳定前缀：命名空间与模板标识。
// 部署标识可能含空格等字符，需清洗后使用，
// 保证最终工具名满足模型厂商对函数名的约束（通常只允许字母、数字、下划线、连字符）。
func MCPToolPrefix(namespace, key string) string {
	pk := sanitizeToolNamePart(key)
	if pk == "" {
		return namespace
	}
	return namespace + "_" + pk
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
	check   func(context.Context) error
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
	if len(infoCopy.Name) > 64 {
		sum := sha256.Sum256([]byte(infoCopy.Name))
		infoCopy.Name = infoCopy.Name[:47] + "_" + fmt.Sprintf("%x", sum[:8])
	}
	return &infoCopy, nil
}

// InvokableRun 转发到底层工具的 InvokableRun（若底层实现了 InvokableTool）
func (t *prefixedTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einoTool.Option) (string, error) {
	if t.check != nil {
		if err := t.check(ctx); err != nil {
			return "", err
		}
	}
	if inv, ok := t.BaseTool.(einoTool.InvokableTool); ok {
		runCtx, cancel := t.callContext(ctx)
		defer cancel()
		result, err := inv.InvokableRun(runCtx, argumentsInJSON, opts...)
		return result, ExecutionError(runCtx, err)
	}
	return "", fmt.Errorf("工具 %s 不支持 InvokableRun", t.prefix)
}
