package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"solvify-agent/internal/llm"
)

func TestContextBudgetBaseline(t *testing.T) {
	for _, limit := range []int{8192, 64000} {
		for _, profile := range []Profile{ProfileQuick, ProfileDeep} {
			instruction := strings.Repeat("遵守用户约束并提供证据。", 30)
			messages := []*schema.Message{schema.SystemMessage(instruction), schema.UserMessage("查找之前约定的方案"), schema.ToolMessage(strings.Repeat("工具证据内容。", 800), "sample")}
			tools := []*schema.ToolInfo{{Name: "search", Desc: "查找相关证据"}}
			before := llm.MessagesTokens(messages, "gpt-4o")
			guard := newBudgetGuard(Request{Profile: profile, ModelID: "baseline", ModelName: "gpt-4o", MaxContextLength: limit})
			_, state, err := guard.BeforeModelRewriteState(context.Background(), &adk.ChatModelAgentState{Messages: messages, ToolInfos: tools}, nil)
			if limit == 8192 {
				if !errors.Is(err, llm.ErrContextBudget) || state.Messages[2].Content != messages[2].Content {
					t.Fatal("无原文存储的证据超限应明确拒绝并保留原文", err)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("window=%d profile=%s client_output=4096 system=%d messages_before=%d tools=%d messages_after=%d", limit, profile, llm.MessageTokens(messages[0], "gpt-4o"), before, llm.ToolsTokens(tools, "gpt-4o"), llm.MessagesTokens(state.Messages, "gpt-4o"))
		}
	}
}
