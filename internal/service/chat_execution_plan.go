package service

import (
	"solvify-agent/internal/agent"
	dto "solvify-agent/internal/model/dto/response"
)

func agentPlanToDTO(plan *agent.ExecutionPlan) *dto.ExecutionPlan {
	if plan == nil {
		return nil
	}
	out := &dto.ExecutionPlan{ID: plan.ID, Version: plan.Version, Phase: plan.Phase, Reason: plan.Reason, Round: plan.Round, Strategy: plan.Strategy, Status: plan.Status, Tasks: make([]dto.TaskProgress, 0, len(plan.Tasks))}
	for _, task := range plan.Tasks {
		out.Tasks = append(out.Tasks, dto.TaskProgress{ID: task.ID, Agent: task.Agent, Title: task.Title, DependsOn: append([]string(nil), task.DependsOn...), Status: task.Status, Summary: task.Summary, AddedVersion: task.AddedVersion})
	}
	return out
}

func upsertTaskProgress(plan *dto.ExecutionPlan, event agent.Event) {
	if plan == nil || event.TaskID == "" || (event.PlanID != "" && event.PlanID != plan.ID) || event.PlanVersion < plan.Version {
		return
	}
	if event.PlanVersion > plan.Version {
		plan.Version = event.PlanVersion
	}
	if event.Phase != "" {
		plan.Phase = event.Phase
	}
	if event.ReplanReason != "" {
		plan.Reason = event.ReplanReason
	}
	if event.Round > 0 {
		plan.Round = event.Round
	}
	progress := dto.TaskProgress{ID: event.TaskID, Agent: event.AgentName, Title: event.Title, DependsOn: append([]string(nil), event.DependsOn...), Status: event.TaskStatus, Summary: event.Detail, AddedVersion: plan.Version}
	for i := range plan.Tasks {
		if plan.Tasks[i].ID == progress.ID {
			plan.Tasks[i] = mergeTaskProgress(plan.Tasks[i], progress)
			return
		}
	}
	plan.Tasks = append(plan.Tasks, progress)
}

func mergeTaskProgress(current, update dto.TaskProgress) dto.TaskProgress {
	update.AddedVersion = current.AddedVersion
	if update.ID == "" {
		update.ID = current.ID
	}
	if update.Agent == "" {
		update.Agent = current.Agent
	}
	if update.Title == "" {
		update.Title = current.Title
	}
	if len(update.DependsOn) == 0 {
		update.DependsOn = current.DependsOn
	}
	if update.Status == "" {
		update.Status = current.Status
	}
	if update.Summary == "" {
		update.Summary = current.Summary
	}
	return update
}
