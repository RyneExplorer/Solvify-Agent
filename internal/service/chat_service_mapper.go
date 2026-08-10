package service

import (
	"encoding/json"

	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/rag"
	"solvify-agent/pkg/logger"
)

// sessionResponse 转换会话响应 DTO
func sessionResponse(session entity.ChatSession) response.SessionResponse {
	resp := response.SessionResponse{
		ID:        session.ID,
		Title:     session.Title,
		ModelID:   session.ModelID,
		Status:    session.Status,
		CreatedAt: session.CreatedAt,
		UpdatedAt: session.UpdatedAt,
	}
	if pc, err := session.GetPendingCheckpoint(); err == nil && pc != nil {
		resp.PendingCheckpoint = &response.PendingCheckpointInfo{
			CheckpointID: pc.CheckpointID,
			InterruptID:  pc.InterruptID,
			Question:     pc.Question,
			ToolName:     pc.ToolName,
			IsClarify:    pc.IsClarify,
			Options:      pc.Options,
			SetAt:        pc.SetAt,
		}
	}
	return resp
}

// messageResponse 转换消息响应 DTO
func messageResponse(msg entity.ChatMessage) response.MessageResponse {
	resp := response.MessageResponse{
		ID:         msg.ID,
		SessionID:  msg.SessionID,
		Role:       msg.Role,
		Content:    msg.Content,
		ModelID:    msg.ModelID,
		SearchMode: msg.SearchMode,
		CreatedAt:  msg.CreatedAt,
	}

	if len(msg.KnowledgeBaseIDs) > 0 {
		var kbIDs []string
		if err := json.Unmarshal(msg.KnowledgeBaseIDs, &kbIDs); err != nil {
			logger.Errorf("解析 KnowledgeBaseIDs 失败, messageID=%s: %v", msg.ID, err)
		} else {
			resp.KnowledgeBaseIDs = kbIDs
		}
	}

	if len(msg.Sources) > 0 {
		var sources []response.SourceInfo
		if err := json.Unmarshal(msg.Sources, &sources); err != nil {
			logger.Errorf("解析 Sources 失败, messageID=%s: %v", msg.ID, err)
		} else {
			resp.Sources = sources
		}
	}

	if len(msg.Metadata) > 0 {
		var meta struct {
			ReasoningSteps []response.ReasoningStep `json:"reasoning_steps"`
		}
		if err := json.Unmarshal(msg.Metadata, &meta); err != nil {
			logger.Errorf("解析 Metadata 失败, messageID=%s: %v", msg.ID, err)
		} else if len(meta.ReasoningSteps) > 0 {
			resp.ReasoningSteps = meta.ReasoningSteps
		}
	}

	return resp
}

// mustMarshal 序列化 JSON，失败时记录日志并返回 "null"
func mustMarshal(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		logger.Errorf("JSON 序列化失败: %v", err)
		return []byte("null")
	}
	return data
}

// groupDocumentsToSources 将检索结果按文档分组为来源信息
func groupDocumentsToSources(docs []rag.Document) []response.SourceInfo {
	docMap := make(map[string]*response.SourceInfo)
	docOrder := make([]string, 0)
	for _, doc := range docs {
		if _, exists := docMap[doc.DocumentID]; !exists {
			docMap[doc.DocumentID] = &response.SourceInfo{
				DocumentID:      doc.DocumentID,
				KnowledgeBaseID: doc.KnowledgeBaseID,
				Title:           doc.Title,
			}
			docOrder = append(docOrder, doc.DocumentID)
		}
		docMap[doc.DocumentID].Chunks = append(docMap[doc.DocumentID].Chunks, response.ChunkSource{
			ID:      doc.ID,
			Content: doc.Content,
			Score:   doc.Score,
		})
		if doc.Score > docMap[doc.DocumentID].Score {
			docMap[doc.DocumentID].Score = doc.Score
		}
	}

	sources := make([]response.SourceInfo, 0, len(docMap))
	for _, docID := range docOrder {
		sources = append(sources, *docMap[docID])
	}
	return sources
}
