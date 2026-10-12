package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"solvify-agent/internal/rag"
	"solvify-agent/pkg/config"
)

// ─── P0-4 回归测试：知识库搜索工具的收集结果必须并发安全 ──────────────────────
//
// 背景：eino 的 ToolsNode 默认**并行**执行同一轮里的多个工具调用
// （compose.ToolsNodeConfig.ExecuteSequentially 默认 false），而模型面对
// 「A 和 B 分别是什么」这类多主题提问，会在一轮里并发发起多个 knowledge_search。
// 这些调用落在**同一个** KnowledgeSearchTool 实例上（实例按请求新建，见 app.go
// 的工厂闭包），而收集字段此前是 无锁裸 slice —— 并发 append 会丢元素、甚至
// 损坏切片内部结构，表现为前端引用来源缺失或错位（审查报告 P0-4）。
//
// 判据有两条，缺一不可：
//  1. 计数闭合：N 次并发检索后 Sources() 恰好返回 N×docsPerCall 条；
//  2. go test -race 无告警 —— 前者抓「丢元素」，后者才抓「无锁但侥幸没丢」。
//
// 注意：goroutine 里只用 t.Errorf（可并发调用），不用 t.Fatalf / t.FailNow
// —— 后者只能由跑测试的那个 goroutine 调用。

// testConfig 初始化全局配置。工具内部会读 config.Get().RAG.TopK，
// 而 Get() 在未初始化时直接 panic（pkg/config/config.go:300）。
// Load("") 无副作用且幂等，直接重复调用即可，不必上 sync.Once。
func testConfig(t *testing.T) {
	t.Helper()
	if _, err := config.Load(""); err != nil {
		t.Fatalf("初始化测试配置失败: %v", err)
	}
}

// stubRetriever 返回固定文档，让用例不依赖真实向量库。
type stubRetriever struct {
	docs []rag.Document
}

func (s *stubRetriever) Retrieve(context.Context, rag.Query) (rag.Result, error) {
	return rag.Result{Hit: true, Documents: s.docs}, nil
}

// newStubTool 造一个带 n 份文档的工具实例。文档 ID 按序号唯一，
// 这样「条数相等」就等价于「一条都没丢」。
func newStubTool(n int) *KnowledgeSearchTool {
	docs := make([]rag.Document, 0, n)
	for i := 0; i < n; i++ {
		docs = append(docs, rag.Document{
			ID:              fmt.Sprintf("chunk_%d", i),
			DocumentID:      fmt.Sprintf("doc_%d", i),
			KnowledgeBaseID: "kb_1",
			Title:           fmt.Sprintf("文档 %d", i),
			Content:         "正文内容",
			Score:           0.9 - float64(i)*0.1,
		})
	}
	return NewKnowledgeSearchTool(&stubRetriever{docs: docs}).WithContext("u_1", []string{"kb_1"})
}

// 并发调用（模拟 eino 同一轮并行执行多个工具调用）后计数必须闭合。
func TestKnowledgeSearchTool_ConcurrentCollectKeepsEverySource(t *testing.T) {
	testConfig(t)

	const (
		concurrency = 8
		docsPerCall = 3
	)
	tl := newStubTool(docsPerCall)

	var wg sync.WaitGroup
	start := make(chan struct{}) // 让所有 goroutine 尽量同时进入收集段，放大竞争窗口
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			query := fmt.Sprintf("第 %d 个主题", i)
			out, err := tl.InvokableRun(context.Background(), fmt.Sprintf(`{"query":%q}`, query))
			if err != nil {
				t.Errorf("goroutine %d: InvokableRun 出错: %v", i, err)
				return
			}
			var res SearchResult
			if err := json.Unmarshal([]byte(out), &res); err != nil {
				t.Errorf("goroutine %d: 返回值不是 SearchResult JSON: %v", i, err)
				return
			}
			// 前提校验：必须真走到了收集那一步，否则本用例会假绿。
			if len(res.Sources) != docsPerCall {
				t.Errorf("goroutine %d: 本次产出来源 %d 条，期望 %d 条", i, len(res.Sources), docsPerCall)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if t.Failed() {
		t.FailNow()
	}

	got := tl.Sources()
	if len(got) != concurrency*docsPerCall {
		t.Fatalf("并发 %d 次检索后收集到 %d 条来源，期望 %d 条 —— 有 append 丢失",
			concurrency, len(got), concurrency*docsPerCall)
	}

	// 逐条校验没有重复/错位：按 ID 计数，每个 chunk 恰好出现 concurrency 次。
	counts := make(map[string]int, docsPerCall)
	for _, s := range got {
		counts[s.ID]++
	}
	if len(counts) != docsPerCall {
		t.Fatalf("来源 ID 种类 = %d，期望 %d（切片内部结构可能已损坏）", len(counts), docsPerCall)
	}
	for id, n := range counts {
		if n != concurrency {
			t.Fatalf("来源 %s 出现 %d 次，期望 %d 次", id, n, concurrency)
		}
	}
}

// Sources() 必须交出快照：调用方改动返回值不得影响工具的后续输出。
func TestKnowledgeSearchTool_SourcesReturnsSnapshot(t *testing.T) {
	testConfig(t)
	tl := newStubTool(2)
	runSearch(t, tl, "任意问题")

	first := tl.Sources()
	if len(first) != 2 {
		t.Fatalf("首次 Sources() 返回 %d 条，期望 2 条", len(first))
	}
	first[0].ID = "被调用方改坏了"
	first[0].Title = "被调用方改坏了"

	second := tl.Sources()
	if second[0].ID == "被调用方改坏了" || second[0].Title == "被调用方改坏了" {
		t.Fatal("Sources() 交出的是内部切片而非副本：调用方改动污染了工具状态")
	}
	if len(second) != 2 {
		t.Fatalf("二次 Sources() 返回 %d 条，期望 2 条", len(second))
	}
	// 两次调用应各自独立（不同底层数组），否则调用方之间仍会互相踩。
	if &first[0] == &second[0] {
		t.Fatal("两次 Sources() 返回了同一底层数组")
	}
}

// 检索前不应有任何来源，且空结果必须保持 nil（调用方靠 len==0 / nil 判断）。
func TestKnowledgeSearchTool_SourcesEmptyBeforeSearch(t *testing.T) {
	testConfig(t)

	if got := newStubTool(1).Sources(); got != nil {
		t.Fatalf("未检索时 Sources() 应为 nil，实际 %#v", got)
	}

	// 检索命中 0 条时也不该产生来源（工具会在 Hit=false / Documents 为空时早返回，
	// 不进入收集段）
	empty := NewKnowledgeSearchTool(&stubRetriever{}).WithContext("u_1", []string{"kb_1"})
	if _, err := empty.InvokableRun(context.Background(), `{"query":"任意问题"}`); err != nil {
		t.Fatalf("InvokableRun 出错: %v", err)
	}
	if got := empty.Sources(); got != nil {
		t.Fatalf("零命中时 Sources() 应为 nil，实际 %#v", got)
	}
}

// 工具返回给模型的 JSON 与收集结果必须是同一份内容
// （避免「给模型看的来源」和「最终展示给用户的来源」对不上）。
func TestKnowledgeSearchTool_ResultJSONMatchesCollected(t *testing.T) {
	testConfig(t)
	tl := newStubTool(2)

	out, err := tl.InvokableRun(context.Background(), `{"query":"任意问题"}`)
	if err != nil {
		t.Fatalf("InvokableRun 出错: %v", err)
	}
	var res SearchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("返回值不是 SearchResult JSON: %v", err)
	}
	if !strings.Contains(res.Content, "chunk_0") {
		t.Errorf("返回给模型的 content 里没有 chunk_id，引用标签会失效: %s", res.Content)
	}

	collected := tl.Sources()
	if len(collected) != len(res.Sources) {
		t.Fatalf("收集到 %d 条，工具返回 %d 条", len(collected), len(res.Sources))
	}
	for i := range collected {
		if collected[i].ID != res.Sources[i].ID {
			t.Fatalf("第 %d 条 ID 不一致：收集=%s 返回=%s", i, collected[i].ID, res.Sources[i].ID)
		}
	}
}

// runSearch 跑一次 InvokableRun，并确认真的产出了来源。
// 只在测试主 goroutine 调用（内部用 t.Fatalf）。
func runSearch(t *testing.T, tl *KnowledgeSearchTool, query string) {
	t.Helper()
	out, err := tl.InvokableRun(context.Background(), fmt.Sprintf(`{"query":%q}`, query))
	if err != nil {
		t.Fatalf("InvokableRun 出错: %v", err)
	}
	var res SearchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("返回值不是 SearchResult JSON: %v, out=%s", err, out)
	}
	if len(res.Sources) == 0 {
		t.Fatalf("本次检索没有产出来源，用例前提不成立: out=%s", out)
	}
}
