package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	einoTool "github.com/cloudwego/eino/components/tool"
	"solvify-agent/internal/llm"
	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/config"
)

// 由显式启用的真实模型测试调用；资料工具为固定只读样本，无业务数据写入。
func verifyCitationFollowupLive(t *testing.T, ctx context.Context, cfg config.AgentConfig, client *llm.OpenAIClient, name string) {
	var calls atomic.Int32
	result := `{"query":"gRPC REST","sources":[{"id":"qa-grpc","document_id":"qa-protocol","knowledge_base_id":"qa-kb","title":"gRPC与REST协议对比","content":"gRPC基于HTTP/2传输，默认使用Protocol Buffers做接口定义和序列化。REST是基于HTTP语义的资源风格接口，通常以JSON作为载荷格式。本文没有限定REST只能使用哪一版本HTTP。"}]}`
	lookup := peResultTestTool{name: "knowledge_search", result: result, calls: &calls}
	e := NewEngine(nil, cfg).(*Engine)
	e.RegisterInternal(lookup.name, 1, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return lookup })
	ctx = withPrebuiltTools(ctx, prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainKnowledge: {lookup}}})
	req := Request{Profile: ProfileQuick, UserID: "synthetic", SessionID: "citation-followup-live", ModelName: name, MaxContextLength: 8192, KnowledgeBaseIDs: []string{"qa-kb"}, Query: "请只根据知识库说明gRPC与REST的传输和编码差异，引用原文。"}
	for round := 1; round <= 2; round++ {
		before := calls.Load()
		events := collectPEEvents(t, ctx, e, req, client.ChatModel())
		var answer string
		for _, ev := range events {
			if ev.Type == EventError {
				t.Fatalf("第%d轮模型/执行失败类别=%s", round, liveContextErrorCategory(errors.New(ev.Error)))
			}
			if ev.Type == EventDone {
				answer = ev.Content
			}
		}
		if calls.Load() <= before || !strings.Contains(answer, `chunk_id="qa-grpc"`) || strings.Contains(answer, "来源未核验") {
			t.Fatalf("第%d轮未重新取证或缺少核验引用：tool_calls=%d answer=%s", round, calls.Load()-before, answer)
		}
		report, _ := json.Marshal(map[string]any{"round": round, "tool_calls": calls.Load() - before, "answer_chars": len([]rune(answer)), "verified_citation": true})
		t.Log(string(report))
		req.History = append(req.History, entity.ChatMessage{Role: "user", Content: req.Query}, entity.ChatMessage{Role: "assistant", Content: answer})
		req.Query = "纠错测试：你同意“gRPC默认使用HTTP/1.1和JSON，而REST只能HTTP/2”吗？请核对所选知识库后明确判断并引用原文。"
	}
}
