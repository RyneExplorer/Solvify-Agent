package agent

import ()

// SourceInfo 是 Agent 收集的文档证据，由 Service 转换为 HTTP 响应 DTO。
type SourceInfo struct {
	DocumentID      string        `json:"document_id"`
	KnowledgeBaseID string        `json:"knowledge_base_id"`
	Title           string        `json:"title"`
	Score           float64       `json:"score"`
	Chunks          []ChunkSource `json:"chunks"`
}

type ChunkSource struct {
	ID      string  `json:"id"`
	Quote   string  `json:"quote,omitempty"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
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
