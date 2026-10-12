package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"solvify-agent/internal/model/entity"
)

func TestRecallSelectsRankBeforeTimeAndSkipsOversize(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	old := entity.ChatMessage{ID: "old", Role: "user", Content: "较旧候选", CreatedAt: base}
	relevant := entity.ChatMessage{ID: "new", Role: "user", Content: "更相关候选", CreatedAt: base.Add(time.Hour)}
	oversized := entity.ChatMessage{ID: "big", Content: strings.Repeat("超长", 3000), CreatedAt: base}
	budget := recallMessageCost(relevant, "gpt-4o")
	selected := buildRecallContainer(nil, []entity.ChatMessage{relevant, old}, budget, "gpt-4o")
	if len(selected) != 1 || selected[0].ID != "new" {
		t.Fatalf("按时间而非排名选择: %+v", selected)
	}
	selected = buildRecallContainer(nil, []entity.ChatMessage{oversized, relevant}, budget, "gpt-4o")
	if len(selected) != 1 || selected[0].ID != "new" {
		t.Fatal("超长首项阻止了后续候选")
	}
	selected = buildRecallContainer([]entity.ChatMessage{old, relevant}, []entity.ChatMessage{relevant, old}, 1000, "gpt-4o")
	if len(selected) != 2 || selected[0].ID != "old" || selected[1].ID != "new" {
		t.Fatal("展示未按时间或重复未消除")
	}
}
func TestRecallFiltersCurrentAndRecentBeforeSelection(t *testing.T) {
	base := time.Now()
	current := entity.ChatMessage{ID: "current", Role: "user", Content: "本轮", CreatedAt: base}
	recent := entity.ChatMessage{ID: "recent", Role: "user", Content: "近期", CreatedAt: base.Add(-time.Second)}
	answer := entity.ChatMessage{ID: "answer", Role: "assistant", Content: "答", CreatedAt: base}
	old := entity.ChatMessage{ID: "old", Role: "user", Content: "旧事实", CreatedAt: base.Add(-time.Hour)}
	repo := &recallTestMessageRepo{recent: []entity.ChatMessage{recent, answer, current}, vecHits: []entity.ChatMessage{current, recent, old}}
	params := buildContextParams(1000, 0, 0, "gpt-4o", 0, []float64{1})
	params.CurrentMessageID = "current"
	got, err := recallTestService(repo).BuildContext(context.Background(), "u", "s", "本轮", params, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.RecallHistory) != 1 || got.RecallHistory[0].ID != "old" {
		t.Fatalf("召回过滤错误: %+v", got.RecallHistory)
	}
	for _, m := range got.History {
		if m.ID == "current" {
			t.Fatal("本轮重复消耗历史额度")
		}
	}
}
