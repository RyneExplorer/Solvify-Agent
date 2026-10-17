package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	openai "github.com/meguminnnnnnnnn/go-openai"

	"solvify-agent/internal/llm"
	"solvify-agent/pkg/config"
)

func modelDNSTimeout() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "i/o timeout", Name: "model.invalid", IsTimeout: true}}
}

func TestRetryableModelErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"dns_timeout", fmt.Errorf("请求失败: %w", modelDNSTimeout()), true},
		{"dns_not_found", &net.DNSError{Err: "no such host", IsNotFound: true}, false},
		{"connection_reset", syscall.ECONNRESET, true},
		{"connection_refused", syscall.ECONNREFUSED, true},
		{"unexpected_eof", io.ErrUnexpectedEOF, true},
		{"canceled", context.Canceled, false},
		{"budget", llm.ErrContextBudget, false},
		{"config", llm.ErrContextConfig, false},
		{"invalid_output", errors.New("模型输出格式无效"), false},
		{"success", nil, false},
	}
	for _, status := range []int{400, 401, 403, 404, 408, 413, 429, 500, 502, 503, 504} {
		want := status == 408 || status == 429 || status == 500 || status == 502 || status == 503 || status == 504
		cases = append(cases, struct {
			name string
			err  error
			want bool
		}{fmt.Sprintf("http_%d", status), &openai.APIError{HTTPStatusCode: status}, want})
		cases = append(cases, struct {
			name string
			err  error
			want bool
		}{fmt.Sprintf("request_%d", status), &openai.RequestError{HTTPStatusCode: status}, want})
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			if got := retryableModelError(context.Background(), scenario.err); got != scenario.want {
				t.Fatalf("是否重试=%t，期望=%t", got, scenario.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if retryableModelError(ctx, modelDNSTimeout()) {
		t.Fatal("已取消的请求不得重试")
	}
}

func TestModelRetryRejectsPartialOutput(t *testing.T) {
	engine := &Engine{cfg: config.AgentConfig{ModelMaxRetries: 3}}
	policy := engine.modelRetryConfig()
	for _, message := range []*schema.Message{
		schema.AssistantMessage("部分答案", nil),
		{ReasoningContent: "部分推理"},
		{Role: schema.Assistant},
		{ToolCalls: []schema.ToolCall{{ID: "pending"}}},
		{Extra: map[string]any{"message_id": "partial"}},
	} {
		if policy.ShouldRetry(context.Background(), &adk.RetryContext{Err: modelDNSTimeout(), OutputMessage: message}).Retry {
			t.Fatalf("已有模型输出不应重试: %+v", message)
		}
	}
	if !policy.ShouldRetry(context.Background(), &adk.RetryContext{Err: modelDNSTimeout(), OutputMessage: &schema.Message{}}).Retry {
		t.Fatal("框架合并空流产生的零值消息不应阻止重试")
	}
}

type retryNotificationAgent struct{}

func (*retryNotificationAgent) Name(context.Context) string        { return "retry_notification" }
func (*retryNotificationAgent) Description(context.Context) string { return "测试重试通知" }
func (*retryNotificationAgent) Run(context.Context, *adk.AgentInput, ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	iter, generator := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		defer generator.Close()
		generator.Send(&adk.AgentEvent{Err: fmt.Errorf("模型节点: %w", &adk.WillRetryError{ErrStr: "临时故障", RetryAttempt: 1})})
		generator.Send(adk.EventFromMessage(schema.AssistantMessage("成功答案", nil), nil, schema.Assistant, ""))
	}()
	return iter
}

func TestModelRetryNotificationIsNotTerminal(t *testing.T) {
	ctx := context.Background()
	engine := &Engine{}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: &retryNotificationAgent{}})
	events := make(chan Event, 10)
	engine.runWithRunner(ctx, runner, "", []*schema.Message{schema.UserMessage("测试")}, Request{}, nil, nil, events)
	close(events)
	if !hasRetryDone(collectGuardEvents(t, events), "成功答案") {
		t.Fatal("重试通知被误判为终态失败")
	}
}

func collectModelRetryEvents(t *testing.T, ctx context.Context, engine *Engine, req Request, cm model.ToolCallingChatModel) []Event {
	t.Helper()
	ctx = withPrebuiltTools(ctx, prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainKnowledge: nil}})
	channel, err := engine.Execute(ctx, req, cm)
	if err != nil {
		t.Fatal(err)
	}
	return collectGuardEvents(t, channel)
}

func TestModelRetryProductionPaths(t *testing.T) {
	for _, profile := range []Profile{ProfileQuick, ProfileDeep} {
		for _, scenario := range []string{"success", "exhausted", "authentication", "disabled"} {
			t.Run(string(profile)+"/"+scenario, func(t *testing.T) {
				engine := &Engine{cfg: config.AgentConfig{QuickAgentMaxIterations: 4, ModelMaxRetries: 3}}
				if scenario == "disabled" {
					engine.cfg.ModelMaxRetries = 0
				}
				var calls atomic.Int32
				cm := &deepTestModel{respond: func(context.Context, []*schema.Message) (*schema.Message, error) {
					attempt := calls.Add(1)
					if scenario == "success" && attempt == 3 {
						return schema.AssistantMessage("成功答案", nil), nil
					}
					if scenario == "authentication" {
						return nil, &openai.APIError{HTTPStatusCode: 401, Message: "认证失败"}
					}
					return nil, modelDNSTimeout()
				}}
				events := collectModelRetryEvents(t, context.Background(), engine, Request{Profile: profile, Query: "测试", MCPDisabled: true}, cm)
				if scenario == "success" {
					if calls.Load() != 3 || !hasRetryDone(events, "成功答案") {
						t.Fatalf("重试未成功: calls=%d events=%+v", calls.Load(), events)
					}
				} else {
					want := int32(1)
					if scenario == "exhausted" {
						want = 4
					}
					if calls.Load() != want {
						t.Fatalf("模型调用次数=%d，期望=%d", calls.Load(), want)
					}
					requirePEError(t, events)
				}
			})
		}
	}
}

func hasRetryDone(events []Event, answer string) bool {
	var done bool
	for _, event := range events {
		if event.Type == EventError {
			return false
		}
		if event.Type == EventDone && event.Content == answer {
			done = true
		}
	}
	return done
}

func TestModelRetryDoesNotRepeatTools(t *testing.T) {
	var tools, calls atomic.Int32
	probe := guardProbeTool{calls: &tools}
	engine := guardTestEngine()
	engine.cfg.ModelMaxRetries = 3
	cm := &deepTestModel{respond: func(_ context.Context, messages []*schema.Message) (*schema.Message, error) {
		switch calls.Add(1) {
		case 1:
			return schema.AssistantMessage("", []schema.ToolCall{{ID: "retry-tool", Type: "function", Function: schema.FunctionCall{Name: "guard_probe", Arguments: "{}"}}}), nil
		case 2:
			return nil, modelDNSTimeout()
		default:
			if _, ok := toolContentByCallID(messages, "retry-tool"); !ok {
				return nil, errors.New("重试丢失已完成工具结果")
			}
			return schema.AssistantMessage("成功答案", nil), nil
		}
	}}
	events := collectGuardEvents(t, mustExecute(t, guardTestContext(probe), engine, Request{Profile: ProfileQuick, Query: "测试工具"}, cm))
	if tools.Load() != 1 || calls.Load() != 3 || !hasRetryDone(events, "成功答案") {
		t.Fatalf("工具被重复执行或结果丢失: tools=%d calls=%d events=%+v", tools.Load(), calls.Load(), events)
	}
}

func TestModelRetryProfessionalAgent(t *testing.T) {
	engine := deepTestEngine()
	engine.cfg.ModelMaxRetries = 3
	var research, attempts atomic.Int32
	base := peModel(`{"strategy":"direct","tasks":[{"id":"s","agent":"synthesis_review","title":"回答"}]}`, &research)
	cm := &deepTestModel{respond: func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		if strings.Contains(messages[0].Content, "仅使用结构化研究结果") && attempts.Add(1) == 1 {
			return nil, modelDNSTimeout()
		}
		return base.respond(ctx, messages)
	}}
	events := collectPEEvents(t, deepTestContext(t), engine, Request{Query: "测试", UserID: "retry-user", SessionID: "retry-session"}, cm)
	var done bool
	for _, event := range events {
		if event.Type == EventError {
			t.Fatalf("专业 Agent 重试失败: %+v", event)
		}
		done = done || event.Type == EventDone
	}
	if !done || attempts.Load() != 2 {
		t.Fatalf("专业 Agent 未按预期重试: attempts=%d events=%+v", attempts.Load(), events)
	}
}

type retryStreamModel struct {
	stream func() (*schema.StreamReader[*schema.Message], error)
}

func (m *retryStreamModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *retryStreamModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return nil, errors.New("应使用流式调用")
}

func (m *retryStreamModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.stream()
}

func TestModelRetryStreamBoundaries(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial_%t", partial), func(t *testing.T) {
			var calls atomic.Int32
			engine := &Engine{cfg: config.AgentConfig{QuickAgentMaxIterations: 4, ModelMaxRetries: 3}}
			cm := &retryStreamModel{stream: func() (*schema.StreamReader[*schema.Message], error) {
				if calls.Add(1) > 1 {
					return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("成功答案", nil)}), nil
				}
				reader, writer := schema.Pipe[*schema.Message](2)
				if partial {
					writer.Send(schema.AssistantMessage("部分答案", nil), nil)
				}
				writer.Send(nil, io.ErrUnexpectedEOF)
				writer.Close()
				return reader, nil
			}}
			events := collectModelRetryEvents(t, context.Background(), engine, Request{Profile: ProfileQuick, Query: "测试", MaxContextLength: 64000, OutputLimit: 4096}, cm)
			if !partial {
				if calls.Load() != 2 || !hasRetryDone(events, "成功答案") {
					t.Fatalf("首块前失败未重试: calls=%d events=%+v", calls.Load(), events)
				}
				return
			}
			if calls.Load() != 1 {
				t.Fatal("已输出部分答案后不应重试")
			}
			var failed bool
			for _, event := range events {
				failed = failed || event.Type == EventError
				if event.Type == EventDone {
					t.Fatal("部分输出失败不应宣称成功")
				}
			}
			if !failed {
				t.Fatal("部分输出失败缺少错误事件")
			}
		})
	}
}

func TestModelRetryCancellationDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	engine := &Engine{cfg: config.AgentConfig{ModelMaxRetries: 3}}
	retry := engine.modelRetryConfig()
	retry.BackoffFunc = func(context.Context, int) time.Duration {
		cancel()
		return time.Hour
	}
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "retry_cancel", ModelRetryConfig: retry, Model: &deepTestModel{respond: func(context.Context, []*schema.Message) (*schema.Message, error) {
		calls.Add(1)
		return nil, modelDNSTimeout()
	}}})
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent})
	iter := runner.Run(ctx, []*schema.Message{schema.UserMessage("测试")})
	for {
		_, ok := iter.Next()
		if !ok {
			break
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("取消后发生了额外调用: %d", calls.Load())
	}
}
