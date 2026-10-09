package service

import (
	"encoding/json"
	"testing"

	"gorm.io/datatypes"

	"solvify-agent/internal/agent"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
)

func TestExecutionPlanStreamAndHistoryRoundTrip(t *testing.T) {
	plan := &agent.ExecutionPlan{ID: "p", Version: 2, Phase: "executing", Reason: "补充验证", Round: 3, Strategy: "parallel", Status: agent.TaskRunning, Tasks: []agent.TaskSpec{
		{ID: "research", Agent: "knowledge_research", Title: "研究", Status: agent.TaskSuccess},
		{ID: "final", Agent: "synthesis_review", Title: "汇总", Status: agent.TaskPending, DependsOn: []string{"research"}},
	}}
	stream := toStreamEvent(agent.Event{Type: agent.EventPlan, PlanID: plan.ID, PlanVersion: plan.Version, Phase: plan.Phase, ReplanReason: plan.Reason, Plan: plan}, agent.ProfileDeep)
	if stream.Plan == nil || stream.Plan.Strategy != "parallel" || len(stream.Plan.Tasks) != 2 {
		t.Fatalf("计划事件不完整: %+v", stream)
	}
	if stream.PlanVersion != 2 || stream.Phase != "executing" || stream.ReplanReason != "补充验证" {
		t.Fatalf("SSE 丢失重规划信息: %+v", stream)
	}
	upsertTaskProgress(stream.Plan, agent.Event{PlanVersion: 2, TaskID: "final", TaskStatus: agent.TaskSuccess, Detail: "汇总结果"})
	stream.Plan.Status = agent.TaskSuccess
	raw, err := json.Marshal(map[string]any{"execution_plan": stream.Plan})
	if err != nil {
		t.Fatal(err)
	}
	response := messageResponse(entity.ChatMessage{Metadata: datatypes.JSON(raw)})
	if response.ExecutionPlan == nil {
		t.Fatal("历史消息丢失任务计划")
	}
	if response.ExecutionPlan.Version != 2 || response.ExecutionPlan.Phase != "executing" || response.ExecutionPlan.Reason != "补充验证" || response.ExecutionPlan.Round != 3 {
		t.Fatalf("历史丢失重规划信息: %+v", response.ExecutionPlan)
	}
	final := response.ExecutionPlan.Tasks[1]
	if final.ID != "final" || final.Title != "汇总" || final.Summary != "汇总结果" || len(final.DependsOn) != 1 || final.Status != agent.TaskSuccess {
		t.Fatalf("历史任务状态丢失: %+v", final)
	}
	pcRaw, _ := json.Marshal(entity.PendingCheckpointData{CheckpointID: "deep-p", ExecutionPlan: datatypes.JSON(mustMarshal(stream.Plan))})
	session := sessionResponse(entity.ChatSession{PendingCheckpoint: datatypes.JSON(pcRaw)})
	if session.PendingCheckpoint == nil || session.PendingCheckpoint.ExecutionPlan == nil || len(session.PendingCheckpoint.ExecutionPlan.Tasks) != 2 {
		t.Fatal("审批刷新后丢失计划")
	}
	if session.PendingCheckpoint.ExecutionPlan.Version != 2 || session.PendingCheckpoint.ExecutionPlan.Reason != "补充验证" {
		t.Fatal("审批刷新后丢失重规划信息")
	}
}

func TestTaskProgressRejectsStaleVersionsAndPreservesRecords(t *testing.T) {
	plan := &dto.ExecutionPlan{ID: "p", Version: 2, Tasks: []dto.TaskProgress{
		{ID: "completed", Title: "研究", Status: agent.TaskSuccess, Summary: "已有结果", AddedVersion: 1},
		{ID: "failed", Status: agent.TaskError, Summary: "研究失败", AddedVersion: 1},
		{ID: "removed", Status: agent.TaskCanceled, AddedVersion: 1},
	}}
	for _, event := range []agent.Event{
		{PlanID: "p", PlanVersion: 1, TaskID: "completed", TaskStatus: agent.TaskRunning},
		{PlanID: "other", PlanVersion: 2, TaskID: "completed", TaskStatus: agent.TaskRunning},
		{PlanID: "p", TaskID: "completed", TaskStatus: agent.TaskRunning},
	} {
		upsertTaskProgress(plan, event)
	}
	if plan.Tasks[0].Status != agent.TaskSuccess || plan.Tasks[0].Summary != "已有结果" {
		t.Fatalf("过期事件改写了终态: %+v", plan.Tasks[0])
	}
	upsertTaskProgress(plan, agent.Event{PlanID: "p", PlanVersion: 2, Phase: "executing", TaskID: "retry", AgentName: "web_research", Title: "替代研究", TaskStatus: agent.TaskRunning})
	if len(plan.Tasks) != 4 || plan.Tasks[3].AddedVersion != 2 || plan.Phase != "executing" || plan.Tasks[1].Status != agent.TaskError || plan.Tasks[2].Status != agent.TaskCanceled {
		t.Fatalf("增量丢失任务历史: %+v", plan)
	}
}

func TestLegacyExecutionPlanHistoryWithoutVersion(t *testing.T) {
	message := messageResponse(entity.ChatMessage{Metadata: datatypes.JSON(`{"execution_plan":{"id":"old","strategy":"sequential","status":"success","tasks":[{"id":"research","agent":"knowledge_research","title":"研究","status":"success"}]}}`)})
	if message.ExecutionPlan == nil || message.ExecutionPlan.Version != 0 || len(message.ExecutionPlan.Tasks) != 1 {
		t.Fatalf("旧历史计划无法回显: %+v", message.ExecutionPlan)
	}
}
