package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"solvify-agent/internal/llm"
	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/config"
)

func quickSummaryHistory() []*schema.Message {
	return []*schema.Message{
		schema.UserMessage("项目代号海星，预算4096；" + strings.Repeat("record: alpha=7;\n", 500)),
		schema.AssistantMessage("已确认代号海星，预算4096，证据doc-ctx/chunk-1。", nil),
		schema.UserMessage("最近补充：预算单位为token。"),
		schema.AssistantMessage("确认单位为token。", nil),
		schema.UserMessage("当前问题：代号、预算和证据是什么？"),
	}
}

func TestQuickConversationGroupsProtectRecentAndFacts(t *testing.T) {
	messages := quickSummaryHistory()
	groups := completedConversationGroups(messages)
	if len(groups) != 1 || groups[0] != (contextGroup{0, 2}) {
		t.Fatal("最近问答或当前问题进入摘要")
	}
	for _, scenario := range []string{"tools", "failure", "summary", "system"} {
		copy := append([]*schema.Message(nil), messages...)
		switch scenario {
		case "tools":
			copy[1] = peCall("search", nil)
		case "failure":
			copy[1] = schema.AssistantMessage(`{"success":false,"message":"未执行"}`, nil)
		case "summary":
			copy[0] = schema.UserMessage(runtimeSummaryPrefix + "先前摘要")
		case "system":
			copy[0] = schema.SystemMessage("必要指令")
		}
		if len(completedConversationGroups(copy)) != 0 {
			t.Fatalf("受保护内容被作为普通问答摘要：%s", scenario)
		}
	}
	guardInput := append([]*schema.Message{schema.UserMessage(runtimeSummaryPrefix + "海星/4096")}, messages...)
	reduced, ok := dropOldConversation(guardInput)
	if !ok || reduced[0] != guardInput[0] || reduced[len(reduced)-1] != messages[4] {
		t.Fatal("预算降级未保留摘要和当前问题")
	}
}

func TestRuntimeSummaryClearsBoundToolsAtHTTPBoundary(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var payload struct {
			Tools               json.RawMessage `json:"tools"`
			MaxCompletionTokens int             `json:"max_completion_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload.Tools) != 0 && string(payload.Tools) != "null" && string(payload.Tools) != "[]" {
			t.Error("摘要HTTP请求仍带绑定的业务工具")
		}
		if payload.MaxCompletionTokens != config.DefaultRuntimeContextPolicy().SummaryOutput {
			t.Error("摘要输出预留和HTTP参数不同源")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"summary","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"海星，4096，doc-ctx/chunk-1。"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	client, err := llm.NewOpenAIClient(context.Background(), llm.OpenAIClientConfig{APIKey: "synthetic", BaseURL: server.URL, Model: "gpt-4o", MaxContextLength: 8192, Config: []byte(`{"max_input_tokens":8192,"max_output_tokens":8192,"max_completion_tokens":4096,"output_token_field":"max_completion_tokens","tool_calling":true,"reasoning_capability":"none","reasoning_mode":"default"}`)})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := client.ChatModel().WithTools([]*schema.ToolInfo{{Name: "write_external_resource", Desc: "不应出现在摘要调用中的工具"}})
	if err != nil {
		t.Fatal(err)
	}
	e := deepTestEngine()
	req := Request{Profile: ProfileQuick, ModelID: "http-summary", ModelName: "gpt-4o", MaxContextLength: 8192}
	runContextTest(t, nil, func(ctx context.Context) error {
		m := e.runtimeHandler(req, "必要指令", bound)
		_, next, err := m.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{Messages: quickSummaryHistory()}, nil)
		if err != nil {
			return err
		}
		if requests.Load() != 1 || !strings.HasPrefix(next.Messages[0].Content, runtimeSummaryPrefix) {
			return errors.New("HTTP摘要链路未真正执行")
		}
		return nil
	})
}

func TestQuickSummaryHistoryAndFallback(t *testing.T) {
	for _, scenario := range []string{"success", "short", "failure", "no_progress", "invalid_tools", "length", "content_filter", "unknown", "missing_finish", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			e := deepTestEngine()
			e.cfg.RuntimeContext = config.DefaultRuntimeContextPolicy()
			e.cfg.RuntimeContext.SummaryMaxCalls = 1
			req := Request{Profile: ProfileQuick, ModelID: "quick-summary", ModelName: "gpt-4o", MaxContextLength: 8192}
			messages := quickSummaryHistory()
			if scenario == "short" {
				messages[0] = schema.UserMessage("项目代号海星，预算4096。")
			}
			original, _ := json.Marshal(messages)
			calls := 0
			cm := &deepTestModel{respond: func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
				calls++
				if llm.MessagesTokens(input, req.ModelName) > e.runtimePolicy().SummaryInput || !strings.Contains(input[1].Content, "doc-ctx/chunk-1") {
					return nil, errors.New("摘要输入超过额度或缺少证据")
				}
				switch scenario {
				case "failure":
					return nil, errors.New("模型暂时不可用")
				case "no_progress":
					return schema.AssistantMessage(strings.Repeat("冗长摘要", 1500), nil), nil
				case "invalid_tools":
					return peCall("unexpected_tool", nil), nil
				case "length", "content_filter", "unknown":
					msg := schema.AssistantMessage("未完整的摘要", nil)
					msg.ResponseMeta = &schema.ResponseMeta{FinishReason: scenario}
					return msg, nil
				}
				return schema.AssistantMessage("项目代号海星，预算4096，证据doc-ctx/chunk-1。", nil), nil
			}}
			cm.rawCompletion = scenario == "missing_finish"
			runContextTest(t, nil, func(ctx context.Context) error {
				m := e.runtimeHandler(req, "必要指令", cm).(*runtimeContextMiddleware)
				state := &adk.ChatModelAgentState{Messages: messages}
				if scenario == "cancel" {
					cancelled, cancel := context.WithCancel(ctx)
					cancel()
					_, _, err := m.BeforeModelRewriteState(cancelled, state, nil)
					if !errors.Is(err, context.Canceled) || calls != 0 {
						return errors.New("取消请求仍发起摘要")
					}
					return nil
				}
				_, next, err := m.BeforeModelRewriteState(ctx, state, nil)
				if err != nil {
					return err
				}
				if scenario == "success" {
					if len(next.Messages) != 4 || next.Messages[0].Role != schema.User || !strings.Contains(next.Messages[0].Content, "doc-ctx/chunk-1") {
						return errors.New("历史摘要未保留结论和证据")
					}
					for i, message := range messages[2:] {
						if next.Messages[i+1] != message {
							return errors.New("最新问答或当前问题被修改")
						}
					}
					guard := newBudgetGuard(req)
					_, guarded, err := guard.BeforeModelRewriteState(ctx, next, nil)
					if err != nil || guarded.Messages[0] != next.Messages[0] {
						return errors.New("预算降级丢失摘要或仍超限")
					}
				} else if next != state {
					return errors.New("短请求或失败改变原上下文")
				}
				_, _, err = m.BeforeModelRewriteState(ctx, next, nil)
				if err != nil {
					return err
				}
				want := 1
				if scenario == "short" {
					want = 0
				}
				if calls != want {
					return errors.New("短请求调用摘要或摘要次数失控")
				}
				encoded, _ := json.Marshal(messages)
				if string(encoded) != string(original) {
					return errors.New("原历史被就地修改")
				}
				return nil
			})
		})
	}
}

func TestQuickSummarySurvivesEngineCheckpoint(t *testing.T) {
	var summaries, chatCalls atomic.Int32
	cm := &deepTestModel{respond: func(_ context.Context, input []*schema.Message) (*schema.Message, error) {
		if strings.HasPrefix(input[0].Content, "总结已完成交互") {
			summaries.Add(1)
			return schema.AssistantMessage("项目代号海星，预算4096，证据doc-ctx/chunk-1。", nil), nil
		}
		found := false
		for _, message := range input {
			if strings.HasPrefix(message.Content, runtimeSummaryPrefix) && strings.Contains(message.Content, "doc-ctx/chunk-1") {
				found = true
			}
		}
		if !found {
			return nil, errors.New("生产模型调用未收到运行摘要")
		}
		if chatCalls.Add(1) == 1 {
			return peCall(clarifyToolName, map[string]any{"question": "是否采用原预算？"}), nil
		}
		return schema.AssistantMessage("海星，4096，依据doc-ctx/chunk-1。", nil), nil
	}}
	first := guardTestEngine()
	first.cfg.RuntimeContext = config.DefaultRuntimeContextPolicy()
	first.cfg.RuntimeContext.SummaryMaxCalls = 1
	req := Request{Profile: ProfileQuick, UserID: "u", SessionID: "quick-summary-resume", ModelID: "quick-summary", ModelName: "gpt-4o", MaxContextLength: 8192, Query: quickSummaryHistory()[4].Content}
	for _, message := range quickSummaryHistory()[:4] {
		req.History = append(req.History, entity.ChatMessage{Role: string(message.Role), Content: message.Content})
	}
	ctx := guardTestContext(guardProbeTool{calls: new(atomic.Int32)})
	var interrupt Event
	for _, event := range collectGuardEvents(t, mustExecute(t, ctx, first, req, cm)) {
		if event.Type == EventError {
			t.Fatal(event.Error)
		}
		if event.Type == EventInterrupt {
			interrupt = event
		}
	}
	if interrupt.CheckpointID == "" || summaries.Load() != 1 {
		t.Fatal("摘要或澄清中断未完成")
	}
	other := guardTestEngine()
	other.cfg = first.cfg
	raw, found, err := first.buildCheckpointStore(req.SessionID).Get(ctx, interrupt.CheckpointID)
	if err != nil || !found {
		t.Fatal("checkpoint保存失败", err)
	}
	if err = other.buildCheckpointStore(req.SessionID).Set(ctx, interrupt.CheckpointID, raw); err != nil {
		t.Fatal(err)
	}
	req.CheckpointID = interrupt.CheckpointID
	req.ResumeData = map[string]any{interrupt.InterruptID: "采用"}
	done := false
	for _, event := range collectGuardEvents(t, mustExecute(t, ctx, other, req, cm)) {
		if event.Type == EventError {
			t.Fatal(event.Error)
		}
		done = done || event.Type == EventDone
	}
	if !done || summaries.Load() != 1 || chatCalls.Load() != 2 {
		t.Fatal("恢复重复摘要或未完成")
	}
}
