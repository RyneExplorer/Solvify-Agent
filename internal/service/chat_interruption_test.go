package service

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"gorm.io/datatypes"

	"solvify-agent/internal/agent"
	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	apperrors "solvify-agent/pkg/errors"
)

type interruptionMessages struct {
	repository.ChatMessageRepo
	saved []entity.ChatMessage
}

func (r *interruptionMessages) Create(ctx context.Context, msg *entity.ChatMessage) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.saved = append(r.saved, *msg)
	return nil
}
func (*interruptionMessages) FindRecentForContext(context.Context, string, int) ([]entity.ChatMessage, error) {
	return nil, nil
}

type interruptionModels struct{ repository.ModelRepo }

func (*interruptionModels) GetByID(context.Context, string) (*entity.Model, error) {
	return &entity.Model{Provider: "openai", ModelID: "timeout-service-test", BaseURL: "https://example.invalid/v1", MaxContextLength: 8192,
		Config: datatypes.JSON(`{"max_input_tokens":8192,"max_output_tokens":8192,"max_completion_tokens":2048,"output_token_field":"max_completion_tokens","tool_calling":true,"reasoning_capability":"none","reasoning_mode":"default"}`)}, nil
}

type interruptionAgent struct {
	agent.AgentInterface
	cancel  context.CancelFunc
	plan    bool
	partial string
	kind    apperrors.Kind
}

func (*interruptionAgent) EstimateToolsTokens(ctx context.Context, _ string, _ []string, _ string, _ agent.Profile, _ bool, _ ...string) (int, context.Context, error) {
	return 0, ctx, nil
}
func (a *interruptionAgent) Execute(context.Context, agent.Request, model.ToolCallingChatModel) (<-chan agent.Event, error) {
	ch := make(chan agent.Event, 4)
	if a.plan {
		ch <- agent.Event{Type: agent.EventPlan, Plan: &agent.ExecutionPlan{ID: "p"}}
	}
	if a.partial != "" {
		ch <- agent.Event{Type: agent.EventAnswer, Content: a.partial}
	}
	if a.cancel != nil {
		a.cancel()
	} else {
		kind := a.kind
		if kind == "" {
			kind = apperrors.KindTimeout
		}
		ch <- agent.Event{Type: agent.EventError, Kind: kind, Done: true}
	}
	close(ch)
	return ch, nil
}

func TestAgentModePreservesInterruptedAnswer(t *testing.T) {
	for _, scenario := range []string{"quick", "deep", "cancel", "no_output", "output_limit", "output_filtered", "model_output_invalid"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := &interruptionAgent{partial: "已经生成的部分内容", plan: scenario == "deep"}
			completionError := scenario == "output_limit" || scenario == "output_filtered" || scenario == "model_output_invalid"
			if completionError {
				a.kind, a.plan = apperrors.Kind(scenario), true
			}
			if scenario == "cancel" {
				a.cancel = cancel
			}
			if scenario == "no_output" {
				a.partial = ""
			}
			messages := &interruptionMessages{}
			svc := &chatService{sessionRepo: &inputLimitSessionRepo{}, messageRepo: messages, modelRepo: &interruptionModels{}, agentEngine: a}
			ch := make(chan dto.StreamEvent, 20)
			profile := agent.ProfileQuick
			if a.plan {
				profile = agent.ProfileDeep
			}
			svc.processAgentMode(ctx, "owner", "session", "user-msg", requestdto.SendMessageRequest{Content: "测试", ModelID: "model", ModelType: "system"}, profile, ch)
			close(ch)
			failed := false
			for event := range ch {
				if event.Type == "done" {
					t.Fatal("中断不得发送成功终态")
				}
				if event.Type == "error" {
					failed = true
					if completionError {
						want := messageFor(a.kind, profile)
						if event.Title != want.Title || event.Detail != want.Detail || event.Retryable {
							t.Fatalf("结束异常提示错误: %+v", event)
						}
					}
				}
			}
			if scenario != "cancel" && !failed {
				t.Fatal("缺少失败终态")
			}
			if scenario == "no_output" {
				if len(messages.saved) != 0 {
					t.Fatal("无输出失败不应创建空回答")
				}
				return
			}
			if len(messages.saved) != 1 || !strings.Contains(messages.saved[0].Content, a.partial) || !strings.Contains(messages.saved[0].Content, "回答已中断，内容不完整") || !strings.Contains(string(messages.saved[0].Metadata), "execution_error") {
				t.Fatalf("部分回答未保留: %+v", messages.saved)
			}
			persisted := messageResponse(messages.saved[0])
			if persisted.ExecutionError == "" || (a.plan && persisted.ExecutionPlan == nil) || persisted.ExecutionRetryable != (scenario != "cancel" && !completionError) {
				t.Fatalf("历史响应丢失失败原因或计划: %+v", persisted)
			}
			if completionError && !strings.Contains(persisted.ExecutionError, messageFor(a.kind, profile).Detail) {
				t.Fatalf("历史记录丢失准确结束原因: %s", persisted.ExecutionError)
			}
		})
	}
}
