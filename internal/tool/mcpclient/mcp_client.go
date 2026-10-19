package mcpclient

import (
	"context"
	"fmt"
	"time"

	mcpp "github.com/cloudwego/eino-ext/components/tool/mcp"

	einoTool "github.com/cloudwego/eino/components/tool"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/logger"
)

// MCPClient MCP 客户端
// 通过 MCP 协议（sse/http）连接 MCP Server，一个 Server 可提供多个工具
type MCPClient struct {
	pool *MCPClientPool
}

// NewMCPClient 创建 MCP Client
func NewMCPClient(pool *MCPClientPool) *MCPClient {
	return &MCPClient{pool: pool}
}

// Validate 验证MCP连接配置。
func (p *MCPClient) Validate(config *tool.MCPConfig) error {
	return validateMCPConfig(config)
}

// GetTools 拉取 MCP Server 的所有工具，返回 eino BaseTool 列表
// 带缓存：TTL 内直接返回缓存的工具列表，避免每次会话都连接 MCP server
func (p *MCPClient) GetTools(ctx context.Context, mcpCfg *tool.MCPConfig) ([]einoTool.BaseTool, error) {
	if mcpCfg == nil {
		return nil, fmt.Errorf("mcp 配置不能为空")
	}

	c, entry, err := p.pool.GetOrCreate(ctx, mcpCfg)
	if err != nil {
		return nil, fmt.Errorf("获取 MCP 客户端失败: %w", err)
	}

	// 1. 检查缓存是否在 TTL 内
	entry.toolsCacheMu.RLock()
	if time.Since(entry.toolsCacheTime) < toolsCacheTTL && len(entry.toolsCache) > 0 {
		tools := entry.toolsCache
		entry.toolsCacheMu.RUnlock()
		logger.Debugf("[MCPClient] 命中工具列表缓存: tools=%d", len(tools))
		return tools, nil
	}
	entry.toolsCacheMu.RUnlock()

	// 2. 未命中或过期：调用 eino-ext/mcp 包拉取工具列表
	listCtx, cancel := context.WithTimeout(ctx, mcpCfg.ToolCallTimeout())
	defer cancel()
	if !entry.Acquire() {
		return nil, fmt.Errorf("MCP连接已被回收，请重试")
	}
	defer entry.Release()
	mcpTools, err := mcpp.GetTools(listCtx, &mcpp.Config{
		Cli:           c,
		CustomHeaders: mcpCfg.Headers,
	})
	if err != nil {
		return nil, fmt.Errorf("拉取 MCP 工具列表失败: %w", err)
	}

	if len(mcpTools) > 128 {
		return nil, fmt.Errorf("MCP工具清单超过128个工具限制")
	}

	// 3. 刷新缓存
	//    包装成 leasedTool：每次调用期间借用连接池条目，
	//    保证 sweep 不会在 tools/call 执行过程中把连接关掉。
	wrapped := make([]einoTool.BaseTool, 0, len(mcpTools))
	for _, mt := range mcpTools {
		wrapped = append(wrapped, &leasedTool{BaseTool: mt, entry: entry})
	}
	entry.toolsCacheMu.Lock()
	entry.toolsCache = wrapped
	entry.toolsCacheTime = time.Now()
	entry.toolsCacheMu.Unlock()

	logger.Infof("[MCPClient] 拉取 MCP 工具列表成功: tools=%d", len(wrapped))
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
