package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/cloudwego/eino/components/model"
	einoTool "github.com/cloudwego/eino/components/tool"

	"solvify-agent/pkg/eventch"
)

// resumeLegacyDeepOrchestrator 仅恢复有效期内的旧 deep-* checkpoint。
func (e *Engine) resumeLegacyDeepOrchestrator(ctx context.Context, req Request, chatModel model.ToolCallingChatModel, eventCh chan<- Event) {
	if !strings.HasPrefix(req.CheckpointID, "deep-") || len(req.ResumeData) == 0 {
		e.emitPlanError(ctx, eventCh, fmt.Errorf("旧深度任务仅支持审批恢复"))
		return
	}
	state := deepRunState{Results: make(map[string]TaskResult), Request: req}
	checkpointID := req.CheckpointID
	store := e.buildCheckpointStore(req.SessionID)
	if strings.HasPrefix(req.CheckpointID, "deep-") && len(req.ResumeData) > 0 {
		checkpointID = req.CheckpointID
		raw, found, err := store.Get(ctx, checkpointID)
		if err != nil || !found || json.Unmarshal(raw, &state) != nil || state.Results == nil || state.Request.UserID != req.UserID || state.Request.SessionID != req.SessionID {
			e.emitPlanError(ctx, eventCh, fmt.Errorf("深度任务中断点无效或已过期"))
			return
		}
		state.Request.ResumeData = req.ResumeData
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
	// 恢复后再按可信原请求重建工具，刷新后的工具选项不能改变原任务能力。
	if _, prebuilt := prebuiltToolsFromContext(ctx); !prebuilt || e.toolFactory != nil {
		sorted := e.sortedInternalTools()
		ctx = withPrebuiltTools(ctx, prebuiltToolsBundle{Sorted: sorted, Buckets: e.buildToolsByDomain(ctx, sorted, req.UserID, req.KnowledgeBaseIDs, AllToolDomains, !req.MCPDisabled, req.UserToolConfigIDs...)})
	}

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
	maps.Copy(inputs, results)
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
			maps.Copy(buckets, bundle.Buckets)
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
	var sources []SourceInfo
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
