package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReplannerContextIsCompactAndKeepsTrustedFacts(t *testing.T) {
	long := strings.Repeat("唯一研究正文", 2000)
	s := &peState{Request: Request{ModelName: "gpt-4o"}, Plan: ExecutionPlan{Version: 3, Tasks: []TaskSpec{{ID: "a", Summary: long, Status: TaskSuccess}}}, Results: map[string]TaskResult{
		"a":      {TaskID: "a", Agent: "knowledge_research", Status: TaskSuccess, Summary: long, Evidence: []AgentEvidence{{ID: "e", Summary: long, Sources: []SourceInfo{{Title: "来源", Chunks: []ChunkSource{{ID: "chunk", Content: long}}}}}}},
		"final":  {TaskID: "final", Agent: "synthesis_review", Summary: "最终回答", Status: TaskSuccess},
		"denied": {TaskID: "denied", Agent: "document_operation", Status: TaskError, Error: "审批拒绝"},
	}, Denied: true, RetryUsed: true}
	projected := replannerContext(s)
	raw, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > len(long) || strings.Contains(string(raw), "ExecutedSteps") {
		t.Fatal("模型重复携带完整结果")
	}
	if projected.Plan.Version != 3 || !projected.Denied || !projected.RetryUsed || projected.Results["denied"].Error != "审批拒绝" || projected.Results["final"].Summary != "最终回答" || projected.Results["a"].Evidence[0].Sources[0].Title != "来源" {
		t.Fatal("丢失可信状态/引用")
	}
	if s.Plan.Tasks[0].Summary != long || s.Results["a"].Evidence[0].Sources[0].Chunks[0].Content != long {
		t.Fatal("修改checkpoint原始状态")
	}
}
