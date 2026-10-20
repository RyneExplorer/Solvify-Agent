package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	apperrors "solvify-agent/pkg/errors"
)

type peAnswerStreamModel struct {
	*deepTestModel
	stream func(context.Context) (*schema.StreamReader[*schema.Message], error)
}

func (m *peAnswerStreamModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *peAnswerStreamModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	if strings.Contains(msgs[0].Content, "你是 Replanner") {
		if choice := model.GetCommonOptions(nil, opts...).ToolChoice; choice == nil || *choice != schema.ToolChoiceAllowed {
			return nil, errors.New("思考模型必须使用 auto 工具选择")
		}
		return m.stream(ctx)
	}
	return m.deepTestModel.Stream(ctx, msgs, opts...)
}

func peAnswerChunk(name, args string) *schema.Message {
	index := 0
	return schema.AssistantMessage("", []schema.ToolCall{{Index: &index, Function: schema.FunctionCall{Name: name, Arguments: args}}})
}

func TestPlanExecuteFinalAnswerStreamsBeforeModelCompletes(t *testing.T) {
	for _, ending := range []string{"success", "read_error", "invalid_json", "length", "valid_json_length", "content_filter", "missing_finish", "unknown_finish", "wrong_finish", "cancel"} {
		t.Run(ending, func(t *testing.T) {
			ctx, cancel := context.WithCancel(deepTestContext(t))
			defer cancel()
			release := make(chan struct{})
			closed := make(chan struct{})
			var streamCalls atomic.Int32
			m := &peAnswerStreamModel{deepTestModel: peModel(`{"strategy":"direct","tasks":[{"id":"s","agent":"synthesis_review","title":"汇总"}]}`, new(atomic.Int32))}
			m.stream = func(ctx context.Context) (*schema.StreamReader[*schema.Message], error) {
				streamCalls.Add(1)
				reader, writer := schema.Pipe[*schema.Message](0)
				go func() {
					defer close(closed)
					defer writer.Close()
					writer.Send(peAnswerChunk("respond", `{"response":"第一段`), nil)
					select {
					case <-ctx.Done():
						writer.Send(nil, ctx.Err())
						return
					case <-release:
					}
					switch ending {
					case "read_error":
						writer.Send(nil, io.ErrUnexpectedEOF)
					case "invalid_json":
						writer.Send(peAnswerChunk("", `\q"}`), nil)
					case "length", "valid_json_length", "content_filter", "missing_finish", "unknown_finish", "wrong_finish":
						finish := ending
						if ending == "valid_json_length" {
							writer.Send(peAnswerChunk("", `"}`), nil)
							finish = "length"
						}
						if ending == "missing_finish" {
							finish = ""
						}
						if ending == "wrong_finish" {
							finish = "stop"
						}
						writer.Send(&schema.Message{ResponseMeta: &schema.ResponseMeta{FinishReason: finish}}, nil)
					default:
						writer.Send(peAnswerChunk("", `\n第二段\uD83D`), nil)
						writer.Send(peAnswerChunk("respond", `\uDE00"}`), nil)
						writer.Send(&schema.Message{ResponseMeta: &schema.ResponseMeta{FinishReason: "tool_calls"}}, nil)
					}
				}()
				return reader, nil
			}
			ch, err := deepTestEngine().Execute(ctx, Request{Query: "测试", UserID: "u", SessionID: "stream"}, m)
			if err != nil {
				t.Fatal(err)
			}
			var content string
			var done, failed bool
			for ev := range ch {
				if ev.Type == EventAnswer {
					if content == "" {
						if ev.Content != "第一段" {
							t.Fatalf("首段没有实时输出: %+v", ev)
						}
						select {
						case <-closed:
							t.Fatal("首段必须在上游结束前可见")
						default:
						}
						if ending == "cancel" {
							cancel()
						} else {
							close(release)
						}
					}
					content += ev.Content
				}
				if ev.Type == EventError {
					failed = true
					var want apperrors.Kind
					switch ending {
					case "length", "valid_json_length":
						want = apperrors.KindOutputLimit
					case "content_filter":
						want = apperrors.KindOutputFiltered
					case "invalid_json", "missing_finish", "unknown_finish", "wrong_finish":
						want = apperrors.KindModelOutputInvalid
					}
					if want != "" && ev.Kind != want {
						t.Fatalf("错误原因丢失: got=%s want=%s", ev.Kind, want)
					}
				}
				if ev.Type == EventDone {
					done = true
					if ev.Content != content {
						t.Fatalf("最终全文与增量正文不一致: %q / %q", ev.Content, content)
					}
				}
			}
			if ending == "success" {
				if content != "第一段\n第二段😀" || !done || failed {
					t.Fatalf("最终回答错误: %q done=%t failed=%t", content, done, failed)
				}
			} else if content != "第一段" || done || (ending != "cancel" && !failed) {
				t.Fatalf("部分正文失败终态错误: %q done=%t failed=%t", content, done, failed)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("上游流未关闭")
			}
			if streamCalls.Load() != 1 {
				t.Fatalf("不应重复生成回答: %d", streamCalls.Load())
			}
		})
	}
}

func TestPEResponseDecoderAcrossEveryByteBoundary(t *testing.T) {
	const want = "中文😀\n\t\"引号\"\\路径\b\f\r/[1]"
	encoded, _ := json.Marshal(map[string]string{"response": want})
	for _, raw := range []string{string(encoded), ` { "response" : "中文\uD83D\uDE00\n\t\"引号\"\\路径\b\f\r/[1]" }`} {
		for split := 0; split <= len(raw); split++ {
			d := &peResponseDecoder{}
			first, err := d.push(raw[:split])
			if err != nil {
				t.Fatal(err)
			}
			second, err := d.push(raw[split:])
			if err != nil || !d.done || first+second != want {
				t.Fatalf("边界 %d 解码错误: %q %q %v", split, first, second, err)
			}
		}
		d := &peResponseDecoder{}
		var content strings.Builder
		for i := range len(raw) {
			part, err := d.push(raw[i : i+1])
			if err != nil {
				t.Fatal(err)
			}
			content.WriteString(part)
		}
		if content.String() != want {
			t.Fatalf("单字节流解码错误: %q", content.String())
		}
	}
}

func TestPlanExecuteFinalAnswerRejectsIncompleteProtocol(t *testing.T) {
	for _, scenario := range []string{"empty_stream", "unclosed_json", "missing_response", "empty_response"} {
		t.Run(scenario, func(t *testing.T) {
			var calls atomic.Int32
			m := &peAnswerStreamModel{deepTestModel: peModel(`{"strategy":"direct","tasks":[{"id":"s","agent":"synthesis_review","title":"审查"}]}`, new(atomic.Int32))}
			m.stream = func(context.Context) (*schema.StreamReader[*schema.Message], error) {
				calls.Add(1)
				if scenario == "empty_stream" {
					return schema.StreamReaderFromArray([]*schema.Message{}), nil
				}
				raw := map[string]string{
					"unclosed_json":    `{"response":"部分正文`,
					"missing_response": `{"other":"错误字段"}`,
					"empty_response":   `{"response":""}`,
				}[scenario]
				msg := peAnswerChunk("respond", raw)
				msg.ResponseMeta = &schema.ResponseMeta{FinishReason: "tool_calls"}
				return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
			}
			events := collectPEEvents(t, deepTestContext(t), deepTestEngine(), Request{Query: "核对", UserID: "u", SessionID: "invalid-respond"}, m)
			var partial string
			failed := false
			for _, ev := range events {
				if ev.Type == EventDone {
					t.Fatal("不完整协议被标为成功")
				}
				if ev.Type == EventError {
					failed = true
					if ev.Kind != apperrors.KindModelOutputInvalid {
						t.Fatalf("协议异常误报: %+v", ev)
					}
				}
				if ev.Type == EventAnswer {
					partial += ev.Content
				}
			}
			if !failed || calls.Load() != 1 || (scenario == "unclosed_json" && partial != "部分正文") {
				t.Fatalf("异常回答重试或部分正文丢失: calls=%d partial=%q", calls.Load(), partial)
			}
		})
	}
}

func TestPlanExecuteAnswerStreamRejectsPrematureRespond(t *testing.T) {
	ctx := deepTestContext(t)
	m := &peAnswerStreamModel{deepTestModel: peModel(`{"strategy":"sequential","tasks":[{"id":"a","agent":"knowledge_research","title":"研究"},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["a"]}]}`, new(atomic.Int32))}
	m.stream = func(context.Context) (*schema.StreamReader[*schema.Message], error) {
		return schema.StreamReaderFromArray([]*schema.Message{peAnswerChunk("respond", `{"response":"未完成任务时不能公开"}`)}), nil
	}
	ch, err := deepTestEngine().Execute(ctx, Request{Query: "测试", UserID: "u", SessionID: "early"}, m)
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	for ev := range ch {
		events = append(events, ev)
	}
	requirePEError(t, events)
}
