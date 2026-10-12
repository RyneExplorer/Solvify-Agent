package agent

import (
	"context"
	"encoding/json"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"solvify-agent/internal/llm"
	"solvify-agent/pkg/config"
	"strings"
	"testing"
	"time"
)

// Offline comparison reports costs and protocol retention; it makes no model-quality claim.
func TestContextOfflineComparison(t *testing.T) {
	for _, profile := range []Profile{ProfileQuick, ProfileDeep} {
		t.Run(string(profile), func(t *testing.T) {
			e := deepTestEngine()
			e.cfg.RuntimeContext = config.DefaultRuntimeContextPolicy()
			req := Request{Profile: profile, ModelID: "offline-v1", ModelName: "gpt-4o", MaxContextLength: 8192, RunID: "sample-run", TaskID: "sample", AgentName: "knowledge_research"}
			messages := append([]*schema.Message{schema.UserMessage("比较证据，保留本轮问题与最新结果")}, testToolGroup("completed", strings.Repeat("已完成的证据 ", 1100))...)
			messages = append(messages, testToolGroup("current", strings.Repeat("当前证据 ", 450))...)
			calls := 0
			cm := &deepTestModel{respond: func(context.Context, []*schema.Message) (*schema.Message, error) {
				calls++
				return schema.AssistantMessage("已完成检索，保留证据来源；继续核对最新结果。", nil), nil
			}}
			runContextTest(t, nil, func(ctx context.Context) error {
				started := time.Now()
				m := e.runtimeHandler(req, "必要指令", cm)
				_, after, err := m.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{Messages: messages}, nil)
				if err != nil {
					return err
				}
				paired := true
				for i, msg := range after.Messages {
					if len(msg.ToolCalls) > 0 {
						if i+1 >= len(after.Messages) || after.Messages[i+1].ToolCallID != msg.ToolCalls[0].ID {
							paired = false
						}
					}
				}
				if !paired {
					t.Fatal("工具配对损坏")
				}
				question, latest := false, false
				for _, msg := range after.Messages {
					question = question || msg.Content == messages[0].Content
					latest = latest || msg.ToolCallID == "current"
				}
				if !question || !latest {
					t.Fatal("必要数据丢失")
				}
				report := map[string]any{"sample_version": "context-offline-v1", "model": "gpt-4o (deterministic fake)", "profile": profile, "window": req.MaxContextLength, "output": config.ContextDefaults().OutputLimit, "before_tokens": llm.MessagesTokens(messages, req.ModelName), "after_tokens": llm.MessagesTokens(after.Messages, req.ModelName), "summary_calls": calls, "question_retained": question, "latest_group_retained": latest, "protocol_valid": paired, "elapsed_ms": time.Since(started).Milliseconds(), "quality_usage_latency": "requires real-model acceptance"}
				raw, _ := json.Marshal(report)
				t.Log(string(raw))
				return nil
			})
		})
	}
}
