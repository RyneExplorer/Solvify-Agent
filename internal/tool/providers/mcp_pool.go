package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	einoTool "github.com/cloudwego/eino/components/tool"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/logger"
)

const (
	// toolsCacheTTL 工具列表缓存有效期
	toolsCacheTTL = 5 * time.Minute
	// defaultTimeout 默认连接超时秒数
	defaultTimeout = 30
	// maxIdle 空闲多久后回收。管理员改 MCP 配置会产生新的 configHash，
	// 旧条目不会再被命中，只能靠空闲回收释放其 stdio 子进程/长连接。
	maxIdle = 30 * time.Minute
	// maxEntries 池中最多保留的连接数，超出后淘汰最久未使用的
	maxEntries = 64
	// failureTTL 初始化失败的负缓存时长。服务端持续故障时，若每次请求都重建条目，
	// 会反复拉起子进程/重连并形成惊群；期限内直接返回上次错误。
	failureTTL = 30 * time.Second
	// initHardTimeout 初始化总时限兜底。
	// stdio 的 NewStdioMCPClient 内部会拉起子进程且**不接受 ctx**，
	// 子进程卡住时没有总时限兜底会让 initDone 永不关闭，
	// 进而把等它的 sweep() 和 Close() 一起挂死。
	initHardTimeout = 60 * time.Second
	// maxTimeoutSeconds 超时配置上限。超过后 int64 纳秒会溢出导致
	// context.WithTimeout 立即触发，故做钳制。
	maxTimeoutSeconds = 24 * 60 * 60
)

// clampTimeoutSeconds 将管理员配置的超时秒数收敛到合法范围
func clampTimeoutSeconds(sec int) int {
	switch {
	case sec <= 0:
		return defaultTimeout
	case sec > maxTimeoutSeconds:
		return maxTimeoutSeconds
	default:
		return sec
	}
}

// initResult 由初始化 goroutine 回传，避免多个 goroutine 并发写 entry 字段
type initResult struct {
	client *client.Client
	err    error
}

// MCPClientPool MCP 客户端连接池
// 按 MCPProviderConfig 的 hash 复用 *client.Client，避免反复启动子进程或建立连接
type MCPClientPool struct {
	mu      sync.Mutex
	closed  atomic.Bool
	clients map[string]*mcpPoolEntry // key = configHash
}

// mcpPoolEntry 连接池条目
type mcpPoolEntry struct {
	client   *client.Client
	initErr  error
	initDone chan struct{} // 并发同步：避免重复初始化
	failedAt time.Time     // 初始化失败时刻，用于负缓存；零值表示未失败

	// leaseMu 保护「借用计数」与「淘汰标记」的一致性。
	// Acquire 与 tryMarkEvicted 必须在同一把锁下互斥，否则会出现：
	// sweep 读完 refs==0（决定淘汰）→ Acquire 完成 refs+1 且两次 evicted 检查都通过
	// → sweep 才置 evicted 并关闭连接，调用方借到一个即将失效的连接。
	// 锁顺序固定为 p.mu → leaseMu，不会死锁。
	leaseMu sync.Mutex
	// refs 借用计数：正在执行 tools/call 的条目不允许被回收
	refs atomic.Int64
	// evicted 标记条目已被摘出连接池，此后不再接受新的借用
	evicted atomic.Bool
	// lastUsed 最近一次被取用/借用的时间戳（UnixNano），用于空闲回收
	lastUsed atomic.Int64

	// 工具列表缓存（带 TTL）
	toolsCache     []einoTool.BaseTool
	toolsCacheTime time.Time
	toolsCacheMu   sync.RWMutex
}

// touch 标记为刚被使用
func (e *mcpPoolEntry) touch() { e.lastUsed.Store(time.Now().UnixNano()) }

// idleFor 返回空闲时长
func (e *mcpPoolEntry) idleFor(now time.Time) time.Duration {
	ts := e.lastUsed.Load()
	if ts == 0 {
		return 0
	}
	return now.Sub(time.Unix(0, ts))
}

// Acquire 借用连接。返回 false 表示条目已被回收，调用方不应再使用该连接。
// 借用期间 sweep 不会关闭它——关闭 stdio 会直接终止子进程，中断在途的 tools/call。
func (e *mcpPoolEntry) Acquire() bool {
	e.leaseMu.Lock()
	defer e.leaseMu.Unlock()
	if e.evicted.Load() {
		return false
	}
	e.refs.Add(1)
	e.touch()
	return true
}

// Release 归还连接
func (e *mcpPoolEntry) Release() { e.refs.Add(-1) }

// tryMarkEvicted 尝试把条目摘出连接池。
// 仅在「无借用者」时成功：一旦成功，后续 Acquire 都会失败，sweep 即可安全关闭连接。
func (e *mcpPoolEntry) tryMarkEvicted() bool {
	e.leaseMu.Lock()
	defer e.leaseMu.Unlock()
	if e.refs.Load() != 0 || e.evicted.Load() {
		return false
	}
	e.evicted.Store(true)
	return true
}

// NewMCPClientPool 创建 MCP 客户端连接池
func NewMCPClientPool() *MCPClientPool {
	return &MCPClientPool{
		clients: make(map[string]*mcpPoolEntry),
	}
}

// GetOrCreate 获取或创建 MCP 客户端
// 同一配置（transport/command/args/env/url/headers）只会创建一个 client 实例
func (p *MCPClientPool) GetOrCreate(ctx context.Context, cfg *tool.MCPProviderConfig) (*client.Client, *mcpPoolEntry, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("MCP 配置不能为空")
	}
	if err := validateMCPConfig(cfg); err != nil {
		return nil, nil, fmt.Errorf("MCP 配置校验失败: %w", err)
	}
	if p.closed.Load() {
		return nil, nil, fmt.Errorf("MCP 连接池已关闭，不再接受新的连接")
	}

	hash := configHash(cfg)

	p.mu.Lock()
	// 二次复查必须在持有 p.mu 时进行：仅靠上面的锁外检查，
	// Close() 可能恰好在其后抢到锁完成清理，而本 goroutine 随后又往已关闭的池里
	// 插入新条目并拉起子进程（要等 30 分钟空闲回收才会被清掉）。
	if p.closed.Load() {
		p.mu.Unlock()
		return nil, nil, fmt.Errorf("MCP 连接池已关闭，不再接受新的连接")
	}
	entry, ok := p.clients[hash]
	if !ok {
		entry = &mcpPoolEntry{initDone: make(chan struct{})}
		entry.touch()
		p.clients[hash] = entry
		// 在锁内启动初始化 goroutine，避免其他 goroutine 在锁外重复创建
		go p.initClient(entry, cfg)
	} else {
		entry.touch()
	}
	p.mu.Unlock()

	// 等待初始化完成
	select {
	case <-entry.initDone:
	case <-ctx.Done():
		return nil, entry, ctx.Err()
	}

	if entry.initErr != nil {
		// 负缓存期内直接返回错误，避免故障风暴下反复重建条目和拉起子进程
		if time.Since(entry.failedAt) < failureTTL {
			return nil, entry, fmt.Errorf("MCP 客户端初始化失败: %w", entry.initErr)
		}
		// 超出负缓存期：摘除旧条目让下次请求重新尝试。
		// 必须按 entry 身份比对再删——这段时间内可能有其他 goroutine 已用同一 hash
		// 建好了新条目，按 hash 无脑删会把新条目连带删掉，导致连接泄漏 + 重复初始化。
		p.removeIfSame(hash, entry)
		return nil, entry, fmt.Errorf("MCP 客户端初始化失败: %w", entry.initErr)
	}

	// 顺带清理空闲连接：配置变更产生的旧条目会自然过期被回收
	p.sweep()

	return entry.client, entry, nil
}

// removeIfSame 仅当池中当前条目就是给定条目时才摘除
func (p *MCPClientPool) removeIfSame(hash string, entry *mcpPoolEntry) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cur, ok := p.clients[hash]; ok && cur == entry {
		delete(p.clients, hash)
		entry.evicted.Store(true)
		return true
	}
	return false
}

// sweep 回收空闲超时的连接，并在超过容量上限时淘汰最久未使用的连接。
// 关键点：只回收「已摘出池子且无借用者」的条目，避免在途 tools/call 被掐断。
// 关闭动作放在锁外执行，避免 Close（终止 stdio 子进程）阻塞其它 goroutine 取连接。
func (p *MCPClientPool) sweep() {
	now := time.Now()

	p.mu.Lock()
	var stale []*mcpPoolEntry
	// 注意：本函数持有 p.mu，内部不得再调用会加 p.mu 的函数，只能内联操作。
	// 先通过 tryMarkEvicted 占位（只拿 leaseMu，不会与 p.mu 形成反向依赖），失败即跳过。
	for hash, e := range p.clients {
		if e.idleFor(now) <= maxIdle {
			continue
		}
		if !e.tryMarkEvicted() {
			continue
		}
		delete(p.clients, hash)
		stale = append(stale, e)
	}
	if over := len(p.clients) - maxEntries; over > 0 {
		type kv struct {
			hash string
			e    *mcpPoolEntry
		}
		candidates := make([]kv, 0, len(p.clients))
		for hash, e := range p.clients {
			if e.refs.Load() == 0 {
				candidates = append(candidates, kv{hash, e})
			}
		}
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].e.idleFor(now) > candidates[j].e.idleFor(now)
		})
		for i := 0; i < over && i < len(candidates); i++ {
			if !candidates[i].e.tryMarkEvicted() {
				continue
			}
			delete(p.clients, candidates[i].hash)
			stale = append(stale, candidates[i].e)
		}
	}
	p.mu.Unlock()

	for _, e := range stale {
		p.closeEntry(e)
	}
}

// closeEntry 关闭并释放单个连接条目
func (p *MCPClientPool) closeEntry(e *mcpPoolEntry) {
	<-e.initDone // 等待初始化结束，避免在初始化中途关闭
	if e.client != nil {
		if err := e.client.Close(); err != nil {
			logger.Warnf("[MCPClientPool] 回收 MCP 客户端失败: %v", err)
			return
		}
		logger.Infof("[MCPClientPool] 已回收空闲 MCP 客户端")
	}
}

// initClient 初始化 MCP 客户端（在独立 goroutine 中执行）
// 结果通过 channel 回传，保证 entry 字段只由本 goroutine 写入，
// 等待方统一通过 initDone 同步，避免数据竞争。
func (p *MCPClientPool) initClient(entry *mcpPoolEntry, cfg *tool.MCPProviderConfig) {
	defer close(entry.initDone)

	resCh := make(chan initResult, 1)
	go func() {
		c, err := p.doInit(cfg)
		resCh <- initResult{client: c, err: err}
	}()

	select {
	case res := <-resCh:
		entry.client = res.client
		entry.initErr = res.err
	case <-time.After(initHardTimeout):
		// doInit 仍在跑（通常是 stdio 子进程卡住），不再等待它，
		// 否则等待方会一直挂在 initDone 上。initDone 仍会按时关闭，Close/sweep 得以有界返回。
		entry.initErr = fmt.Errorf("MCP 客户端初始化耗时超过 %v 未返回", initHardTimeout)
		logger.Warnf("[MCPClientPool] MCP 初始化超时，可能存在僵尸子进程: transport=%s, command=%s",
			cfg.Transport, cfg.Command)
		// 兜底清理：doInit 稍后才成功返回时，这个 client 没有任何 entry 接管，
		// 不显式关闭就会一直泄漏（含 stdio 子进程），因此在这里补收并关闭。
		go func() {
			if res := <-resCh; res.client != nil {
				logger.Warnf("[MCPClientPool] 关闭初始化超时后迟到的 MCP 客户端: command=%s", cfg.Command)
				_ = res.client.Close()
			}
		}()
	}

	if entry.initErr != nil {
		entry.failedAt = time.Now()
		return
	}
	logger.Infof("[MCPClientPool] MCP 客户端初始化成功: transport=%s, command=%s", cfg.Transport, cfg.Command)
}

// doInit 真正创建并握手 MCP 客户端，任何失败路径都要保证已创建的资源被清理
func (p *MCPClientPool) doInit(cfg *tool.MCPProviderConfig) (*client.Client, error) {
	timeout := clampTimeoutSeconds(cfg.Timeout)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	var c *client.Client
	var err error
	switch cfg.Transport {
	case "stdio":
		// client.NewStdioMCPClient 内部会自动调用 Start（启动子进程），不受 ctx 约束
		envSlice := make([]string, 0, len(cfg.Env))
		for k, v := range cfg.Env {
			envSlice = append(envSlice, k+"="+v)
		}
		if c, err = client.NewStdioMCPClient(cfg.Command, envSlice, cfg.Args...); err != nil {
			return nil, fmt.Errorf("创建 stdio MCP 客户端失败: %w", err)
		}
	case "sse":
		if c, err = client.NewSSEMCPClient(cfg.URL); err != nil {
			return nil, fmt.Errorf("创建 SSE MCP 客户端失败: %w", err)
		}
		if err = c.Start(ctx); err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("启动 SSE MCP 客户端失败: %w", err)
		}
	case "http":
		if c, err = client.NewStreamableHttpClient(cfg.URL); err != nil {
			return nil, fmt.Errorf("创建 HTTP MCP 客户端失败: %w", err)
		}
		if err = c.Start(ctx); err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("启动 HTTP MCP 客户端失败: %w", err)
		}
	default:
		return nil, fmt.Errorf("不支持的 transport 类型: %s", cfg.Transport)
	}

	// Initialize 握手（MCP 协议要求）
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "solvify-agent",
		Version: "1.0.0",
	}
	if _, err = c.Initialize(ctx, initReq); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("MCP Initialize 握手失败: %w", err)
	}
	return c, nil
}

// Close 关闭所有 MCP 客户端连接，之后连接池不再接受新连接
func (p *MCPClientPool) Close() error {
	p.closed.Store(true)

	p.mu.Lock()
	entries := make([]*mcpPoolEntry, 0, len(p.clients))
	for hash, entry := range p.clients {
		entries = append(entries, entry)
		delete(p.clients, hash)
		entry.evicted.Store(true)
	}
	p.mu.Unlock()

	var lastErr error
	for _, entry := range entries {
		<-entry.initDone // 等待初始化完成（如果还在进行中）
		if entry.client != nil {
			if err := entry.client.Close(); err != nil {
				logger.Warnf("[MCPClientPool] 关闭 MCP 客户端失败: %v", err)
				lastErr = err
			}
		}
	}
	return lastErr
}

// validateMCPConfig 校验 MCP 配置
func validateMCPConfig(cfg *tool.MCPProviderConfig) error {
	if cfg.Transport == "" {
		return fmt.Errorf("transport 不能为空")
	}
	switch cfg.Transport {
	case "stdio":
		if cfg.Command == "" {
			return fmt.Errorf("stdio 模式下 command 不能为空")
		}
	case "sse", "http":
		if cfg.URL == "" {
			return fmt.Errorf("%s 模式下 url 不能为空", cfg.Transport)
		}
	default:
		return fmt.Errorf("不支持的 transport 类型: %s（仅支持 stdio/sse/http）", cfg.Transport)
	}
	return nil
}

// configHash 计算 MCP 配置的 hash 值
// 只算连接相关字段（transport/command/args/env/url/headers），不算 timeout
func configHash(cfg *tool.MCPProviderConfig) string {
	key := struct {
		Transport string            `json:"transport"`
		Command   string            `json:"command"`
		Args      []string          `json:"args"`
		Env       map[string]string `json:"env"`
		URL       string            `json:"url"`
		Headers   map[string]string `json:"headers"`
	}{
		Transport: cfg.Transport,
		Command:   cfg.Command,
		Args:      cfg.Args,
		Env:       cfg.Env,
		URL:       cfg.URL,
		Headers:   cfg.Headers,
	}
	data, _ := json.Marshal(key)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
