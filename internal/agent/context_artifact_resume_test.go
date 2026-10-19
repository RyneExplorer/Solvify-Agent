package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/eino/schema"
	"strings"
	"sync/atomic"
	"testing"
)

func TestContextArtifactQuickCheckpointAndBoundedRead(t *testing.T) {
	probe := guardProbeTool{calls: new(atomic.Int32)}
	repo := &memoryArtifactRepo{}
	var calls atomic.Int32
	var ref artifactReference
	cm := &deepTestModel{respond: func(_ context.Context, messages []*schema.Message) (*schema.Message, error) {
		switch calls.Add(1) {
		case 1:
			return peCall("guard_probe", map[string]any{}), nil
		case 2:
			found := false
			for _, msg := range messages {
				if msg.Role == schema.Tool && json.Unmarshal([]byte(msg.Content), &ref) == nil && ref.ArtifactID != "" {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("工具结果未变为可读引用")
			}
			return peCall(clarifyToolName, map[string]any{"question": "选择口径"}), nil
		case 3:
			found := false
			for _, msg := range messages {
				if msg.Role == schema.Tool && strings.Contains(msg.Content, ref.ArtifactID) {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("恢复丢失产物引用")
			}
			return peCall(artifactReadToolName, map[string]any{"artifact_id": ref.ArtifactID, "offset": 0, "limit": 100}), nil
		case 4:
			found := false
			for _, msg := range messages {
				if msg.Role == schema.Tool && strings.Contains(msg.Content, `"next_offset":100`) {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("未读取原文片段")
			}
			return schema.AssistantMessage("使用恢复的证据回答", nil), nil
		default:
			return nil, fmt.Errorf("出现非预期额外模型调用")
		}
	}}
	req := Request{Profile: ProfileQuick, UserID: "u", SessionID: "s", ModelID: "artifact-resume", ModelName: "gpt-4o", MaxContextLength: 64000}
	first := guardTestEngine()
	first.WithArtifactRepo(repo)
	events := collectGuardEvents(t, mustExecute(t, guardTestContext(probe), first, req, cm))
	var interrupt Event
	for _, ev := range events {
		if ev.Type == EventError {
			t.Fatal(ev.Error)
		}
		if ev.Type == EventInterrupt {
			interrupt = ev
		}
	}
	if interrupt.CheckpointID == "" || probe.calls.Load() != 1 {
		t.Fatal("未按预期中断")
	}
	other := guardTestEngine()
	other.WithArtifactRepo(repo)
	raw, found, err := first.buildCheckpointStore(req.SessionID).Get(context.Background(), interrupt.CheckpointID)
	if err != nil || !found {
		t.Fatal("缺少checkpoint")
	}
	if err = other.buildCheckpointStore(req.SessionID).Set(context.Background(), interrupt.CheckpointID, raw); err != nil {
		t.Fatal(err)
	}
	req.CheckpointID = interrupt.CheckpointID
	req.ResumeData = map[string]any{interrupt.InterruptID: "继续"}
	done := false
	for _, ev := range collectGuardEvents(t, mustExecute(t, guardTestContext(probe), other, req, cm)) {
		if ev.Type == EventError {
			t.Fatal(ev.Error)
		}
		done = done || ev.Type == EventDone
	}
	if !done || calls.Load() != 4 || probe.calls.Load() != 1 {
		t.Fatal("恢复重执行原工具或未完成", calls.Load(), probe.calls.Load())
	}
}
