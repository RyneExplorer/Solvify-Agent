package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	einoTool "github.com/cloudwego/eino/components/tool"

	"solvify-agent/internal/model/dto/response"
	"solvify-agent/pkg/config"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/eventch"
)

// runDeepOrchestrator 仅恢复有效期内的旧 deep-* checkpoint。
func (e *Engine) runDeepOrchestrator(ctx context.Context, req Request, chatModel model.ToolCallingChatModel, eventCh chan<- Event) {
	if !strings.HasPrefix(req.CheckpointID, "deep-") || len(req.ResumeData) == 0 {
		e.emitPlanError(ctx, eventCh, fmt.Errorf("旧深度任务仅支持审批恢复"))
		return
	}
	if _, ok := prebuiltToolsFromContext(ctx); !ok {
		sorted := e.sortedInternalTools()
		ctx = withPrebuiltTools(ctx, prebuiltToolsBundle{Sorted: sorted, Buckets: e.buildToolsByDomain(ctx, sorted, req.UserID, req.KnowledgeBaseIDs, AllToolDomains, !req.MCPDisabled, req.UserToolConfigIDs...)})
	}
	state := deepRunState{Results: make(map[string]TaskResult), Request: req}
	checkpointID := req.CheckpointID
	store := e.buildCheckpointStore(req.SessionID)
	if strings.HasPrefix(req.CheckpointID, "deep-") && len(req.ResumeData) > 0 {
		checkpointID = req.CheckpointID
		raw, found, err := store.Get(ctx, checkpointID)
		if err != nil || !found || json.Unmarshal(raw, &state) != nil || state.Request.UserID != req.UserID || state.Request.SessionID != req.SessionID {
			e.emitPlanError(ctx, eventCh, fmt.Errorf("深度任务中断点无效或已过期"))
			return
		}
		state.Request.ResumeData = req.ResumeData
		state.Request.SystemPrompt = req.SystemPrompt
		state.Plan.Status = TaskRunning
		for i := range state.Plan.Tasks {
			r := state.Results[state.Plan.Tasks[i].ID]
			if r.Interrupt != nil {
				if _, ok := req.ResumeData[r.Interrupt.InterruptID]; ok {
					state.Plan.Tasks[i].Status = TaskPending
				}
			}
		}
	}
	plan := state.Plan
	if err := plan.Validate(deepMaxPlanTasks(e.cfg)); err != nil {
		e.emitPlanError(ctx, eventCh, err)
		return
	}
	e.emitPlan(ctx, eventCh, plan)
	results := state.Results
	req = state.Request

	for {
		ready := plan.ReadyTasks()
		if len(ready) == 0 {
			break
		}
		if plan.Strategy != "parallel" || !e.cfg.DeepParallelEnabled {
			ready = ready[:1]
		}
		e.runParallelTasks(ctx, req, chatModel, &plan, ready, results, eventCh)
		if ctx.Err() != nil {
			return
		}
	}
	if hasWaiting(plan) {
		plan.Status = TaskWaiting
		e.emitPlan(ctx, eventCh, plan)
		state.Plan, state.Results = plan, results
		state.Request.ResumeData = nil
		raw, err := json.Marshal(state)
		if err == nil {
			err = store.Set(ctx, checkpointID, raw)
		}
		if err != nil {
			e.emitPlanError(ctx, eventCh, fmt.Errorf("保存深度任务中断点失败: %w", err))
			return
		}
		for _, task := range plan.Tasks {
			if result := results[task.ID]; result.Status == TaskWaiting && result.Interrupt != nil {
				interrupt := *result.Interrupt
				interrupt.CheckpointID = checkpointID
				eventch.Send(ctx, eventCh, interrupt)
				return
			}
		}
	}
	if !planComplete(plan) {
		plan.Status = TaskError
		for i := range plan.Tasks {
			if plan.Tasks[i].Status == TaskPending {
				plan.Tasks[i].Status = TaskCanceled
			}
		}
		e.emitPlan(ctx, eventCh, plan)
		for _, task := range plan.Tasks {
			if result := results[task.ID]; result.Status == TaskError {
				e.emitPlanError(ctx, eventCh, fmt.Errorf("任务「%s」失败: %s", task.Title, result.Error))
				return
			}
		}
		e.emitPlanError(ctx, eventCh, fmt.Errorf("深度任务未能完成"))
		return
	}

	var final TaskResult
	for _, task := range plan.Tasks {
		if task.Agent == "synthesis_review" {
			final = results[task.ID]
		}
	}
	plan.Status = TaskSuccess
	e.emitPlan(ctx, eventCh, plan)
	if deletable, ok := store.(interface {
		Delete(context.Context, string) error
	}); ok {
		_ = deletable.Delete(ctx, checkpointID)
	}
	eventch.Send(ctx, eventCh, Event{Type: EventAnswer, Content: final.Summary, PlanID: plan.ID, TaskID: final.TaskID, AgentName: "synthesis_review"})
	var evidence []AgentEvidence
	for _, task := range plan.Tasks {
		evidence = append(evidence, results[task.ID].Evidence...)
	}
	eventch.Send(ctx, eventCh, Event{Type: EventDone, Content: final.Summary, PlanID: plan.ID, Sources: flattenSources(evidence)})
}

type deepRunState struct {
	Plan    ExecutionPlan         `json:"plan"`
	Results map[string]TaskResult `json:"results"`
	Request Request               `json:"request"`
}

func (e *Engine) runParallelTasks(ctx context.Context, req Request, chatModel model.ToolCallingChatModel, plan *ExecutionPlan, tasks []TaskSpec, results map[string]TaskResult, out chan<- Event) {
	resultCh := make(chan TaskResult, len(tasks))
	inputs := make(map[string]TaskResult, len(results))
	for id, result := range results {
		inputs[id] = result
	}
	planSnapshot := *plan
	planSnapshot.Tasks = append([]TaskSpec(nil), plan.Tasks...)
	for _, task := range tasks {
		for i := range plan.Tasks {
			if plan.Tasks[i].ID == task.ID {
				plan.Tasks[i].Status = TaskRunning
			}
		}
		e.emitTask(ctx, out, *plan, task, TaskRunning, "")
		go func(task TaskSpec) {
			resultCh <- e.executeDeepTask(ctx, req, chatModel, planSnapshot, task, inputs, out)
		}(task)
	}
	for range tasks {
		result := <-resultCh
		mergeTaskResult(plan, results, result)
		e.emitTaskResult(ctx, out, *plan, taskByID(*plan, result.TaskID), result)
	}
}

func (e *Engine) executeDeepTask(ctx context.Context, req Request, chatModel model.ToolCallingChatModel, plan ExecutionPlan, task TaskSpec, results map[string]TaskResult, out chan<- Event) TaskResult {
	childReq := req
	childReq.Profile = ProfileDeep
	childReq.TaskID = task.ID
	childReq.AgentName = task.Agent
	childReq.ToolDomains = taskToolDomains(task.Agent)
	childReq.ResumeData, childReq.CheckpointID = nil, ""
	if previous := results[task.ID]; previous.Interrupt != nil {
		childReq.CheckpointID = previous.Interrupt.CheckpointID
		childReq.ResumeData = req.ResumeData
	}
	if task.Description != "" {
		childReq.Query = req.Query + "\n当前研究任务：" + task.Description
	}
	if len(task.DependsOn) > 0 {
		inputsTask := task
		if task.Agent == "synthesis_review" {
			inputsTask.DependsOn = nil
			for _, planned := range plan.Tasks {
				if results[planned.ID].Status == TaskSuccess {
					inputsTask.DependsOn = append(inputsTask.DependsOn, planned.ID)
				}
			}
		}
		childReq.Query = buildTaskQuery(childReq.Query, inputsTask, results)
	}
	// 知识检索工具收集本次调用的来源；每个研究任务使用独立实例，避免互相混入引用。
	if task.Agent == "knowledge_research" {
		if bundle, ok := prebuiltToolsFromContext(ctx); ok {
			buckets := make(map[ToolDomain][]einoTool.BaseTool, len(bundle.Buckets))
			for domain, tools := range bundle.Buckets {
				buckets[domain] = tools
			}
			buckets[DomainKnowledge] = nil
			for _, entry := range bundle.Sorted {
				if entry.Domain == DomainKnowledge {
					buckets[DomainKnowledge] = append(buckets[DomainKnowledge], entry.Build(ctx, req.UserID, req.KnowledgeBaseIDs))
				}
			}
			ctx = withPrebuiltTools(ctx, prebuiltToolsBundle{Sorted: bundle.Sorted, Buckets: buckets})
		}
	}

	childEvents := make(chan Event, 100)
	go func() { defer close(childEvents); e.runAgent(ctx, childReq, chatModel, childEvents) }()
	var answer strings.Builder
	var sources []response.SourceInfo
	status := TaskSuccess
	var taskErr string
	var interrupt *Event
	var fallback string
	for ev := range childEvents {
		ev.PlanID, ev.TaskID, ev.AgentName = plan.ID, task.ID, task.Agent
		if ev.Type == EventAnswer {
			answer.WriteString(ev.Content)
			sources = append(sources, ev.Sources...)
		} else if ev.Type == EventDone {
			fallback = ev.Content
			sources = append(sources, ev.Sources...)
		} else if ev.Type == EventInterrupt {
			status = TaskWaiting
			taskErr = ev.Detail
			eventCopy := ev
			interrupt = &eventCopy
		} else if ev.Type == EventError {
			status = TaskError
			taskErr = ev.Error
		}
		if ev.Type != EventDone && ev.Type != EventAnswer && ev.Type != EventSources && ev.Type != EventCitation && ev.Type != EventInterrupt && ev.Type != EventError {
			eventch.Send(ctx, out, ev)
		}
	}
	if ctx.Err() != nil {
		status = TaskCanceled
	}
	if answer.Len() == 0 {
		answer.WriteString(fallback)
	}
	if status == TaskSuccess && strings.TrimSpace(answer.String()) == "" {
		status = TaskError
		if taskErr == "" {
			taskErr = "子 Agent 未返回有效结果"
		}
	}
	result := TaskResult{TaskID: task.ID, Agent: task.Agent, Status: status, Summary: strings.TrimSpace(answer.String()), Error: taskErr, Interrupt: interrupt}
	if len(sources) > 0 {
		result.Evidence = []AgentEvidence{{ID: task.ID + "-evidence", AgentName: task.Agent, Summary: result.Summary, Sources: sources}}
	}
	return result
}

func taskToolDomains(agentName string) []ToolDomain {
	if agentName == "web_research" {
		return []ToolDomain{DomainExternal}
	}
	if agentName == "synthesis_review" {
		return []ToolDomain{}
	}
	return []ToolDomain{DomainKnowledge}
}

const synthesisInputLimit = 6000

func buildTaskQuery(query string, task TaskSpec, results map[string]TaskResult) string {
	inputs := make([]TaskResult, 0, len(task.DependsOn))
	budget := synthesisInputLimit / max(1, len(task.DependsOn))
	for _, dep := range task.DependsOn {
		if result, ok := results[dep]; ok && strings.TrimSpace(result.Summary) != "" {
			result.Summary = truncateTaskSummary(result.Summary, budget)
			result.Evidence = append([]AgentEvidence(nil), result.Evidence...)
			for i := range result.Evidence {
				result.Evidence[i].Summary = ""
				result.Evidence[i].Sources = append([]response.SourceInfo(nil), result.Evidence[i].Sources...)
				for j := range result.Evidence[i].Sources {
					source := &result.Evidence[i].Sources[j]
					source.Chunks = append([]response.ChunkSource(nil), source.Chunks...)
					for k := range source.Chunks {
						source.Chunks[k].Content = ""
						source.Chunks[k].Quote = ""
					}
				}
			}
			inputs = append(inputs, result)
		}
	}
	if len(inputs) == 0 {
		return query
	}
	data, _ := json.Marshal(inputs)
	return query + "\n\n前置研究结果（JSON 证据数据）：\n" + string(data)
}

func truncateTaskSummary(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func mergeTaskResult(plan *ExecutionPlan, results map[string]TaskResult, result TaskResult) {
	results[result.TaskID] = result
	for i := range plan.Tasks {
		if plan.Tasks[i].ID == result.TaskID {
			plan.Tasks[i].Status = result.Status
			plan.Tasks[i].ResultRef = result.TaskID
			plan.Tasks[i].Summary = truncateTaskSummary(result.Summary, 500)
			if result.Error != "" {
				plan.Tasks[i].Summary = result.Error
			}
			return
		}
	}
}

func taskByID(plan ExecutionPlan, id string) TaskSpec {
	for _, task := range plan.Tasks {
		if task.ID == id {
			return task
		}
	}
	return TaskSpec{ID: id}
}

func (e *Engine) emitPlan(ctx context.Context, out chan<- Event, plan ExecutionPlan) {
	planCopy := plan
	planCopy.Tasks = append([]TaskSpec(nil), plan.Tasks...)
	eventch.Send(ctx, out, Event{Type: EventPlan, PlanID: plan.ID, Status: plan.Status, TaskStatus: plan.Status, TaskTotal: len(plan.Tasks), Detail: plan.Goal, Plan: &planCopy})
	for i, task := range plan.Tasks {
		eventch.Send(ctx, out, Event{Type: EventTaskCreated, PlanID: plan.ID, TaskID: task.ID, AgentName: task.Agent, TaskStatus: task.Status, TaskIndex: i + 1, TaskTotal: len(plan.Tasks), Title: task.Title, Detail: task.Summary, DependsOn: task.DependsOn})
	}
}

func (e *Engine) emitTask(ctx context.Context, out chan<- Event, plan ExecutionPlan, task TaskSpec, status, summary string) {
	eventch.Send(ctx, out, Event{Type: EventTaskUpdated, PlanID: plan.ID, TaskID: task.ID, AgentName: task.Agent, TaskStatus: status, Title: task.Title, Detail: summary, DependsOn: task.DependsOn})
}

func (e *Engine) emitTaskResult(ctx context.Context, out chan<- Event, plan ExecutionPlan, task TaskSpec, result TaskResult) {
	detail := truncateTaskSummary(result.Summary, 500)
	if result.Error != "" {
		detail = result.Error
	}
	eventch.Send(ctx, out, Event{Type: EventTaskResult, PlanID: plan.ID, TaskID: task.ID, AgentName: task.Agent, TaskStatus: result.Status, Title: task.Title, Detail: detail, Error: result.Error, DependsOn: task.DependsOn})
}

// emitPlanError 上报深度编排失败。
// 只报种类，不报文案 —— 此前 Detail 直接放 err.Error()，把原始英文错泄漏给用户；
// 面向用户的措辞统一由 service 层按种类 × 模式给出（见 internal/service/chat_error_messages.go）。
func (e *Engine) emitPlanError(ctx context.Context, out chan<- Event, err error) {
	eventch.Send(ctx, out, Event{Type: EventError, Kind: apperrors.KindAgentRunFailed, Error: err.Error(), Status: "error", Done: true})
}

func planComplete(plan ExecutionPlan) bool {
	for _, task := range plan.Tasks {
		if task.Status != TaskSuccess {
			return false
		}
	}
	return true
}

func hasWaiting(plan ExecutionPlan) bool {
	for _, task := range plan.Tasks {
		if task.Status == TaskWaiting {
			return true
		}
	}
	return false
}

func deepMaxPlanTasks(cfg config.AgentConfig) int {
	if cfg.DeepMaxPlanTasks > 0 {
		return cfg.DeepMaxPlanTasks
	}
	return 4
}

func flattenSources(evidence []AgentEvidence) []response.SourceInfo {
	var out []response.SourceInfo
	indexes := make(map[string]int)
	chunks := make(map[string]map[string]bool)
	for _, item := range evidence {
		for _, source := range item.Sources {
			key := source.KnowledgeBaseID + "/" + source.DocumentID
			index, exists := indexes[key]
			if !exists {
				index = len(out)
				indexes[key] = index
				copy := source
				copy.Chunks = nil
				out = append(out, copy)
				chunks[key] = make(map[string]bool)
			}
			for _, chunk := range source.Chunks {
				if !chunks[key][chunk.ID] {
					out[index].Chunks = append(out[index].Chunks, chunk)
					chunks[key][chunk.ID] = true
				}
			}
			if source.Score > out[index].Score {
				out[index].Score = source.Score
			}
		}
	}
	return out
}
