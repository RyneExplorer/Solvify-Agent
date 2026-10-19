package service

import (
	"solvify-agent/internal/agent"
	dto "solvify-agent/internal/model/dto/response"
)

// toStreamEvent 将 Agent 事件转换为 SSE 流式事件。
//
// 错误事件的文案在这里做**唯一的一次翻译**：agent 层只上报 Kind，面向用户的
// Title/Detail 在这里按 Kind × 模式取自 chat_error_messages.go。
// 因此快速模式的失败不可能再显示深度模式的措辞，同一句话也不会出现第二个来源。
func toStreamEvent(e agent.Event, profile agent.Profile) dto.StreamEvent {
	se := dto.StreamEvent{
		ToolCallID:   e.ToolCallID,
		ToolName:     e.ToolName,
		Type:         e.Type,
		Title:        e.Title,
		Detail:       e.Detail,
		Status:       e.Status,
		Content:      e.Content,
		Error:        e.Error,
		Done:         e.Done,
		Retryable:    e.Retryable,
		PlanID:       e.PlanID,
		PlanVersion:  e.PlanVersion,
		Phase:        e.Phase,
		ReplanReason: e.ReplanReason,
		Round:        e.Round,
		TaskID:       e.TaskID,
		ParentTaskID: e.ParentTaskID,
		AgentName:    e.AgentName,
		TaskStatus:   e.TaskStatus,
		TaskIndex:    e.TaskIndex,
		TaskTotal:    e.TaskTotal,
	}
	if e.Type == agent.EventError {
		msg := messageFor(e.Kind, profile)
		se.Title, se.Detail, se.Retryable = msg.Title, msg.Detail, msg.Retryable
	}
	if e.Type == agent.EventPlan {
		if e.Plan != nil {
			se.Plan = agentPlanToDTO(e.Plan)
		} else {
			se.Plan = &dto.ExecutionPlan{ID: e.PlanID, Version: e.PlanVersion, Phase: e.Phase, Reason: e.ReplanReason, Strategy: "sequential", Status: e.TaskStatus}
		}
	}
	if e.Type == agent.EventTaskCreated || e.Type == agent.EventTaskUpdated || e.Type == agent.EventTaskResult {
		se.Task = &dto.TaskProgress{ID: e.TaskID, Agent: e.AgentName, Title: e.Title, DependsOn: e.DependsOn, Status: e.TaskStatus, Summary: e.Detail}
	}
	if len(e.Sources) > 0 {
		se.Sources = agentSourcesToDTO(e.Sources)
	}
	// citation 事件的字段映射
	if e.Type == agent.EventCitation {
		se.CitationID = e.CitationID
		se.CitationChunkID = e.CitationChunkID
		se.CitationFileName = e.CitationFileName
		se.CitationContent = e.CitationContent
	}
	// interrupt 事件：携带 checkpointID / interruptID / info
	if e.Type == agent.EventInterrupt {
		se.CheckpointID = e.CheckpointID
		se.InterruptID = e.InterruptID
		se.InterruptInfo = e.InterruptInfo
		if e.IsClarify {
			se.Clarify = &dto.ClarifyPayload{
				Question: e.ClarifyQuestion,
				Options:  e.ClarifyOptions,
			}
		}
	}
	return se
}

func agentSourcesToDTO(sources []agent.SourceInfo) []dto.SourceInfo {
	if sources == nil {
		return nil
	}
	result := make([]dto.SourceInfo, len(sources))
	for i, source := range sources {
		result[i] = dto.SourceInfo{DocumentID: source.DocumentID, KnowledgeBaseID: source.KnowledgeBaseID, Title: source.Title, Score: source.Score}
		if source.Chunks != nil {
			result[i].Chunks = make([]dto.ChunkSource, len(source.Chunks))
			for j, chunk := range source.Chunks {
				result[i].Chunks[j] = dto.ChunkSource{ID: chunk.ID, Quote: chunk.Quote, Content: chunk.Content, Score: chunk.Score}
			}
		}
	}
	return result
}
