package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	einoOpenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	openai "github.com/meguminnnnnnnnn/go-openai"

	"solvify-agent/internal/llm"
	"solvify-agent/pkg/config"
	apperrors "solvify-agent/pkg/errors"
)

func contextRejected() error {
	return &openai.APIError{HTTPStatusCode: 400, Code: "context_length_exceeded", Message: "maximum context length exceeded"}
}

func TestContextRecoveryClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{contextRejected(), true}, {fmt.Errorf("调用失败: %w", llm.ErrContextBudget), true},
		{&einoOpenai.APIError{HTTPStatusCode: 400, Code: "context_length_exceeded", Message: "bad request"}, true},
		{errModelOutputLimit, false}, {errModelOutputFiltered, false}, {errModelOutputInvalid, false},
		{context.Canceled, false}, {modelDNSTimeout(), false},
		{&openai.APIError{HTTPStatusCode: 401, Message: "maximum context length"}, false},
		{&openai.APIError{HTTPStatusCode: 400, Message: "invalid tool arguments are too long"}, false},
	} {
		if recoverableContextError(tc.err) != tc.want {
			t.Fatalf("分类错误: %v", tc.err)
		}
		if tc.want && !errors.Is(tc.err, llm.ErrContextBudget) && apperrors.Classify(normalizeContextError(tc.err)) != apperrors.KindContextLength {
			t.Fatalf("结构化上下文原因丢失: %v", tc.err)
		}
	}
}

// 真实HTTP请求验证max_tokens/max_completion_tokens同步变化，能力配置不变。
func TestAdaptiveOutputHTTPBoundary(t *testing.T) {
	for _, field := range []string{"max_tokens", "max_completion_tokens"} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", field, streaming), func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					var payload map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Error(err)
						return
					}
					var output int
					_ = json.Unmarshal(payload[field], &output)
					if output < llm.MinimumAdaptiveOutput || output >= 4096 {
						t.Errorf("实际输出未自适应: %d", output)
					}
					other := "max_tokens"
					if field == other {
						other = "max_completion_tokens"
					}
					if string(payload[other]) != "" {
						t.Errorf("出现错误输出字段: %s", other)
					}
					var msgs []struct {
						Role    schema.RoleType
						Content string
					}
					_ = json.Unmarshal(payload["messages"], &msgs)
					used := 0
					for _, msg := range msgs {
						used += llm.MessageTokens(&schema.Message{Role: msg.Role, Content: msg.Content}, "gpt-4o")
					}
					budget, err := llm.ResolveContextBudget("", "gpt-4o", 8192, output, false, config.ContextDefaults())
					if err != nil || budget.Check(used) != nil {
						t.Errorf("真实请求仍超预算: input=%d output=%d err=%v", used, output, err)
					}
					if streaming {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprint(w, "data: {\"id\":\"r\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"简明结论\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"id":"r","choices":[{"index":0,"message":{"role":"assistant","content":"简明结论"},"finish_reason":"stop"}]}`)
					}
				}))
				defer server.Close()
				client, err := llm.NewOpenAIClient(context.Background(), llm.OpenAIClientConfig{Model: "gpt-4o", APIKey: "synthetic", BaseURL: server.URL + "/v1", MaxContextLength: 8192, Config: []byte(fmt.Sprintf(`{"max_input_tokens":8192,"max_output_tokens":4096,"max_completion_tokens":4096,"output_token_field":%q,"tool_calling":true,"reasoning_capability":"none","reasoning_mode":"default"}`, field))})
				if err != nil {
					t.Fatal(err)
				}
				messages := []*schema.Message{schema.SystemMessage("保留必要指令"), schema.UserMessage(strings.Repeat("x\n", 2350))}
				original, _ := json.Marshal(messages)
				wrapped := &budgetModel{inner: client.ChatModel(), guard: newBudgetGuard(Request{ModelName: "gpt-4o", MaxContextLength: 8192, OutputLimit: 4096})}
				var answer *schema.Message
				if streaming {
					var stream *schema.StreamReader[*schema.Message]
					stream, err = wrapped.Stream(context.Background(), messages)
					if err == nil {
						answer, err = schema.ConcatMessageStream(stream)
					}
				} else {
					answer, err = wrapped.Generate(context.Background(), messages)
				}
				if err != nil || answer == nil || answer.Content != "简明结论" || calls.Load() != 1 {
					t.Fatalf("自适应失败: answer=%v calls=%d err=%v", answer, calls.Load(), err)
				}
				after, _ := json.Marshal(messages)
				if string(after) != string(original) || client.OutputLimit() != 4096 || client.MaxContextLength() != 8192 {
					t.Fatal("修改了原消息或部署配置")
				}
			})
		}
	}
}

type recoveryModel struct {
	*deepTestModel
	call func(context.Context, []*schema.Message, []model.Option) (*schema.Message, error)
}

func (m *recoveryModel) Generate(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.call(ctx, msgs, opts)
}
func (m *recoveryModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}
func (m *recoveryModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

func TestSingleAgentAdaptiveOutput(t *testing.T) {
	for _, profile := range []Profile{ProfileQuick, ProfileDeep} {
		t.Run(string(profile), func(t *testing.T) {
			e := deepTestEngine()
			e.cfg.DeepPlanExecuteEnabled = false
			query := strings.Repeat("x\n", 2000)
			calls := 0
			m := &recoveryModel{deepTestModel: &deepTestModel{}, call: func(_ context.Context, msgs []*schema.Message, opts []model.Option) (*schema.Message, error) {
				calls++
				output := model.GetCommonOptions(nil, opts...).MaxTokens
				if output == nil || *output < llm.MinimumAdaptiveOutput || *output >= 4096 {
					t.Fatalf("真实Agent未分配输出额度: %v", output)
				}
				if msgs[len(msgs)-1].Content != query || !strings.Contains(msgs[0].Content, "输出额度已缩小") {
					t.Fatal("必要问题被裁剪或未告知本次额度变化")
				}
				budget, err := llm.ResolveContextBudget("", "gpt-4o", 8192, *output, false, config.ContextDefaults())
				if err != nil || budget.Check(llm.MessagesTokens(msgs, "gpt-4o")) != nil {
					t.Fatal("调整后仍超预算", err)
				}
				msg := schema.AssistantMessage("简明结论", nil)
				msg.ResponseMeta = &schema.ResponseMeta{FinishReason: "stop"}
				return msg, nil
			}}
			events := collectPEEvents(t, deepTestContext(t), e, Request{Profile: profile, Query: query, MaxContextLength: 8192, ModelName: "gpt-4o", UserID: "u", SessionID: "adaptive"}, m)
			if calls != 1 || !hasRetryDone(events, "简明结论") {
				t.Fatalf("单Agent输入较长时未正常结束: calls=%d events=%+v", calls, events)
			}
		})
	}
}

func TestAdaptiveOutputNoticePreservesRequiredInput(t *testing.T) {
	messages := []*schema.Message{schema.SystemMessage("必要指令"), schema.UserMessage(strings.Repeat("x\n", 2350))}
	used := llm.MessagesTokens(messages, "gpt-4o")
	wrapped := &budgetModel{guard: newBudgetGuard(Request{ModelName: "gpt-4o", MaxContextLength: 8192, InputLimit: used, OutputLimit: 4096})}
	_, input, opts, err := wrapped.prepare(context.Background(), messages, nil)
	output := model.GetCommonOptions(nil, opts...).MaxTokens
	if err != nil || output == nil || *output >= 4096 || llm.MessagesTokens(input, "gpt-4o") != used || input[0].Content != messages[0].Content || input[1].Content != messages[1].Content {
		t.Fatalf("可选额度提示挤占必要输入: output=%v err=%v", output, err)
	}
}

func TestContextRecoveryOnceAndSharedTaskBudget(t *testing.T) {
	for _, scenario := range []string{"success", "still_rejected", "no_budget", "last_call_tool", "canceled", "partial_output", "stream_open", "stream_read"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			inner := &recoveryModel{call: func(ctx context.Context, msgs []*schema.Message, opts []model.Option) (*schema.Message, error) {
				calls++
				if calls == 1 {
					if scenario == "canceled" {
						cancel()
					}
					if scenario == "partial_output" {
						return schema.AssistantMessage("已生成内容", nil), contextRejected()
					}
					return nil, contextRejected()
				}
				actual := model.GetCommonOptions(nil, opts...).MaxTokens
				if actual == nil || *actual != 2048 {
					t.Fatalf("恢复未调整输出额度: %v", actual)
				}
				if scenario == "still_rejected" {
					return nil, contextRejected()
				}
				if scenario == "last_call_tool" {
					return peCall("lookup_record", nil), nil
				}
				msg := schema.AssistantMessage("恢复后结论", nil)
				msg.ResponseMeta = &schema.ResponseMeta{FinishReason: "stop"}
				return msg, nil
			}}
			b := &taskModelBudget{Limit: 3}
			if scenario == "no_budget" {
				b.Limit = 1
			}
			if scenario == "last_call_tool" {
				b.Limit = 2
			}
			wrapped := &budgetModel{inner: inner, guard: newBudgetGuard(Request{ModelName: "gpt-4o", MaxContextLength: 8192, OutputLimit: 4096})}
			m := &peTaskModel{inner: wrapped, budget: b}
			var msg *schema.Message
			var err error
			if scenario == "stream_open" {
				var sr *schema.StreamReader[*schema.Message]
				sr, err = wrapped.Stream(ctx, []*schema.Message{schema.UserMessage("必要问题")})
				if err == nil {
					msg, err = schema.ConcatMessageStream(sr)
				}
			} else if scenario == "stream_read" {
				streamModel := &retryStreamModel{stream: func() (*schema.StreamReader[*schema.Message], error) {
					calls++
					sr, sw := schema.Pipe[*schema.Message](2)
					sw.Send(schema.AssistantMessage("部分正文", nil), nil)
					sw.Send(nil, contextRejected())
					sw.Close()
					return sr, nil
				}}
				wrapped.inner = streamModel
				sr, openErr := wrapped.Stream(ctx, []*schema.Message{schema.UserMessage("问题")})
				if openErr != nil {
					t.Fatal(openErr)
				}
				_, err = schema.ConcatMessageStream(sr)
			} else {
				msg, err = m.Generate(ctx, []*schema.Message{schema.UserMessage("必要问题")})
			}
			want := 2
			if scenario == "no_budget" || scenario == "canceled" || scenario == "partial_output" || scenario == "stream_read" {
				want = 1
			}
			if calls != want {
				t.Fatalf("恢复次数错误: calls=%d want=%d err=%v", calls, want, err)
			}
			if scenario != "stream_open" && scenario != "stream_read" && b.Used != want {
				t.Fatalf("恢复重置任务预算: %+v", b)
			}
			if scenario == "success" || scenario == "stream_open" {
				if err != nil || msg.Content != "恢复后结论" {
					t.Fatalf("恢复失败: %v", err)
				}
			} else if err == nil {
				t.Fatal("失败被吞掉")
			}
			if (scenario == "no_budget" || scenario == "last_call_tool") && !errors.Is(err, adk.ErrExceedMaxIterations) {
				t.Fatalf("预算错误丢失: %v", err)
			}
		})
	}
}

func TestPlanExecuteContextRecoveryDoesNotRepeatTools(t *testing.T) {
	for _, limit := range []int{2, 3} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			var tools, research, attempts atomic.Int32
			e := deepTestEngine()
			e.cfg.DeepMaxSubagentIterations = limit
			e.RegisterInternal("lookup_record", 1, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return taskBudgetTool{calls: &tools} })
			base := peModel(`{"strategy":"sequential","tasks":[{"id":"a","agent":"knowledge_research","title":"研究"},{"id":"s","agent":"synthesis_review","title":"审查","depends_on":["a"]}]}`, &research)
			m := &recoveryModel{deepTestModel: base, call: func(ctx context.Context, msgs []*schema.Message, opts []model.Option) (*schema.Message, error) {
				if strings.Contains(msgs[0].Content, "研究知识库") {
					n := attempts.Add(1)
					if n == 1 {
						return peCall("lookup_record", nil), nil
					}
					if n == 2 {
						return nil, contextRejected()
					}
					if output := model.GetCommonOptions(nil, opts...).MaxTokens; output == nil || *output != 2048 {
						t.Error("恢复未调整额度")
					}
					msg := schema.AssistantMessage("证据已核对", nil)
					msg.ResponseMeta = &schema.ResponseMeta{FinishReason: "stop"}
					return msg, nil
				}
				return base.Generate(ctx, msgs, opts...)
			}}
			events := collectPEEvents(t, deepTestContext(t), e, Request{Query: "核对资料", UserID: "u", SessionID: "recovery-tools"}, m)
			var done, failed bool
			for _, ev := range events {
				done = done || ev.Type == EventDone
				if ev.Type == EventError {
					failed = true
					if limit == 2 && ev.Kind != apperrors.KindMaxIterations {
						t.Fatalf("恢复额度分类错误: %+v", ev)
					}
				}
			}
			if tools.Load() != 1 || int(attempts.Load()) != limit || done != (limit == 3) || failed != (limit == 2) {
				t.Fatalf("恢复重跑工具或突破预算: tools=%d calls=%d done=%t failed=%t", tools.Load(), attempts.Load(), done, failed)
			}
		})
	}
}

func TestContextRecoveryHTTPRejectThenSuccess(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint(streaming), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				var payload map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					return
				}
				var output int
				_ = json.Unmarshal(payload["max_completion_tokens"], &output)
				want := 4096
				if n == 2 {
					want = 2048
				}
				if output != want {
					t.Errorf("恢复实际输出=%d want=%d", output, want)
				}
				if n == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(400)
					fmt.Fprint(w, `{"error":{"message":"maximum context length exceeded","type":"invalid_request_error","code":"context_length_exceeded"}}`)
					return
				}
				if streaming {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"id\":\"r\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"恢复成功\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"r","choices":[{"index":0,"message":{"role":"assistant","content":"恢复成功"},"finish_reason":"stop"}]}`)
			}))
			defer srv.Close()
			client, err := llm.NewOpenAIClient(context.Background(), llm.OpenAIClientConfig{Model: "gpt-4o", BaseURL: srv.URL + "/v1", APIKey: "synthetic", MaxContextLength: 8192, Config: []byte(`{"max_input_tokens":8192,"max_output_tokens":4096,"max_completion_tokens":4096,"output_token_field":"max_completion_tokens","tool_calling":true,"reasoning_capability":"none","reasoning_mode":"default"}`)})
			if err != nil {
				t.Fatal(err)
			}
			m := &budgetModel{inner: client.ChatModel(), guard: newBudgetGuard(Request{ModelName: "gpt-4o", MaxContextLength: 8192, OutputLimit: 4096})}
			var msg *schema.Message
			if streaming {
				var sr *schema.StreamReader[*schema.Message]
				sr, err = m.Stream(context.Background(), []*schema.Message{schema.UserMessage("必要问题")})
				if err == nil {
					msg, err = schema.ConcatMessageStream(sr)
				}
			} else {
				msg, err = m.Generate(context.Background(), []*schema.Message{schema.UserMessage("必要问题")})
			}
			if err != nil || calls.Load() != 2 || msg == nil || msg.Content != "恢复成功" {
				t.Fatalf("真实HTTP未恢复: calls=%d msg=%v err=%v", calls.Load(), msg, err)
			}
		})
	}
}

func TestSingleAgentCompletionGuard(t *testing.T) {
	for _, profile := range []Profile{ProfileQuick, ProfileDeep} {
		for _, toolCall := range []bool{false, true} {
			for _, reason := range []string{"stop", "tool_calls", "length", "content_filter", "", "unknown"} {
				t.Run(fmt.Sprintf("%s/tool=%t/%s", profile, toolCall, reason), func(t *testing.T) {
					var tools, attempts atomic.Int32
					e := deepTestEngine()
					e.cfg.DeepPlanExecuteEnabled = false
					e.cfg.ModelMaxRetries = 3
					e.RegisterInternal("lookup_record", 1, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return taskBudgetTool{calls: &tools} })
					m := &deepTestModel{rawCompletion: true, respond: func(_ context.Context, msgs []*schema.Message) (*schema.Message, error) {
						attempts.Add(1)
						for _, msg := range msgs {
							if msg.Role == schema.Tool {
								out := schema.AssistantMessage("工具完成后答案", nil)
								out.ResponseMeta = &schema.ResponseMeta{FinishReason: "stop"}
								return out, nil
							}
						}
						out := schema.AssistantMessage("已生成的部分正文", nil)
						if toolCall {
							out = peCall("lookup_record", nil)
						}
						out.ResponseMeta = &schema.ResponseMeta{FinishReason: reason}
						return out, nil
					}}
					events := collectPEEvents(t, deepTestContext(t), e, Request{Profile: profile, Query: "问题", UserID: "u", SessionID: "single-completion"}, m)
					valid := reason == "stop" && !toolCall || reason == "tool_calls" && toolCall
					var done, failed bool
					var partial string
					for _, ev := range events {
						done = done || ev.Type == EventDone
						if ev.Type == EventAnswer {
							partial += ev.Content
						}
						if ev.Type == EventError {
							failed = true
							want := apperrors.KindModelOutputInvalid
							if reason == "length" {
								want = apperrors.KindOutputLimit
							}
							if reason == "content_filter" {
								want = apperrors.KindOutputFiltered
							}
							if !valid && ev.Kind != want {
								t.Fatalf("错误类型丢失: %+v", ev)
							}
						}
					}
					if valid {
						if !done || failed {
							t.Fatalf("正常结束失败: %+v", events)
						}
					} else if done || !failed || tools.Load() != 0 || attempts.Load() != 1 {
						t.Fatalf("异常结束成功/执行工具/重试: tools=%d attempts=%d events=%+v", tools.Load(), attempts.Load(), events)
					}
					if !toolCall && !valid && partial != "已生成的部分正文" {
						t.Fatalf("部分正文丢失: %q", partial)
					}
				})
			}
		}
	}
}
