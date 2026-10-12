package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	apperrors "solvify-agent/pkg/errors"
)

type peChoiceTestModel struct {
	deepTestModel
	calls   []schema.ToolChoice
	failure error
}

func (m *peChoiceTestModel) WithTools([]*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return m, nil
}

func (m *peChoiceTestModel) Generate(_ context.Context, _ []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	options := model.GetCommonOptions(nil, opts...)
	choice := schema.ToolChoiceAllowed
	if options.ToolChoice != nil {
		choice = *options.ToolChoice
	}
	m.calls = append(m.calls, choice)
	if choice == schema.ToolChoiceForced || m.failure != nil {
		if m.failure != nil {
			return nil, m.failure
		}
		return nil, errors.New("Thinking mode does not support this tool_choice")
	}
	return peCall("plan", map[string]any{"tasks": []any{}}), nil
}

func TestPEControlModelRetriesOnlyRejectedThinkingChoice(t *testing.T) {
	for _, failure := range []error{nil, errors.New("i/o timeout"), errors.New("status code: 429"), errors.New("invalid parameter")} {
		m := &peChoiceTestModel{failure: failure}
		control, err := (&peControlModel{inner: m}).WithTools([]*schema.ToolInfo{{Name: "plan"}})
		if err != nil {
			t.Fatal(err)
		}
		opts := []model.Option{model.WithToolChoice(schema.ToolChoiceForced)}
		msg, err := control.Generate(context.Background(), nil, opts...)
		if failure == nil {
			if err != nil || msg == nil || len(m.calls) != 2 || m.calls[1] != schema.ToolChoiceAllowed {
				t.Fatalf("未正确兼容思考模型: calls=%v, err=%v", m.calls, err)
			}
		} else if err == nil || len(m.calls) != 1 {
			t.Fatalf("非参数错误不应重试: calls=%v, err=%v", m.calls, err)
		}
		if *model.GetCommonOptions(nil, opts...).ToolChoice != schema.ToolChoiceForced {
			t.Fatal("重试改写调用方选项")
		}
	}
}

func TestPEErrorPreservesModelFailureKind(t *testing.T) {
	for _, test := range []struct {
		failure string
		kind    apperrors.Kind
	}{
		{"lookup api.deepseek.com: i/o timeout", apperrors.KindTimeout},
		{"Thinking mode does not support this tool_choice", apperrors.KindToolUnsupported},
		{"任务前置结果未成功", apperrors.KindAgentRunFailed},
	} {
		ch := make(chan Event, 1)
		(&Engine{}).emitPlanError(context.Background(), ch, errors.New(test.failure))
		if event := <-ch; event.Kind != test.kind || !event.Done {
			t.Fatalf("错误种类丢失: %+v", event)
		}
	}
}

func TestPEProfessionalTaskUsesTitleWithoutDescription(t *testing.T) {
	var calls atomic.Int32
	m := peModel(`{"strategy":"parallel","tasks":[{"id":"a","agent":"knowledge_research","title":"研究 gRPC"},{"id":"b","agent":"knowledge_research","title":"研究 Kafka"},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["a","b"]}]}`, &calls)
	respond := m.respond
	var mu sync.Mutex
	var inputs []string
	m.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		if strings.Contains(messages[0].Content, "研究知识库") {
			mu.Lock()
			inputs = append(inputs, messages[len(messages)-1].Content)
			mu.Unlock()
		}
		return respond(ctx, messages)
	}
	events := collectPEEvents(t, deepTestContext(t), deepTestEngine(), Request{Query: "研究两种技术", UserID: "u", SessionID: "scope"}, m)
	for _, ev := range events {
		if ev.Type == EventError {
			t.Fatal(ev.Error)
		}
	}
	if len(inputs) != 2 {
		t.Fatalf("未执行两个研究: %v", inputs)
	}
	for _, title := range []string{"研究 gRPC", "研究 Kafka"} {
		found := false
		for _, input := range inputs {
			if strings.Contains(input, "只执行当前任务："+title) {
				found = true
			}
		}
		if !found {
			t.Fatalf("缺少独立任务输入 %s: %v", title, inputs)
		}
	}
}
