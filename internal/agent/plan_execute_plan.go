package agent

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
)

// pePlan 的 Batch 固定为本轮执行批次，供原生 Replanner 记录 ExecutedStep。
type pePlan struct {
	ExecutionPlan
	Batch []TaskSpec `json:"-"`
}

func (p *pePlan) MarshalJSON() ([]byte, error)    { return json.Marshal(p.ExecutionPlan) }
func (p *pePlan) UnmarshalJSON(data []byte) error { return json.Unmarshal(data, &p.ExecutionPlan) }
func (p *pePlan) FirstStep() string {
	batch := p.Batch
	if len(batch) == 0 {
		batch = p.ReadyTasks()
		if p.Strategy != "parallel" && len(batch) > 1 {
			batch = batch[:1]
		}
	}
	data, _ := json.Marshal(batch)
	return string(data)
}

// peState 只存业务数据；模型、工具和互斥锁由每次请求重新装配。
type peState struct {
	Request   Request
	Plan      ExecutionPlan
	Results   map[string]TaskResult
	Batch     []TaskSpec
	Round     int
	Final     string
	RetryUsed bool
	Unsafe    map[string]bool
	Denied    bool
	Blocked   bool
	Failures  map[string]string
}

type peChildState struct {
	TaskID string
	Data   []byte
}

func init() {
	schema.RegisterName[*pePlan]("solvify_pe_plan_v1")
	schema.RegisterName[*peState]("solvify_pe_state_v1")
	schema.RegisterName[*peChildState]("solvify_pe_child_v1")
	schema.RegisterName[Event]("solvify_pe_event_v1")
	schema.RegisterName[map[string]any]("solvify_pe_map_v1")
}

func (r *peRuntime) installPlan(s *peState, next *pePlan) error {
	p := clonePEPlan(next.ExecutionPlan)
	if !r.engine.cfg.DeepParallelEnabled && p.Strategy == "parallel" {
		p.Strategy = "sequential"
	}
	first := s.Plan.ID == ""
	if first {
		p.ID, p.Goal, p.Version = "plan-"+uuid.NewString(), s.Request.Query, 1
	} else {
		p.ID, p.Goal, p.Version = s.Plan.ID, s.Plan.Goal, s.Plan.Version+1
	}
	p.Status, p.Phase, p.Round = TaskRunning, "planning", s.Round
	if !first {
		p.Phase = "replanning"
		if strings.TrimSpace(p.Reason) == "" {
			return fmt.Errorf("重规划必须说明调整原因")
		}
	}
	old := make(map[string]TaskSpec)
	for _, task := range s.Plan.Tasks {
		old[task.ID] = task
	}
	seen := make(map[string]bool)
	var active []TaskSpec
	var latestSynthesis string
	var latestSynthesisVersion int
	newResearch := false
	for i := range p.Tasks {
		t := &p.Tasks[i]
		if seen[t.ID] {
			return fmt.Errorf("任务 ID 重复: %s", t.ID)
		}
		seen[t.ID] = true
		if strings.TrimSpace(t.Title) == "" {
			return fmt.Errorf("任务标题不能为空")
		}
		if t.Agent == "document_operation" && !r.document {
			return fmt.Errorf("未配置文档操作工具")
		}
		if t.Agent == "web_research" && !r.external {
			return fmt.Errorf("未配置外部工具")
		}
		if previous, ok := old[t.ID]; ok {
			if previous.Status == TaskSuccess || previous.Status == TaskError || previous.Status == TaskCanceled {
				if previous.Agent != t.Agent || previous.Title != t.Title || previous.Description != t.Description || !slices.Equal(previous.DependsOn, t.DependsOn) {
					return fmt.Errorf("已结束任务 %s 不可修改，请创建新任务", t.ID)
				}
				*t = previous
			} else {
				if t.Agent != "synthesis_review" && (s.Denied || s.Blocked) && (previous.Agent != t.Agent || previous.Description != t.Description || !slices.Equal(previous.DependsOn, t.DependsOn)) {
					return fmt.Errorf("审批拒绝或权限错误后不可修改研究任务以绕过限制")
				}
				t.AddedVersion = previous.AddedVersion
				t.Status = TaskPending
				t.Summary = ""
				t.ResultRef = ""
			}
		} else {
			t.AddedVersion = p.Version
			t.Status = TaskPending
			t.Summary = ""
			t.ResultRef = ""
			if t.Agent != "synthesis_review" {
				newResearch = true
			}
			if s.Denied && (t.Agent == "web_research" || t.Agent == "document_operation") {
				return fmt.Errorf("审批已拒绝，不允许重新委派外部操作")
			}
		}
		if t.Status != TaskCanceled && t.Status != TaskError {
			active = append(active, *t)
			if t.Agent == "synthesis_review" && (latestSynthesis == "" || t.AddedVersion >= latestSynthesisVersion) {
				latestSynthesis = t.ID
				latestSynthesisVersion = t.AddedVersion
			}
		}
	}
	for _, previous := range s.Plan.Tasks {
		if !seen[previous.ID] {
			if previous.Status != TaskSuccess && previous.Status != TaskError && previous.Status != TaskCanceled {
				previous.Status = TaskCanceled
			}
			p.Tasks = append(p.Tasks, previous)
		}
	}
	// 历史终态任务不占新增任务预算，但它们仍可作为合法依赖。
	count := 0
	for _, t := range active {
		if t.Status != TaskSuccess {
			count++
		}
	}
	if count > deepMaxPlanTasks(r.engine.cfg) {
		return fmt.Errorf("有效任务数超过上限")
	}
	if err := p.Validate(0); err != nil {
		return err
	}
	if latestSynthesis == "" {
		return fmt.Errorf("计划缺少汇总任务")
	}
	byID := make(map[string]TaskSpec)
	for _, t := range p.Tasks {
		byID[t.ID] = t
	}
	ancestors := make(map[string]bool)
	var walk func(string)
	walk = func(id string) {
		for _, dep := range byID[id].DependsOn {
			if !ancestors[dep] {
				ancestors[dep] = true
				walk(dep)
			}
		}
	}
	walk(latestSynthesis)
	for _, t := range p.Tasks {
		if t.Status == TaskCanceled || t.Status == TaskError {
			continue
		}
		if t.Agent != "synthesis_review" && !ancestors[t.ID] {
			return fmt.Errorf("汇总任务未覆盖研究任务 %s", t.ID)
		}
		for _, dep := range t.DependsOn {
			if byID[dep].Status == TaskError || byID[dep].Status == TaskCanceled {
				return fmt.Errorf("任务依赖已失败或取消: %s", dep)
			}
		}
	}
	retryUsed := s.RetryUsed
	if newResearch {
		if s.Blocked || s.Denied {
			return fmt.Errorf("审批拒绝或权限错误后不可补充研究")
		}
		for id, result := range s.Results {
			if result.Status == TaskError && s.Unsafe[id] {
				return fmt.Errorf("副作用操作失败后不可自动重试或替代")
			}
		}
		for id, result := range s.Results {
			if result.Status == TaskError {
				if s.Unsafe[id] || s.RetryUsed || s.Blocked || s.Denied {
					return fmt.Errorf("任务失败后不可再次自动研究")
				}
				retryUsed = true
				break
			}
		}
	}
	if p.Strategy == "direct" && (len(active) != 1 || active[0].Agent != "synthesis_review") {
		return fmt.Errorf("直接执行只能包含汇总任务")
	}
	if len(p.ReadyTasks()) == 0 {
		return fmt.Errorf("新计划没有可执行任务")
	}
	s.Plan = p
	s.RetryUsed = retryUsed
	return nil
}

func clonePEPlan(plan ExecutionPlan) ExecutionPlan {
	plan.Tasks = append([]TaskSpec(nil), plan.Tasks...)
	for i := range plan.Tasks {
		plan.Tasks[i].DependsOn = append([]string(nil), plan.Tasks[i].DependsOn...)
	}
	return plan
}

func validatePECompletion(s *peState, response string) error {
	if strings.TrimSpace(response) == "" {
		return fmt.Errorf("最终答复为空")
	}
	var final *TaskSpec
	for i := range s.Plan.Tasks {
		t := &s.Plan.Tasks[i]
		if t.Status == TaskPending || t.Status == TaskRunning || t.Status == TaskWaiting {
			return fmt.Errorf("任务尚未完成，不能提前结束")
		}
		if t.Agent == "synthesis_review" && t.Status == TaskSuccess && (final == nil || t.AddedVersion >= final.AddedVersion) {
			final = t
		}
	}
	if final == nil || strings.TrimSpace(s.Results[final.ID].Summary) == "" {
		return fmt.Errorf("缺少有效汇总结果")
	}
	ancestors := map[string]bool{}
	var walk func(string)
	walk = func(id string) {
		for _, dep := range taskByID(s.Plan, id).DependsOn {
			if !ancestors[dep] {
				ancestors[dep] = true
				walk(dep)
			}
		}
	}
	walk(final.ID)
	for _, t := range s.Plan.Tasks {
		if t.Agent != "synthesis_review" && t.Status == TaskSuccess && !ancestors[t.ID] {
			return fmt.Errorf("汇总未覆盖最新研究")
		}
	}
	return nil
}
