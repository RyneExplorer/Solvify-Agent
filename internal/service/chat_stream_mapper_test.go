package service

import (
	"encoding/json"
	"testing"

	"solvify-agent/internal/agent"
)

func TestAgentSourcesDTOConversionPreservesWireContractAndIsolation(t *testing.T) {
	input := []agent.SourceInfo{{DocumentID: "doc", KnowledgeBaseID: "kb", Title: "资料", Score: 0.9, Chunks: []agent.ChunkSource{{ID: "chunk", Quote: "引文", Content: "正文", Score: 0.8}}}}
	event := toStreamEvent(agent.Event{Type: agent.EventSources, Sources: input}, agent.ProfileDeep)
	want, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(event.Sources)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("公开引用字段发生变化: %s != %s", got, want)
	}
	event.Sources[0].Chunks[0].Content = "修改"
	if input[0].Chunks[0].Content != "正文" {
		t.Fatal("HTTP DTO 与 Agent 证据共享可变状态")
	}
	if agentSourcesToDTO(nil) != nil {
		t.Fatal("空来源不应创建空列表")
	}
}
