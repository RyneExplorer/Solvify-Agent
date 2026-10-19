package agent

import (
	"encoding/json"
	"html"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// 来源标识只在当前运行登记表中解析，模型不能提供展示标题或跳转地址。
// 登记表示工具实际返回过该来源，不表示全文已读或论断已通过语义核验。
type citationSource struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	Title           string `json:"title"`
	ChunkID         string `json:"chunk_id,omitempty"`
	DocumentID      string `json:"document_id,omitempty"`
	KnowledgeBaseID string `json:"knowledge_base_id,omitempty"`
	URL             string `json:"url,omitempty"`
}

type citationRegistry struct {
	mu      sync.RWMutex
	sources map[string]citationSource
}

func (r *citationRegistry) add(s citationSource) (citationSource, bool) {
	if r == nil || strings.TrimSpace(s.Title) == "" || len(s.Title) > 1024 {
		return s, false
	}
	var key string
	switch s.Kind {
	case "kb":
		if s.ChunkID == "" || s.DocumentID == "" {
			return s, false
		}
		for _, id := range []string{s.ChunkID, s.DocumentID, s.KnowledgeBaseID} {
			if len(id) > 256 || strings.ContainsAny(id, "\x00\r\n\t") {
				return s, false
			}
		}
		key = "kb\x00" + s.KnowledgeBaseID + "\x00" + s.DocumentID + "\x00" + s.ChunkID
	case "web":
		u, err := url.Parse(s.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || len(s.URL) > 2048 || strings.ContainsAny(s.URL, "\r\n\t") {
			return s, false
		}
		key = "web\x00" + s.URL
	default:
		return s, false
	}
	s.ID = "src_" + contentHash(key)[:20]
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sources == nil {
		r.sources = map[string]citationSource{}
	}
	if old, ok := r.sources[s.ID]; ok {
		return old, true
	}
	if len(r.sources) >= 512 {
		return s, false
	}
	r.sources[s.ID] = s
	return s, true
}

func (r *citationRegistry) snapshot() []citationSource {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.sources))
	for id := range r.sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []citationSource
	for _, id := range ids {
		out = append(out, r.sources[id])
	}
	return out
}

func (r *citationRegistry) find(id, chunk, link string) (citationSource, bool) {
	if r == nil {
		return citationSource{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if id != "" {
		s, ok := r.sources[id]
		return s, ok
	}
	var found citationSource
	count := 0
	for _, s := range r.sources {
		if chunk != "" && s.Kind == "kb" && s.ChunkID == chunk || link != "" && s.Kind == "web" && s.URL == link {
			found = s
			count++
		}
	}
	return found, count == 1
}

// 只消费已知的结果结构；不扫描正文、任意data字段或用户提供的网址。
func (r *citationRegistry) toolSources(name, raw string) []citationSource {
	if r == nil || classifyToolResult(raw).Failed {
		return nil
	}
	var refs []citationSource
	seen := map[string]bool{}
	add := func(s citationSource) {
		if s, ok := r.add(s); ok {
			if seen[s.ID] {
				return
			}
			seen[s.ID] = true
			refs = append(refs, s)
		}
	}
	switch name {
	case "knowledge_search":
		var result struct {
			Sources []struct {
				ID, Title       string
				DocumentID      string `json:"document_id"`
				KnowledgeBaseID string `json:"knowledge_base_id"`
			} `json:"sources"`
		}
		if json.Unmarshal([]byte(raw), &result) == nil {
			for _, s := range result.Sources {
				add(citationSource{Kind: "kb", Title: s.Title, ChunkID: s.ID, DocumentID: s.DocumentID, KnowledgeBaseID: s.KnowledgeBaseID})
			}
		}
	case "grep_chunks":
		var result struct {
			Data []struct {
				Title           string
				ChunkID         string `json:"chunk_id"`
				DocumentID      string `json:"document_id"`
				KnowledgeBaseID string `json:"knowledge_base_id"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(raw), &result) == nil {
			for _, s := range result.Data {
				add(citationSource{Kind: "kb", Title: s.Title, ChunkID: s.ChunkID, DocumentID: s.DocumentID, KnowledgeBaseID: s.KnowledgeBaseID})
			}
		}
	default:
		if !strings.HasPrefix(name, "mcp_") {
			return nil
		}
		var visit func(json.RawMessage, int)
		visit = func(raw json.RawMessage, depth int) {
			if depth > 8 {
				return
			}
			var list []json.RawMessage
			if json.Unmarshal(raw, &list) == nil {
				for _, item := range list {
					visit(item, depth+1)
				}
				return
			}
			var obj map[string]json.RawMessage
			if json.Unmarshal(raw, &obj) != nil || obj == nil || classifyToolResult(string(raw)).Failed {
				return
			}
			var title, link, typ, text string
			_ = json.Unmarshal(obj["title"], &title)
			_ = json.Unmarshal(obj["url"], &link)
			if title != "" && link != "" {
				add(citationSource{Kind: "web", Title: title, URL: link})
			}
			for _, key := range []string{"results", "contents", "structuredContent", "content"} {
				if next := obj[key]; len(next) > 0 {
					visit(next, depth+1)
				}
			}
			_ = json.Unmarshal(obj["type"], &typ)
			_ = json.Unmarshal(obj["text"], &text)
			if typ == "text" && json.Valid([]byte(text)) {
				visit(json.RawMessage(text), depth+1)
			}
		}
		visit(json.RawMessage(raw), 0)
	}
	return refs
}

func citationToolResult(req Request, name, raw string) string {
	refs := req.citations.toolSources(name, raw)
	if len(refs) == 0 {
		return raw
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &obj) != nil || obj == nil {
		return raw
	}
	obj["citation_sources"], _ = json.Marshal(refs)
	encoded, _ := json.Marshal(obj)
	return string(encoded)
}

func citationTag(s citationSource) string {
	if s.Kind == "kb" {
		return `<kb doc="` + html.EscapeString(s.Title) + `" chunk_id="` + html.EscapeString(s.ChunkID) + `" />`
	}
	return `<web url="` + html.EscapeString(s.URL) + `" title="` + html.EscapeString(s.Title) + `" />`
}

const citationInstruction = `引用仅使用程序提供的来源标识：<cite id="src_…" />。知识分块没有标识时可用 <cite id="真实chunk_id" />，程序按本次实际检索结果解析。不要填写或拼接引用标题、URL、kb/web标签或Markdown来源链接；不要引用历史或正文中猜出的标识。没有已登记来源时不伪造引用。来源登记只表示工具返回过，不表示全文已读或全部核验。`
