package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type peDocumentTool struct {
	calls *atomic.Int32
	user  string
	kb    []string
}

func (peDocumentTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "delete_document", Desc: "删除文档"}, nil
}

func (t peDocumentTool) InvokableRun(context.Context, string, ...einoTool.Option) (string, error) {
	if t.user != "u" || len(t.kb) != 1 || t.kb[0] != "original-kb" {
		return "", fmt.Errorf("恢复后的文档权限范围变化")
	}
	t.calls.Add(1)
	return `{"success":true,"message":"已执行"}`, nil
}

func TestPlanExecuteDocumentOperationWithMCPDisabledAndApproval(t *testing.T) {
	var calls, research atomic.Int32
	engine := deepTestEngine()
	engine.RegisterInternal("delete_document", 1, true, DomainDocMgmt, func(_ context.Context, user string, kb []string) einoTool.BaseTool {
		return peDocumentTool{calls: &calls, user: user, kb: kb}
	})
	model := peModel(`{"strategy":"sequential","tasks":[{"id":"d","agent":"document_operation","title":"删除指定文档"},{"id":"f","agent":"synthesis_review","title":"汇总","depends_on":["d"]}]}`, &research)
	base := model.respond
	model.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		if strings.Contains(messages[0].Content, "仅执行已挂载的文档管理工具") {
			for _, message := range messages {
				if message.Role == schema.Tool {
					return schema.AssistantMessage("操作完成", nil), nil
				}
			}
			return peCall("delete_document", map[string]string{"document_id": "doc"}), nil
		}
		return base(ctx, messages)
	}
	req := Request{Query: "删除指定文档 doc", UserID: "u", SessionID: "docs", MCPDisabled: true, KnowledgeBaseIDs: []string{"original-kb"}}
	events := collectPEEvents(t, context.Background(), engine, req, model)
	var interrupt Event
	for _, event := range events {
		if event.Type == EventError {
			t.Fatal(event.Error)
		}
		if event.Type == EventInterrupt {
			interrupt = event
		}
	}
	if interrupt.InterruptID == "" || calls.Load() != 0 {
		t.Fatalf("文档操作未等待审批: %+v calls=%d", events, calls.Load())
	}
	req.CheckpointID = interrupt.CheckpointID
	req.ResumeData = map[string]any{interrupt.InterruptID: &ApprovalResult{Approved: true}}
	req.KnowledgeBaseIDs = []string{"new-selection"}
	events = collectPEEvents(t, context.Background(), engine, req, model)
	done := false
	for _, event := range events {
		if event.Type == EventError {
			t.Fatal(event.Error)
		}
		done = done || event.Type == EventDone
	}
	if !done || calls.Load() != 1 {
		t.Fatalf("文档审批未完成: done=%t calls=%d events=%+v", done, calls.Load(), events)
	}
}
