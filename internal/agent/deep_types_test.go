package agent

import "testing"

func TestExecutionPlanValidateAndReadyTasks(t *testing.T) {
	plan := ExecutionPlan{ID: "p1", Strategy: "parallel", Tasks: []TaskSpec{
		{ID: "a", Agent: "knowledge_research", Status: TaskSuccess},
		{ID: "b", Agent: "web_research", Status: TaskPending},
		{ID: "c", Agent: "synthesis_review", DependsOn: []string{"a", "b"}, Status: TaskPending},
	}}
	if err := plan.Validate(4); err != nil {
		t.Fatal(err)
	}
	ready := plan.ReadyTasks()
	if len(ready) != 1 || ready[0].ID != "b" {
		t.Fatalf("ready=%v", ready)
	}
	plan.Tasks[1].Status = TaskSuccess
	ready = plan.ReadyTasks()
	if len(ready) != 1 || ready[0].ID != "c" {
		t.Fatalf("ready after dependency=%v", ready)
	}
}

func TestExecutionPlanRejectsCycle(t *testing.T) {
	plan := ExecutionPlan{ID: "p1", Strategy: "parallel", Tasks: []TaskSpec{
		{ID: "a", Agent: "knowledge_research", DependsOn: []string{"b"}},
		{ID: "b", Agent: "web_research", DependsOn: []string{"a"}},
	}}
	if err := plan.Validate(4); err == nil {
		t.Fatal("expected cycle validation error")
	}
}

func TestExecutionPlanRejectsUnknownAgentAndStrategy(t *testing.T) {
	plan := ExecutionPlan{ID: "p1", Strategy: "parallel", Tasks: []TaskSpec{{ID: "a", Agent: "unknown", Status: TaskPending}}}
	if err := plan.Validate(4); err == nil {
		t.Fatal("expected unknown agent validation error")
	}
	plan.Tasks[0].Agent = "knowledge_research"
	plan.Strategy = "branching"
	if err := plan.Validate(4); err == nil {
		t.Fatal("expected strategy validation error")
	}
}
