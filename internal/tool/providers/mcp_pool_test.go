package providers

import (
	"context"
	"sync"
	"testing"
	"time"

	"solvify-agent/internal/tool"
)

// closedCh 返回一个已关闭的 channel，用于构造「初始化已完成」的条目
func closedCh() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// TestMCPClientPool_SweepSkipsBorrowedEntry 验证：借用中的连接不会被空闲回收
//
// 背景：关闭 stdio 连接会直接终止子进程，正在执行的 tools/call 会立刻失败。
// sweep 必须尊重借用计数，否则长耗时 MCP 调用会在半途被掐断。
func TestMCPClientPool_SweepSkipsBorrowedEntry(t *testing.T) {
	pool := NewMCPClientPool()
	const hash = "hash-borrowed"
	e := &mcpPoolEntry{initDone: closedCh()}
	// 伪造成已空闲远超 maxIdle
	e.lastUsed.Store(time.Now().Add(-2 * maxIdle).UnixNano())
	pool.clients[hash] = e

	if !e.Acquire() {
		t.Fatal("Acquire() 应当成功")
	}

	pool.sweep()
	if _, ok := pool.clients[hash]; !ok {
		t.Fatal("借用中的条目被 sweep 回收了，会中断在途 MCP 调用")
	}

	e.Release()
	// Acquire() 会刷新 lastUsed（借用中不算空闲），这里重新把空闲时长拨回超时区间
	e.lastUsed.Store(time.Now().Add(-2 * maxIdle).UnixNano())
	pool.sweep()
	if _, ok := pool.clients[hash]; ok {
		t.Fatal("归还且空闲超时的条目应当被回收")
	}
}

// TestMCPClientPool_AcquireAfterEvicted 验证：已回收的条目不再接受新的借用
func TestMCPClientPool_AcquireAfterEvicted(t *testing.T) {
	e := &mcpPoolEntry{initDone: closedCh()}
	e.touch()

	if ok := e.Acquire(); !ok {
		t.Fatal("首次借用应当成功")
	}
	e.Release()

	e.evicted.Store(true)
	if ok := e.Acquire(); ok {
		t.Fatal("条目已摘出连接池，不应再接受借用（其连接可能已被关闭）")
	}
	if refs := e.refs.Load(); refs != 0 {
		t.Fatalf("借用失败时不应残留计数: refs=%d", refs)
	}
}

// TestMCPClientPool_InitFailureNegativeCache 验证：初始化失败后不会反复重建条目
//
// 背景：服务端持续故障时，若每次请求都销毁旧条目再新建，会反复拉起子进程
// 并形成惊群；失败后应在负缓存期内直接返回错误。
func TestMCPClientPool_InitFailureNegativeCache(t *testing.T) {
	pool := NewMCPClientPool()
	cfg := &tool.MCPProviderConfig{
		Transport: "stdio",
		Command:   "solvify-definitely-not-exist-command",
		Timeout:   1,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, _, err := pool.GetOrCreate(ctx, cfg); err == nil {
		t.Fatal("预期初始化失败，实际成功")
	}

	pool.mu.Lock()
	after := len(pool.clients)
	entryHash := ""
	for h := range pool.clients {
		entryHash = h
		break
	}
	pool.mu.Unlock()

	if after != 1 {
		t.Fatalf("首次失败后池中应保留 1 个负缓存条目，实际 %d", after)
	}
	if entryHash != configHash(cfg) {
		t.Fatalf("负缓存条目 hash 不匹配: got=%s want=%s", entryHash, configHash(cfg))
	}

	// 负缓存期内重复调用：应命中同一条目，不再重建
	for i := 0; i < 3; i++ {
		if _, _, err := pool.GetOrCreate(ctx, cfg); err == nil {
			t.Fatalf("第 %d 次调用预期仍失败", i+2)
		}
	}
	pool.mu.Lock()
	got := len(pool.clients)
	_, same := pool.clients[entryHash]
	pool.mu.Unlock()
	if got != 1 || !same {
		t.Fatalf("负缓存期内不应重建条目: size=%d sameEntry=%v", got, same)
	}
}

// TestMCPClientPool_ClosedRejectsNew 验证：连接池关闭后不再接受新连接，避免「复活」子进程
func TestMCPClientPool_ClosedRejectsNew(t *testing.T) {
	pool := NewMCPClientPool()
	if err := pool.Close(); err != nil {
		t.Fatalf("Close() 返回错误: %v", err)
	}
	cfg := &tool.MCPProviderConfig{Transport: "stdio", Command: "node"}
	if _, _, err := pool.GetOrCreate(context.Background(), cfg); err == nil {
		t.Fatal("连接池关闭后应拒绝新建连接")
	}
}

// TestMCPClientPool_AcquireSweepRace 借用方与回收方的并发压力测试
//
// 语义保证：一旦 Acquire 成功，条目在 Release 之前不会被 sweep 摘出并关闭，
// 因此在途的 tools/call 不会因为连接被回收而中断。配合 -race 同时验证无数据竞争。
func TestMCPClientPool_AcquireSweepRace(t *testing.T) {
	pool := NewMCPClientPool()
	const hash = "hash-race"
	e := &mcpPoolEntry{initDone: closedCh()}
	e.touch()
	pool.clients[hash] = e

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ { // 借用方
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				if e.Acquire() {
					if e.evicted.Load() {
						t.Error("借用期间条目被标记为已淘汰，在途调用会被掐断")
					}
					e.Release()
				}
				// 周期性把空闲时长拨回超时区间，制造回收压力
				e.lastUsed.Store(time.Now().Add(-2 * maxIdle).UnixNano())
			}
		}()
	}
	for i := 0; i < 2; i++ { // 回收方
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 150; j++ {
				pool.sweep()
			}
		}()
	}
	wg.Wait()
}

// TestMCPClientPool_ConcurrentGetOrCreate 验证并发取用同一配置时的数据安全（-race）
func TestMCPClientPool_ConcurrentGetOrCreate(t *testing.T) {
	pool := NewMCPClientPool()
	cfg := &tool.MCPProviderConfig{
		Transport: "stdio",
		Command:   "solvify-definitely-not-exist-command",
		Timeout:   1,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	done := make(chan struct{})
	for i := 0; i < 16; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_, _, _ = pool.GetOrCreate(ctx, cfg)
			pool.sweep()
		}()
	}
	for i := 0; i < 16; i++ {
		<-done
	}
}
