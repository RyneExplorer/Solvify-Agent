package service

import (
	"encoding/json"
	"testing"

	"gorm.io/datatypes"

	"solvify-agent/internal/agent"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
)

func TestToolTraceIdentitySurvivesStreamAndHistory(t *testing.T) {
	var steps []dto.ReasoningStep
	for _, typ := range []string{agent.EventToolCall, agent.EventToolResult} {
		e := agent.Event{Type: typ, Title: "工具记录", Status: "running", ToolCallID: "call-a", ToolName: "grep_chunks", TaskID: "task-a", PlanID: "plan-a", PlanVersion: 2}
		if typ == agent.EventToolResult {
			e.Status, e.Detail = "error", "查询被限流"
		}
		for _, profile := range []agent.Profile{agent.ProfileDeep, agent.ProfileQuick} {
			stream := toStreamEvent(e, profile)
			if stream.ToolCallID != e.ToolCallID || stream.ToolName != e.ToolName || stream.TaskID != e.TaskID || stream.PlanID != e.PlanID || stream.PlanVersion != e.PlanVersion {
				t.Fatalf("SSE丢失工具关联信息: %+v", stream)
			}
		}
		applyReasoningStep(&steps, e)
	}
	raw, err := json.Marshal(map[string]any{"reasoning_steps": steps})
	if err != nil {
		t.Fatal(err)
	}
	history := messageResponse(entity.ChatMessage{Metadata: datatypes.JSON(raw)})
	if len(history.ReasoningSteps) != 2 {
		t.Fatalf("历史记录丢失: %+v", history)
	}
	for _, step := range history.ReasoningSteps {
		if step.ToolCallID != "call-a" || step.ToolName != "grep_chunks" || step.TaskID != "task-a" || step.PlanID != "plan-a" || step.PlanVersion != 2 {
			t.Fatalf("历史丢失工具关联信息: %+v", step)
		}
	}
	if history.ReasoningSteps[1].Status != "error" || history.ReasoningSteps[1].Detail != "查询被限流" {
		t.Fatalf("真实工具失败未保留: %+v", history.ReasoningSteps[1])
	}
}
