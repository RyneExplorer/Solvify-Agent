package service

import (
	"context"
	"testing"
	"time"

	"solvify-agent/internal/model/entity"
)

func TestExplicitZeroBudgetsStayZero(t *testing.T) {
	repo := &recallTestMessageRepo{recent: []entity.ChatMessage{{ID: "history", Role: "user", Content: "历史", CreatedAt: time.Now()}}, vecHits: []entity.ChatMessage{{ID: "recall", Content: "召回"}}}
	ctx, err := recallTestService(repo).BuildContext(context.Background(), "u", "s", "问题", buildContextParams(0, 0, 0, "gpt-4o", 0, []float64{1}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ctx.History) > 0 || len(ctx.RecallHistory) > 0 || len(ctx.Memories) > 0 || ctx.RetrievalBudget != 0 || ctx.HistoryBudget != 0 || repo.vecCalls != 0 {
		t.Fatalf("零预算回填: %+v", ctx)
	}
}
