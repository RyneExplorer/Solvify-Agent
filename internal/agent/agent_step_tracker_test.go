package agent

import (
	"strings"
	"sync"
	"testing"

	"solvify-agent/internal/observability"
)

// stepCaptureRecorder 只捕获 RecordAgentStep，其余方法由嵌入的接口占位。
//
// 为什么不用真实 Recorder：agentStepTracker 是纯逻辑（切轮次 + 攒字段），
// 断言的是「切了几轮、每轮的 index / 工具字段对不对」，不该把落库通路扯进来。
// 嵌入 observability.Recorder（值为 nil）只为满足接口方法集 ——
// 本文件只会调用 RecordAgentStep 这一个方法。
type stepCaptureRecorder struct {
	observability.Recorder
	mu    sync.Mutex
	steps []*observability.AgentStep
}

func (r *stepCaptureRecorder) RecordAgentStep(s *observability.AgentStep) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, s)
}

func (r *stepCaptureRecorder) got() []*observability.AgentStep {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*observability.AgentStep(nil), r.steps...)
}

// TestAgentStepTrackerSplitsRounds 锁定「事件流被正确切成轮次」。
//
// 回归背景：agent_task_steps 表、AgentStep 结构、Recorder.RecordAgentStep、
// ObservabilityRepo.AppendStep 全都现成，唯独没有生产者 —— 表里长期只有 1 行，
// 而 agent_tasks 有 291 行。这个测试钉住新接上的生产者切出的轮次结构。
func TestAgentStepTrackerSplitsRounds(t *testing.T) {
	cap := &stepCaptureRecorder{}
	tr := newAgentStepTracker(cap, "task-1")

	// 第 0 轮：模型决定调工具 → 工具返回
	tr.beginRound()
	tr.noteThinking("我需要查一下知识库")
	tr.noteToolCall("grep_chunks", `{"q":"重试策略"}`)
	tr.noteToolResult("命中 3 个片段")

	// 第 1 轮：模型直接给答案（beginRound 应先把第 0 轮收尾）
	tr.beginRound()
	tr.noteThinking("答案是重试三次")
	tr.finish()

	steps := cap.got()
	if len(steps) != 2 {
		t.Fatalf("期望 2 轮，实际 %d 轮", len(steps))
	}
	if steps[0].StepIndex != 0 || steps[1].StepIndex != 1 {
		t.Errorf("step_index 不是从 0 起连续：%d, %d", steps[0].StepIndex, steps[1].StepIndex)
	}
	if steps[0].TaskID != "task-1" || steps[1].TaskID != "task-1" {
		t.Errorf("task_id 丢了：%q, %q", steps[0].TaskID, steps[1].TaskID)
	}
	if steps[0].ToolName != "grep_chunks" {
		t.Errorf("第 0 轮工具名 = %q，期望 grep_chunks", steps[0].ToolName)
	}
	if !strings.Contains(steps[0].ToolInputMasked, "重试策略") {
		t.Errorf("第 0 轮工具入参丢了：%q", steps[0].ToolInputMasked)
	}
	if steps[0].ToolStatus != "success" {
		t.Errorf("第 0 轮工具状态 = %q，期望 success", steps[0].ToolStatus)
	}
	if !strings.Contains(steps[0].ThinkingSummary, "知识库") {
		t.Errorf("第 0 轮思考摘要丢了：%q", steps[0].ThinkingSummary)
	}
	// 轮次之间字段不能串：第 1 轮没调工具
	if steps[1].ToolName != "" || steps[1].ToolInputMasked != "" || steps[1].ToolStatus != "" {
		t.Errorf("第 1 轮没有工具，却带上了工具字段：name=%q input=%q status=%q",
			steps[1].ToolName, steps[1].ToolInputMasked, steps[1].ToolStatus)
	}
	if !strings.Contains(steps[1].ThinkingSummary, "重试三次") {
		t.Errorf("第 1 轮思考摘要丢了：%q", steps[1].ThinkingSummary)
	}
	for _, s := range steps {
		if s.StartedAt.IsZero() || s.EndedAt.IsZero() {
			t.Errorf("第 %d 轮缺起止时间", s.StepIndex)
		}
		if s.LatencyMs < 0 {
			t.Errorf("第 %d 轮 latency_ms 为负：%d", s.StepIndex, s.LatencyMs)
		}
		if s.EndedAt.Before(s.StartedAt) {
			t.Errorf("第 %d 轮 ended_at 早于 started_at", s.StepIndex)
		}
	}
}

// TestAgentStepTrackerFinishIsIdempotent 锁定 finish 幂等。
//
// 两条收尾路径会撞在一起：主循环里 beginRound 内部会 finish 上一轮，
// runWithRunner 的 defer 又会对最后一轮 finish 一次。不幂等就会重复写行。
func TestAgentStepTrackerFinishIsIdempotent(t *testing.T) {
	cap := &stepCaptureRecorder{}
	tr := newAgentStepTracker(cap, "task-1")

	tr.beginRound()
	tr.noteThinking("x")
	tr.finish()
	tr.finish()
	tr.finish()

	if n := len(cap.got()); n != 1 {
		t.Errorf("finish 不幂等：期望 1 行，实际 %d 行", n)
	}
}

// TestAgentStepTrackerWithoutOwnerWritesNothing 锁定「没有归属就不写」。
//
// task_id 在表里是 not null：没有归属时硬写只会得到一行无主数据，
// 前端按 task_id 查永远查不到它，等于制造垃圾。
func TestAgentStepTrackerWithoutOwnerWritesNothing(t *testing.T) {
	cap := &stepCaptureRecorder{}
	cases := map[string]*agentStepTracker{
		"空 taskID":     newAgentStepTracker(cap, ""),
		"nil recorder": newAgentStepTracker(nil, "task-1"),
		"nil tracker":  nil,
	}
	for name, tr := range cases {
		tr.beginRound()
		tr.noteThinking("x")
		tr.noteToolCall("t", "a")
		tr.noteToolResult("r")
		tr.noteToolError("e")
		tr.finish()
		if n := len(cap.got()); n != 0 {
			t.Errorf("%s：不该产生任何行，实际 %d 行", name, n)
		}
	}
}

// TestAgentStepTrackerRecordsToolError 锁定工具失败也会落库。
func TestAgentStepTrackerRecordsToolError(t *testing.T) {
	cap := &stepCaptureRecorder{}
	tr := newAgentStepTracker(cap, "task-1")
	tr.beginRound()
	tr.noteToolCall("grep_chunks", `{"q":"x"}`)
	tr.noteToolError("connection refused")
	tr.finish()

	steps := cap.got()
	if len(steps) != 1 {
		t.Fatalf("期望 1 行，实际 %d 行", len(steps))
	}
	if steps[0].ToolStatus != "error" {
		t.Errorf("tool_status = %q，期望 error", steps[0].ToolStatus)
	}
	if steps[0].ToolError != "connection refused" {
		t.Errorf("tool_error = %q", steps[0].ToolError)
	}
}
