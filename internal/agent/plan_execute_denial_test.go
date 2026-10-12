package agent

import "testing"

func TestDeniedPlanCanSummarizeWithoutFailedDependency(t *testing.T) {
	r := &peRuntime{engine: deepTestEngine()}
	s := &peState{Denied: true, Results: map[string]TaskResult{
		"d": {TaskID: "d", Agent: "document_operation", Status: TaskError, Error: "用户拒绝审批，操作未执行"},
	}, Plan: ExecutionPlan{ID: "p", Version: 1, Strategy: "sequential", Tasks: []TaskSpec{
		{ID: "d", Agent: "document_operation", Title: "删除", Status: TaskError},
		{ID: "f", Agent: "synthesis_review", Title: "原汇总", DependsOn: []string{"d"}, Status: TaskPending},
	}}}
	bad := &pePlan{ExecutionPlan: ExecutionPlan{Strategy: "direct", Reason: "报告拒绝", Tasks: []TaskSpec{
		{ID: "summary", Agent: "synthesis_review", Title: "说明未执行", DependsOn: []string{"d"}},
	}}}
	if err := r.installPlan(s, bad); err == nil {
		t.Fatal("汇总仍能依赖失败操作")
	}
	good := &pePlan{ExecutionPlan: ExecutionPlan{Strategy: "direct", Reason: "报告拒绝", Tasks: []TaskSpec{
		{ID: "summary", Agent: "synthesis_review", Title: "说明用户拒绝，操作未执行", DependsOn: []string{}},
	}}}
	if err := r.installPlan(s, good); err != nil {
		t.Fatal(err)
	}
	ready := s.Plan.ReadyTasks()
	if len(ready) != 1 || ready[0].ID != "summary" || taskByID(s.Plan, "d").Status != TaskError || taskByID(s.Plan, "f").Status != TaskCanceled || s.Results["d"].Error == "" || !s.Denied {
		t.Fatal("合法收尾丢失拒绝事实或保留失败依赖")
	}
}
