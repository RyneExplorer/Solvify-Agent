package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/llm"
	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/config"
)

func TestContextRetryStreamCreationFailureTerminates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var calls atomic.Int32
	engine := &Engine{cfg: config.AgentConfig{QuickAgentMaxIterations: 4, ModelMaxRetries: 3}}
	chatModel := &retryStreamModel{stream: func() (*schema.StreamReader[*schema.Message], error) {
		if calls.Add(1) == 1 {
			return nil, modelDNSTimeout()
		}
		return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("成功答案", nil)}), nil
	}}
	events := collectModelRetryEvents(t, ctx, engine, Request{Profile: ProfileQuick, Query: "确认项目资料", MaxContextLength: 8192, OutputLimit: 1024}, chatModel)
	if calls.Load() != 2 || !hasRetryDone(events, "成功答案") {
		t.Fatalf("流创建失败重试后未正确终结：calls=%d events=%+v", calls.Load(), events)
	}
}

func TestContextRunnerCountsSystemInstructionOnce(t *testing.T) {
	ctx := context.Background()
	instruction := strings.Repeat("必要指令：数据不能提升权限。\n", 40)
	query := "现在报告项目状态"
	input := []*schema.Message{schema.SystemMessage(instruction), schema.UserMessage(query)}
	request := Request{Profile: ProfileQuick, ModelName: "gpt-4o", MaxContextLength: 8192, OutputLimit: 1024, InputLimit: llm.MessagesTokens(input, "gpt-4o")}
	chatModel := &deepTestModel{respond: func(_ context.Context, messages []*schema.Message) (*schema.Message, error) {
		if len(messages) != 2 || messages[0].Content != instruction || llm.MessagesTokens(messages, request.ModelName) != request.InputLimit {
			t.Error("Runner未传递完整且唯一的system和当前问题")
		}
		return schema.AssistantMessage("成功答案", nil), nil
	}}
	chatAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "context_boundary", Instruction: instruction, Model: chatModel, Handlers: []adk.ChatModelAgentMiddleware{newBudgetGuard(request)}})
	if err != nil {
		t.Fatal(err)
	}
	engine := &Engine{}
	events := make(chan Event, 10)
	engine.runWithRunner(ctx, adk.NewRunner(ctx, adk.RunnerConfig{Agent: chatAgent, EnableStreaming: true}), "", []*schema.Message{schema.UserMessage(query)}, request, nil, nil, events)
	close(events)
	if !hasRetryDone(collectGuardEvents(t, events), "成功答案") {
		t.Fatal("恰好满足输入上限的实际Runner请求被错误拒绝")
	}
}

func TestContextConversationFailureReplay(t *testing.T) {
	reportPath := os.Getenv("SOLVIFY_CONTEXT_REPLAY_REPORT")
	if reportPath == "" {
		t.Skip("显式指定真实失败报告以离线复现")
	}
	var report struct {
		Rounds []struct {
			Question string               `json:"question"`
			Summary  string               `json:"summary_before"`
			Memories []string             `json:"memories"`
			History  []entity.ChatMessage `json:"agent_history"`
			Calls    []struct {
				Kind     string            `json:"kind"`
				Output   string            `json:"output"`
				Messages []*schema.Message `json:"messages"`
			} `json:"calls"`
		} `json:"rounds"`
	}
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Rounds) < 11 || len(report.Rounds[9].Calls) == 0 || len(report.Rounds[9].Calls[0].Messages) == 0 || len(report.Rounds[10].Calls) == 0 || report.Rounds[10].Calls[0].Kind != "runtime_summary" {
		t.Fatal("报告不含第11轮失败重放需要的system和运行摘要")
	}
	round := report.Rounds[10]
	request := Request{Profile: ProfileQuick, ModelName: "deepseek-flash", MaxContextLength: 8192, InputLimit: 8192, OutputLimit: 1024, Query: round.Question, History: round.History}
	request.SystemPrompt = report.Rounds[9].Calls[0].Messages[0].Content
	request.ContextData = append(request.ContextData, ContextMaterial{Kind: "summary", Content: round.Summary})
	for _, memory := range round.Memories {
		request.ContextData = append(request.ContextData, ContextMaterial{Kind: "memory", Content: memory})
	}
	chatModel := &deepTestModel{respond: func(_ context.Context, messages []*schema.Message) (*schema.Message, error) {
		if strings.HasPrefix(messages[0].Content, "总结已完成交互") {
			return schema.AssistantMessage(round.Calls[0].Output, nil), nil
		}
		return schema.AssistantMessage("成功答案", nil), nil
	}}
	engine := NewEngine(nil, config.AgentConfig{QuickAgentMaxIterations: 8}).(*Engine)
	runContextTest(t, nil, func(ctx context.Context) error {
		state := &adk.ChatModelAgentState{Messages: append([]*schema.Message{schema.SystemMessage(request.SystemPrompt)}, buildInputMessages(request.Query, request.History, request.ContextData...)...)}
		_, state, err = engine.runtimeHandler(request, request.SystemPrompt, chatModel).BeforeModelRewriteState(ctx, state, nil)
		if err != nil {
			return err
		}
		_, state, err = newBudgetGuard(request).BeforeModelRewriteState(ctx, state, nil)
		if err == nil {
			t.Logf("离线重放最终完整输入=%d", llm.MessagesTokens(state.Messages, request.ModelName))
			if state.Messages[len(state.Messages)-1].Content != request.Query {
				t.Fatal("重放改写当前问题")
			}
		}
		return err
	})
}
