package providers

import (
	"context"
	"fmt"
	"time"

	mcpp "github.com/cloudwego/eino-ext/components/tool/mcp"
	"github.com/mark3labs/mcp-go/mcp"

	einoTool "github.com/cloudwego/eino/components/tool"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/logger"
)

// MCPProvider MCP 供应商
// 通过 MCP 协议（stdio/sse/http）连接 MCP Server，一个 Server 可提供多个工具
type MCPProvider struct {
	pool *MCPClientPool
}

// NewMCPProvider 创建 MCP Provider
func NewMCPProvider(pool *MCPClientPool) *MCPProvider {
	return &MCPProvider{pool: pool}
}

func (p *MCPProvider) Name() string { return "mcp" }

// Validate 验证执行配置
func (p *MCPProvider) Validate(config *tool.ExecuteConfig) error {
	if config.ProviderConfig == nil {
		return fmt.Errorf("provider_config 不能为空")
	}
	if config.ProviderConfig.MCP == nil {
		return fmt.Errorf("mcp 配置不能为空")
	}
	return validateMCPConfig(config.ProviderConfig.MCP)
}

// Execute 执行工具调用
// MCP 主要通过 GetTools 返回多个 BaseTool 由 Agent 直接调用，
// 此方法保留用于兼容 Provider 接口和管理员测试 API（Test）
func (p *MCPProvider) Execute(ctx context.Context, config *tool.ExecuteConfig) (string, error) {
	if config.ProviderConfig == nil || config.ProviderConfig.MCP == nil {
		return "", fmt.Errorf("mcp 配置不能为空")
	}

	mcpCfg := config.ProviderConfig.MCP
	c, entry, err := p.pool.GetOrCreate(ctx, mcpCfg)
	if err != nil {
		return "", fmt.Errorf("获取 MCP 客户端失败: %w", err)
	}
	if c == nil {
		return "", fmt.Errorf("MCP 客户端不可用")
	}

	// 管理员测试场景：调用 MCP tools/list 返回工具清单，便于验证连接是否正常
	listReq := mcp.ListToolsRequest{}
	result, err := p.listTools(ctx, entry, listReq)
	if err != nil {
		return "", fmt.Errorf("调用 MCP tools/list 失败: %w", err)
	}

	// 返回工具数量和工具名列表
	toolNames := make([]string, 0, len(result.Tools))
	for _, t := range result.Tools {
		toolNames = append(toolNames, t.Name)
	}
	return fmt.Sprintf("MCP 连接成功，共 %d 个工具: %v", len(result.Tools), toolNames), nil
}

// listTools 借用连接池条目后调用 tools/list，避免调用途中连接被空闲回收
func (p *MCPProvider) listTools(ctx context.Context, entry *mcpPoolEntry, req mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	if entry != nil {
		if !entry.Acquire() {
			return nil, fmt.Errorf("MCP 连接已被回收，请重试")
		}
		defer entry.Release()
	}
	return entry.client.ListTools(ctx, req)
}

// GetTools 拉取 MCP Server 的所有工具，返回 eino BaseTool 列表
// 带缓存：TTL 内直接返回缓存的工具列表，避免每次会话都连接 MCP server
func (p *MCPProvider) GetTools(ctx context.Context, providerConfig *tool.ProviderConfig) ([]einoTool.BaseTool, error) {
	if providerConfig == nil || providerConfig.MCP == nil {
		return nil, fmt.Errorf("mcp 配置不能为空")
	}

	mcpCfg := providerConfig.MCP
	c, entry, err := p.pool.GetOrCreate(ctx, mcpCfg)
	if err != nil {
		return nil, fmt.Errorf("获取 MCP 客户端失败: %w", err)
	}

	// 1. 检查缓存是否在 TTL 内
	entry.toolsCacheMu.RLock()
	if time.Since(entry.toolsCacheTime) < toolsCacheTTL && len(entry.toolsCache) > 0 {
		tools := entry.toolsCache
		entry.toolsCacheMu.RUnlock()
		logger.Debugf("[MCPProvider] 命中工具列表缓存: tools=%d", len(tools))
		return tools, nil
	}
	entry.toolsCacheMu.RUnlock()

	// 2. 未命中或过期：调用 eino-ext/mcp 包拉取工具列表
	mcpTools, err := mcpp.GetTools(ctx, &mcpp.Config{
		Cli:           c,
		CustomHeaders: mcpCfg.Headers,
	})
	if err != nil {
		return nil, fmt.Errorf("拉取 MCP 工具列表失败: %w", err)
	}

	// 3. 刷新缓存
	//    包装成 leasedTool：每次调用期间借用连接池条目，
	//    保证 sweep 不会在 tools/call 执行过程中把连接（及其 stdio 子进程）关掉。
	wrapped := make([]einoTool.BaseTool, 0, len(mcpTools))
	for _, mt := range mcpTools {
		wrapped = append(wrapped, &leasedTool{BaseTool: mt, entry: entry})
	}
	entry.toolsCacheMu.Lock()
	entry.toolsCache = wrapped
	entry.toolsCacheTime = time.Now()
	entry.toolsCacheMu.Unlock()

	logger.Infof("[MCPProvider] 拉取 MCP 工具列表成功: tools=%d", len(wrapped))
	return wrapped, nil
}

// leasedTool 在单次工具调用期间借用连接池条目，防止连接被空闲回收掉
type leasedTool struct {
	einoTool.BaseTool
	entry *mcpPoolEntry
}

// InvokableRun 借用 → 调用 → 归还
// 借用失败说明条目已被回收（如管理员改了配置），此时应让调用方重试而非打到已关闭的连接上。
func (t *leasedTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einoTool.Option) (string, error) {
	inv, ok := t.BaseTool.(einoTool.InvokableTool)
	if !ok {
		return "", fmt.Errorf("底层 MCP 工具不支持 InvokableRun")
	}
	if !t.entry.Acquire() {
		return "", fmt.Errorf("MCP 连接已被回收，请重试该工具调用")
	}
	defer t.entry.Release()
	return inv.InvokableRun(ctx, argumentsInJSON, opts...)
}
