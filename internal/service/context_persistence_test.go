package service

import (
	"context"
	"errors"
	"testing"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
)

type failedMemoryRepo struct {
	repository.UserMemoryRepo
	failure error
	calls   int
}

func (repo *failedMemoryRepo) Upsert(_ context.Context, _ *entity.UserMemory) error {
	repo.calls++
	return repo.failure
}

func TestContextMemoryPersistenceFailureIsNotSuccess(t *testing.T) {
	failure := errors.New("隔离测试写入失败")
	repo := &failedMemoryRepo{failure: failure}
	svc := &contextService{memoryRepo: repo}
	chatModel := newStubChatModel(`{"type":"fact","content":"项目预算5120"}`)
	memories, err := svc.ExtractMemories(context.Background(), "owner", "session", []entity.ChatMessage{{Role: "user", Content: "项目预算5120"}}, chatModel)
	if !errors.Is(err, failure) || len(memories) != 0 || repo.calls != 1 {
		t.Fatalf("持久化失败被伪装成成功：memories=%d calls=%d err=%v", len(memories), repo.calls, err)
	}
}

func TestContextEmptySummaryDoesNotAdvanceCoverage(t *testing.T) {
	svc, fixture, chatModel := newSummaryTestFixture(t, 20, 8)
	chatModel.reply = " \n "
	_, err := svc.SummarizeSession(context.Background(), "session", chatModel)
	if err == nil || fixture.summaryRepo.saved != nil {
		t.Fatalf("空摘要仍持久化并推进覆盖：saved=%v err=%v", fixture.summaryRepo.saved, err)
	}
}
