package agent

import (
	"fmt"
	"strings"
)

const (
	TaskPending  = "pending"
	TaskRunning  = "running"
	TaskSuccess  = "success"
	TaskError    = "error"
	TaskCanceled = "canceled"
	TaskWaiting  = "waiting"
)

type ExecutionPlan struct {
	ID       string     `json:"id"`
	Version  int        `json:"version"`
	Phase    string     `json:"phase,omitempty"`
	Reason   string     `json:"replan_reason,omitempty"`
	Round    int        `json:"round,omitempty"`
	Goal     string     `json:"goal"`
	Strategy string     `json:"strategy"`
	Status   string     `json:"status"`
	Tasks    []TaskSpec `json:"tasks"`
}

var supportedDeepAgents = map[string]struct{}{
	"knowledge_research": {},
	"web_research":       {},
	"synthesis_review":   {},
}

type TaskSpec struct {
	ID           string   `json:"id"`
	AddedVersion int      `json:"added_version,omitempty"`
	Agent        string   `json:"agent"`
	Title        string   `json:"title"`
	Description  string   `json:"description,omitempty"`
	DependsOn    []string `json:"depends_on,omitempty"`
	Status       string   `json:"status"`
	ResultRef    string   `json:"result_ref,omitempty"`
	Summary      string   `json:"summary,omitempty"`
}

type AgentEvidence struct {
	ID         string       `json:"id"`
	AgentName  string       `json:"agent_name"`
	Summary    string       `json:"summary"`
	Confidence float64      `json:"confidence,omitempty"`
	Sources    []SourceInfo `json:"sources,omitempty"`
}

type TaskResult struct {
	TaskID    string          `json:"task_id"`
	Agent     string          `json:"agent"`
	Status    string          `json:"status"`
	Summary   string          `json:"summary,omitempty"`
	Evidence  []AgentEvidence `json:"evidence,omitempty"`
	Error     string          `json:"error,omitempty"`
	Interrupt *Event          `json:"interrupt,omitempty"`
}

// Validate 在执行前校验角色、任务标识、依赖引用和依赖环。
func (p ExecutionPlan) Validate(maxTasks int) error {
	if strings.TrimSpace(p.ID) == "" || len(p.Tasks) == 0 {
		return fmt.Errorf("计划必须包含 ID 和任务")
	}
	if maxTasks > 0 && len(p.Tasks) > maxTasks {
		return fmt.Errorf("计划任务数超过上限 %d", maxTasks)
	}
	if p.Strategy != "direct" && p.Strategy != "sequential" && p.Strategy != "parallel" {
		return fmt.Errorf("计划策略无效: %s", p.Strategy)
	}
	seen := make(map[string]TaskSpec, len(p.Tasks))
	for _, task := range p.Tasks {
		if strings.TrimSpace(task.ID) == "" || task.Agent == "" {
			return fmt.Errorf("任务 ID 和 Agent 不能为空")
		}
		if _, ok := supportedDeepAgents[task.Agent]; !ok {
			return fmt.Errorf("不支持的任务 Agent: %s", task.Agent)
		}
		if task.Status != "" && task.Status != TaskPending && task.Status != TaskRunning && task.Status != TaskSuccess && task.Status != TaskError && task.Status != TaskCanceled && task.Status != TaskWaiting {
			return fmt.Errorf("任务 %s 初始状态无效: %s", task.ID, task.Status)
		}
		if _, ok := seen[task.ID]; ok {
			return fmt.Errorf("任务 ID 重复: %s", task.ID)
		}
		seen[task.ID] = task
	}
	graph := make(map[string][]string, len(seen))
	for _, task := range p.Tasks {
		dependencies := make(map[string]bool, len(task.DependsOn))
		for _, dep := range task.DependsOn {
			if dependencies[dep] {
				return fmt.Errorf("任务 %s 依赖重复: %s", task.ID, dep)
			}
			dependencies[dep] = true
			if _, ok := seen[dep]; !ok {
				return fmt.Errorf("任务 %s 依赖不存在: %s", task.ID, dep)
			}
			graph[task.ID] = append(graph[task.ID], dep)
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("计划存在循环依赖: %s", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dep := range graph[id] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		delete(visiting, id)
		visited[id] = true
		return nil
	}
	for id := range seen {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

// ReadyTasks 返回前置任务全部成功的待执行任务。
func (p ExecutionPlan) ReadyTasks() []TaskSpec {
	status := make(map[string]string, len(p.Tasks))
	for _, task := range p.Tasks {
		status[task.ID] = task.Status
	}
	ready := make([]TaskSpec, 0)
	for _, task := range p.Tasks {
		if task.Status != "" && task.Status != TaskPending {
			continue
		}
		ok := true
		for _, dep := range task.DependsOn {
			if status[dep] != TaskSuccess {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, task)
		}
	}
	return ready
}
