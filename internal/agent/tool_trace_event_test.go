package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestRunnerToolEventsCarryMatchingIdentity(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "完整消息", true: "流式消息"}[streaming], func(t *testing.T) {
			e := &Engine{}
			ctx := context.Background()
			events := make(chan Event, 4)
			var answer strings.Builder
			call := schema.AssistantMessage("", []schema.ToolCall{{ID: "trace-call", Function: schema.FunctionCall{Name: "grep_chunks", Arguments: `{}`}}})
			result := &schema.Message{Role: schema.Tool, ToolCallID: "trace-call", Content: `{"success":true}`}
			for _, msg := range []*schema.Message{call, result} {
				toolName := ""
				if msg.Role == schema.Tool {
					toolName = "grep_chunks"
				}
				if streaming {
					mv := &adk.TypedMessageVariant[*schema.Message]{Role: msg.Role, ToolName: toolName, MessageStream: schema.StreamReaderFromArray([]*schema.Message{msg})}
					if err := e.consumeMessageStream(ctx, mv, nil, &answer, events); err != nil {
						t.Fatal(err)
					}
				} else {
					e.handleMessage(ctx, msg, msg.Role, toolName, nil, &answer, events)
				}
			}
			if len(events) != 2 {
				t.Fatalf("事件数量错误: %d", len(events))
			}
			for _, typ := range []string{EventToolCall, EventToolResult} {
				ev := <-events
				if ev.Type != typ || ev.ToolCallID != "trace-call" || ev.ToolName != "grep_chunks" {
					t.Fatalf("调用与结果无法匹配: %+v", ev)
				}
			}
		})
	}
}
