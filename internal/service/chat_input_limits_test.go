package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	apperrors "solvify-agent/pkg/errors"
)

type inputLimitSessionRepo struct {
	repository.ChatSessionRepo
	calls int
}

func (repo *inputLimitSessionRepo) FindByID(_ context.Context, sessionID string) (*entity.ChatSession, error) {
	repo.calls++
	return &entity.ChatSession{ID: sessionID, UserID: "owner", Status: sessionStatusActive}, nil
}

type inputLimitMessageRepo struct {
	repository.ChatMessageRepo
	calls   int
	content string
}

func (repo *inputLimitMessageRepo) Create(_ context.Context, message *entity.ChatMessage) error {
	repo.calls++
	repo.content = message.Content
	return errors.New("测试持久化失败")
}

func TestChatService_RejectBlankMessagesBeforeSideEffects(t *testing.T) {
	for _, content := range []string{"", "   ", "\t", "\r\n", " \t\r\n ", "\u00a0\u2003\u3000"} {
		t.Run(content, func(t *testing.T) {
			sessions := &inputLimitSessionRepo{}
			messages := &inputLimitMessageRepo{}
			svc := &chatService{sessionRepo: sessions, messageRepo: messages}
			stream, err := svc.SendMessage(context.Background(), "owner", "session", requestdto.SendMessageRequest{Content: content})
			var businessError *apperrors.BizError
			if stream != nil || !errors.As(err, &businessError) || businessError.Code != apperrors.CodeBadRequest {
				t.Fatalf("空白消息未返回参数错误：stream=%v err=%v", stream, err)
			}
			if sessions.calls != 0 || messages.calls != 0 {
				t.Fatalf("空白消息仍触发仓库：sessions=%d messages=%d", sessions.calls, messages.calls)
			}
		})
	}
}

func TestChatService_InputLimitBeforeSideEffects(t *testing.T) {
	for _, content := range []string{strings.Repeat("中", requestdto.MaxChatMessageChars+1), strings.Repeat("😀", requestdto.MaxChatMessageChars+1), "下面是不可省略的当前问题，完整读入后只回答收到。\n" + strings.Repeat("当前问题必要数据 alpha=7 budget-check=overflow\n", 1500)} {
		sessions := &inputLimitSessionRepo{}
		messages := &inputLimitMessageRepo{}
		svc := &chatService{sessionRepo: sessions, messageRepo: messages}
		stream, err := svc.SendMessage(context.Background(), "owner", "session", requestdto.SendMessageRequest{Content: content})
		var businessError *apperrors.BizError
		if stream != nil || !errors.As(err, &businessError) || businessError.Code != apperrors.CodeBadRequest || !strings.Contains(businessError.Message, "4000") {
			t.Fatalf("直接调用Service未返回明确的输入错误：stream=%v err=%v", stream, err)
		}
		if sessions.calls != 0 || messages.calls != 0 {
			t.Fatalf("输入拒绝仍触发仓库：sessions=%d messages=%d", sessions.calls, messages.calls)
		}
	}
}

func TestChatService_InputLimitPreservesBoundaryContent(t *testing.T) {
	content := strings.Repeat("😀", requestdto.MaxChatMessageChars-2) + " \n"
	sessions := &inputLimitSessionRepo{}
	messages := &inputLimitMessageRepo{}
	svc := &chatService{sessionRepo: sessions, messageRepo: messages}
	stream, err := svc.SendMessage(context.Background(), "owner", "session", requestdto.SendMessageRequest{Content: content})
	if stream != nil || err == nil || sessions.calls != 1 || messages.calls != 1 || messages.content != content {
		t.Fatalf("边界消息未按原文进入持久化：sessionCalls=%d messageCalls=%d err=%v", sessions.calls, messages.calls, err)
	}
}
