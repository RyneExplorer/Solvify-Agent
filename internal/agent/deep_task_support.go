package agent

import (
	"context"
	"encoding/json"
	"strings"

	"solvify-agent/pkg/config"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/eventch"
)

// 深度任务共享的能力白名单、证据输入与过程事件；供当前编排和旧 checkpoint 恢复使用。
func taskToolDomains(agentName string) []ToolDomain {
	if agentName == "web_research" {
		return []ToolDomain{DomainExternal}
	}
	if agentName == "document_operation" {
		return []ToolDomain{DomainDocMgmt}
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
				result.Evidence[i].Sources = append([]SourceInfo(nil), result.Evidence[i].Sources...)
				for j := range result.Evidence[i].Sources {
					source := &result.Evidence[i].Sources[j]
					source.Chunks = append([]ChunkSource(nil), source.Chunks...)
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
	kind := apperrors.Classify(err)
	if isToolChoiceUnsupportedError(err.Error()) {
		kind = apperrors.KindToolUnsupported
	} else if kind == apperrors.KindUnknown {
		kind = apperrors.KindAgentRunFailed
	}
	eventch.Send(ctx, out, Event{Type: EventError, Kind: kind, Error: err.Error(), Status: "error", Done: true})
}

func deepMaxPlanTasks(cfg config.AgentConfig) int {
	if cfg.DeepMaxPlanTasks > 0 {
		return cfg.DeepMaxPlanTasks
	}
	return 4
}

func flattenSources(evidence []AgentEvidence) []SourceInfo {
	var out []SourceInfo
	indexes := make(map[string]int)
	chunks := make(map[string]map[string]bool)
	for _, item := range evidence {
		for _, source := range item.Sources {
			key := source.KnowledgeBaseID + "/" + source.DocumentID
			index, exists := indexes[key]
			if !exists {
				index = len(out)
				indexes[key] = index
				info := source
				info.Chunks = nil
				out = append(out, info)
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
