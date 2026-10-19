package agent

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

const citationKBResult = `{"query":"select","sources":[{"id":"chunk-select","document_id":"doc-select","knowledge_base_id":"kb-go","title":"Golang并发之select详解","content":"select语义"}]}`

func TestCitationRegistersOnlySuccessfulStructuredSources(t *testing.T) {
	for _, tc := range []struct {
		name, tool, raw string
		want            int
	}{
		{"知识检索", "knowledge_search", citationKBResult, 1},
		{"关键词检索", "grep_chunks", `{"success":true,"data":[{"chunk_id":"c","document_id":"d","knowledge_base_id":"k","title":"文档"}]}`, 1},
		{"结构化网页", "mcp_search", `{"results":[{"title":"Go规范","url":"https://go.dev/ref/spec?a=1&b=2"}]}`, 1},
		{"MCP结构化正文", "mcp_fetch", `{"structuredContent":{"contents":[{"title":"Go规范","url":"https://go.dev/ref/spec"}]}}`, 1},
		{"MCP文本JSON", "mcp_search", `{"content":[{"type":"text","text":"{\"results\":[{\"title\":\"Go规范\",\"url\":\"https://go.dev/ref/spec\"}]}"}]}`, 1},
		{"普通正文不能成为来源", "mcp_search", `{"content":[{"type":"text","text":"Title: Go规范\nURL: https://go.dev/ref/spec"}]}`, 0},
		{"业务数据不能成为来源", "mcp_search", `{"data":{"title":"伪造","url":"https://example.com"}}`, 0},
		{"未声明工具不能成为来源", "custom", `{"title":"伪造","url":"https://example.com"}`, 0},
		{"失败不能登记", "knowledge_search", `{"success":false,"sources":[{"id":"c","document_id":"d","title":"错误结果"}]}`, 0},
		{"MCP失败不能登记", "mcp_search", `{"isError":true,"results":[{"title":"错误","url":"https://example.com"}]}`, 0},
		{"限流不能登记", "mcp_search", `{"_meta":{"ai.exa/rateLimited":true},"results":[{"title":"错误","url":"https://example.com"}]}`, 0},
		{"嵌套失败不能登记", "mcp_search", `{"content":[{"type":"text","text":"{\"success\":false,\"results\":[{\"title\":\"错误\",\"url\":\"https://example.com\"}]}"}]}`, 0},
		{"危险URL不能登记", "mcp_search", `{"results":[{"title":"脚本","url":"javascript:alert(1)"},{"title":"凭据","url":"https://user:secret@example.com"}]}`, 0},
		{"无分块不能伪造知识引用", "grep_chunks", `{"success":true,"data":[{"document_id":"d","title":"文档"}]}`, 0},
		{"损坏JSON", "knowledge_search", `{"sources":`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{citations: &citationRegistry{}}
			out := citationToolResult(req, tc.tool, tc.raw)
			if got := len(req.citations.snapshot()); got != tc.want {
				t.Fatalf("登记数=%d want=%d", got, tc.want)
			}
			if tc.want == 0 && out != tc.raw {
				t.Fatal("无有效来源时修改了结果")
			}
			if tc.want > 0 {
				var result struct {
					CitationSources []citationSource `json:"citation_sources"`
				}
				if json.Unmarshal([]byte(out), &result) != nil || len(result.CitationSources) != tc.want {
					t.Fatal("模型未收到可选来源标识", out)
				}
			}
		})
	}
}

func TestCitationCanonicalMetadataAndRunIsolation(t *testing.T) {
	r := &citationRegistry{}
	kb := r.toolSources("knowledge_search", citationKBResult)[0]
	web, _ := r.add(citationSource{Kind: "web", Title: `Go "规范" & select`, URL: "https://go.dev/ref/spec?a=1&b=2"})
	for _, tc := range []struct{ input, want string }{
		{`<cite id="` + kb.ID + `" />`, citationTag(kb)},
		{`<CITE\tid='` + kb.ID + `' />`, citationTag(kb)},
		{`<kb doc="错误标题" chunk_id="chunk-select" />`, citationTag(kb)},
		{`<kb\ndoc="错误 > 标题" chunk_id="chunk-select" />`, citationTag(kb)},
		{`<cite id="chunk-select" />`, citationTag(kb)},
		{`<web url="https://go.dev/ref/spec?a=1&amp;b=2" title="Golang并发之select详解" />`, citationTag(web)},
		{`[Golang并发之select详解](https://go.dev/ref/spec?a=1&b=2)`, citationTag(web)},
		{`[错误标题](<https://go.dev/ref/spec?a=1&b=2> "错误tooltip")`, citationTag(web)},
		{`<cite id="unknown" />`, "（来源未核验）"},
		{`<web url="https://unseen.example.com" title="伪造" />`, "（来源未核验）"},
		{`[伪造](https://unseen.example.com)`, "伪造（链接未核验）"},
		{`[目录](#select)`, `[目录](#select)`},
	} {
		input := strings.NewReplacer(`\t`, "\t", `\n`, "\n").Replace(tc.input)
		if got := normalizeCitations(r, input); got != tc.want {
			t.Errorf("%q => %q want %q", input, got, tc.want)
		}
	}
	if got := normalizeCitations(&citationRegistry{}, `<cite id="`+kb.ID+`" />`); got != "（来源未核验）" {
		t.Fatal("其他执行接受了未返回的历史来源", got)
	}
}

func TestCitationConcurrentSourcesAndAmbiguousChunks(t *testing.T) {
	r := &citationRegistry{}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.toolSources("knowledge_search", citationKBResult); _ = r.snapshot() }()
	}
	wg.Wait()
	if len(r.snapshot()) != 1 {
		t.Fatal("同一来源没有去重")
	}
	r.add(citationSource{Kind: "kb", Title: "另一文档", ChunkID: "chunk-select", DocumentID: "different", KnowledgeBaseID: "kb-go"})
	if normalizeCitations(r, `<cite id="chunk-select" />`) != "（来源未核验）" {
		t.Fatal("歧义分块不应随意匹配")
	}
}
