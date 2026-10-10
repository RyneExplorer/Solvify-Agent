package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestDangerousToolResumeRequiresValidApproval(t *testing.T) {
	for _, tc := range []struct {
		name string
		data any
	}{
		{"空值", nil},
		{"空指针", (*ApprovalResult)(nil)},
		{"错误类型", "approve"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := deepTestContext(t)
			var calls atomic.Int32
			ctx = withPrebuiltTools(ctx, prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{
				DomainExternal: {deepApprovalTool{calls: &calls}},
			}})
			e := &Engine{}
			m := &deepTestModel{respond: func(_ context.Context, msgs []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("", []schema.ToolCall{{ID: "write", Type: "function", Function: schema.FunctionCall{Name: "write_file", Arguments: "{}"}}}), nil
			}}
			req := Request{UserID: "u", SessionID: "s", Query: "写文件"}
			events := collectPEEvents(t, ctx, e, req, m)
			var interrupt Event
			for _, ev := range events {
				if ev.Type == EventInterrupt {
					interrupt = ev
				}
			}
			if interrupt.InterruptID == "" {
				t.Fatalf("没有触发审批: %+v", events)
			}
			req.CheckpointID = interrupt.CheckpointID
			req.ResumeData = map[string]any{interrupt.InterruptID: tc.data}
			events = collectPEEvents(t, ctx, e, req, m)
			failed := false
			for _, ev := range events {
				if ev.Type == EventError {
					failed = strings.Contains(ev.Error, "缺少有效审批结果") || strings.Contains(ev.Error, "resume")
				}
			}
			if !failed || calls.Load() != 0 {
				t.Fatalf("无有效审批必须失败且不得执行: failed=%v calls=%d events=%+v", failed, calls.Load(), events)
			}
		})
	}
}
