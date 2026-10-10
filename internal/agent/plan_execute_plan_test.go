package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestPlanExecuteSupplementResearchAfterSynthesis(t *testing.T) {
	var research atomic.Int32
	model := peModel(parallelTestPlan, &research)
	base := model.respond
	var supplemented bool
	model.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		if strings.Contains(messages[0].Content, "你是 Replanner") && !supplemented {
			text := messages[len(messages)-1].Content
			var state struct {
				Plan    ExecutionPlan
				Results map[string]TaskResult
			}
			index := strings.Index(text, "执行状态：")
			if index >= 0 && json.Unmarshal([]byte(text[index+len("执行状态："):]), &state) == nil && state.Results["final"].Status == TaskSuccess {
				supplemented = true
				state.Plan.Reason = "补充研究并重新汇总"
				// 省略成功任务时，服务器仍保留历史和结果。
				state.Plan.Tasks = []TaskSpec{
					{ID: "extra", Agent: "knowledge_research", Title: "补充研究"},
					{ID: "latest", Agent: "synthesis_review", Title: "最新汇总", DependsOn: []string{"a", "b", "extra"}},
				}
				return peCall("plan", state.Plan), nil
			}
		}
		return base(ctx, messages)
	}
	events := collectPEEvents(t, deepTestContext(t), deepTestEngine(), Request{Query: "比较并校验", UserID: "u", SessionID: "s"}, model)
	var final *ExecutionPlan
	done := false
	for _, event := range events {
		if event.Type == EventError {
			t.Fatalf("补充研究失败: %+v", event)
		}
		if event.Plan != nil {
			final = event.Plan
		}
		if event.Type == EventDone {
			done = true
		}
	}
	if !done || research.Load() != 3 || final == nil || len(final.Tasks) != 5 || final.Version < 4 {
		t.Fatalf("补充闭环不完整: research=%d plan=%+v events=%+v", research.Load(), final, events)
	}
	for _, task := range final.Tasks {
		if task.Status != TaskSuccess {
			t.Fatalf("终态记录被改写: %+v", task)
		}
	}
}

func TestPlanExecutePlanRevisionRules(t *testing.T) {
	newState := func() (*peRuntime, *peState) {
		r := &peRuntime{engine: deepTestEngine(), external: true}
		s := &peState{Request: Request{Query: "研究"}, Results: map[string]TaskResult{}, Unsafe: map[string]bool{}, Failures: map[string]string{}}
		p := &pePlan{ExecutionPlan: ExecutionPlan{Strategy: "sequential", Tasks: []TaskSpec{
			{ID: "a", Agent: "knowledge_research", Title: "研究 A"},
			{ID: "f", Agent: "synthesis_review", Title: "汇总", DependsOn: []string{"a"}},
		}}}
		if err := r.installPlan(s, p); err != nil {
			t.Fatal(err)
		}
		return r, s
	}
	t.Run("成功不可改写且历史不占预算", func(t *testing.T) {
		r, s := newState()
		mergeTaskResult(&s.Plan, s.Results, TaskResult{TaskID: "a", Agent: "knowledge_research", Status: TaskSuccess, Summary: "原结论"})
		p := &pePlan{ExecutionPlan: s.Plan}
		p.Tasks = append([]TaskSpec(nil), s.Plan.Tasks...)
		p.Reason = "调整"
		p.Tasks[0].Title = "伪造标题"
		if err := r.installPlan(s, p); err == nil {
			t.Fatal("允许改写成功任务")
		}
		if s.Plan.Tasks[0].Title != "研究 A" || s.Plan.Version != 1 {
			t.Fatal("拒绝计划仍修改了可信状态")
		}
		p.Tasks[0].Title = "研究 A"
		p.Tasks[0].DependsOn = []string{}
		if err := r.installPlan(s, p); err != nil {
			t.Fatalf("空依赖与省略依赖语义相同，不应拒绝: %v", err)
		}
		r.engine.cfg.DeepMaxPlanTasks = 1
		p.Tasks = []TaskSpec{{ID: "latest", Agent: "synthesis_review", Title: "汇总", DependsOn: []string{"a"}}}
		if err := r.installPlan(s, p); err != nil {
			t.Fatal(err)
		}
		if s.Plan.ID == "" || s.Plan.Version != 3 || taskByID(s.Plan, "f").Status != TaskCanceled || s.Results["a"].Summary != "原结论" {
			t.Fatal("版本或历史未保留")
		}
	})
	for _, scenario := range []string{"只读失败一次重试", "副作用失败", "权限失败", "拒绝审批"} {
		t.Run(scenario, func(t *testing.T) {
			r, s := newState()
			mergeTaskResult(&s.Plan, s.Results, TaskResult{TaskID: "a", Agent: "knowledge_research", Status: TaskError, Error: "研究失败"})
			s.Batch = []TaskSpec{taskByID(s.Plan, "a")}
			s.Unsafe["a"] = scenario == "副作用失败"
			s.Blocked = scenario == "权限失败"
			s.Denied = scenario == "拒绝审批"
			retry := &pePlan{ExecutionPlan: ExecutionPlan{Strategy: "sequential", Reason: "替代研究", Tasks: []TaskSpec{
				{ID: "retry", Agent: "knowledge_research", Title: "替代研究"},
				{ID: "f2", Agent: "synthesis_review", Title: "汇总", DependsOn: []string{"retry"}},
			}}}
			err := r.installPlan(s, retry)
			if scenario != "只读失败一次重试" {
				if err == nil {
					t.Fatal("允许绕过失败限制")
				}
				return
			}
			if err != nil || !s.RetryUsed || s.Plan.Version != 2 {
				t.Fatalf("只读替代失败: %v", err)
			}
			if taskByID(s.Plan, "a").Status != TaskError || taskByID(s.Plan, "f").Status != TaskCanceled {
				t.Fatal("失败历史被清除")
			}
			mergeTaskResult(&s.Plan, s.Results, TaskResult{TaskID: "retry", Status: TaskError, Error: "再次失败"})
			s.Batch = []TaskSpec{taskByID(s.Plan, "retry")}
			retry = &pePlan{ExecutionPlan: ExecutionPlan{Strategy: "sequential", Reason: "再次替代", Tasks: []TaskSpec{
				{ID: "retry2", Agent: "knowledge_research", Title: "再次替代"},
				{ID: "f3", Agent: "synthesis_review", Title: "汇总", DependsOn: []string{"retry2"}},
			}}}
			if err := r.installPlan(s, retry); err == nil {
				t.Fatal("允许无限重试")
			}
		})
	}
}

func TestPlanExecuteRetryLimitSurvivesIntermediateBatch(t *testing.T) {
	r := &peRuntime{engine: deepTestEngine()}
	s := &peState{
		Request: Request{Query: "研究"},
		Plan: ExecutionPlan{ID: "p", Version: 3, Strategy: "sequential", Tasks: []TaskSpec{
			{ID: "failed", Agent: "knowledge_research", Title: "失败研究", Status: TaskError},
			{ID: "f", Agent: "synthesis_review", Title: "汇总", Status: TaskSuccess},
		}},
		Results:   map[string]TaskResult{"failed": {Status: TaskError}, "f": {Status: TaskSuccess}},
		Batch:     []TaskSpec{{ID: "f", Agent: "synthesis_review"}},
		RetryUsed: true,
	}
	next := &pePlan{ExecutionPlan: ExecutionPlan{Strategy: "sequential", Reason: "再次研究", Tasks: []TaskSpec{
		{ID: "retry", Agent: "knowledge_research", Title: "再次研究"},
		{ID: "final", Agent: "synthesis_review", Title: "新汇总", DependsOn: []string{"retry"}},
	}}}
	if err := r.installPlan(s, next); err == nil {
		t.Fatal("中间汇总批次绕过了只读重试次数限制")
	}
	if s.Plan.Version != 3 {
		t.Fatal("被拒绝的计划改变可信状态")
	}
}
