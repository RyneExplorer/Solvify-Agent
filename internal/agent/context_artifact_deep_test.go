package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/config"
)

func TestPlanExecuteOversizedArtifactReadCompletesDependencies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	e := deepTestEngine()
	e.cfg.DeepMaxSubagentIterations = 10
	e.cfg.RuntimeContext = config.DefaultRuntimeContextPolicy()
	e.WithArtifactRepo(&memoryArtifactRepo{})
	probe := guardProbeTool{calls: new(atomic.Int32)}
	e.RegisterInternal("guard_probe", 1, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return probe })
	ctx = withPrebuiltTools(ctx, prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainKnowledge: {probe}}})
	plan := `{"strategy":"sequential","tasks":[{"id":"t1","agent":"knowledge_research","title":"清单","description":"读取完整清单"},{"id":"t2","agent":"knowledge_research","title":"比对","description":"基于清单比对","depends_on":["t1"]},{"id":"t3","agent":"synthesis_review","title":"汇总","depends_on":["t1","t2"]}]}`
	m := peModel(plan, new(atomic.Int32))
	base := m.respond
	var readCalls, firstTaskCalls int
	var rebuilt strings.Builder
	var ref artifactReference
	const researchSummary = "已读取清单原文并完成核验"
	m.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		if !strings.Contains(messages[0].Content, "研究知识库") {
			return base(ctx, messages)
		}
		if strings.Contains(messages[1].Content, "基于清单比对") {
			if !strings.Contains(messages[1].Content, researchSummary) {
				return nil, fmt.Errorf("第二步没有收到第一步结果")
			}
			return schema.AssistantMessage("完成比对", nil), nil
		}
		firstTaskCalls++
		last := messages[len(messages)-1]
		if last.Role != schema.Tool {
			return peCall("guard_probe", map[string]any{}), nil
		}
		if last.ToolName == "guard_probe" || last.ToolCallID == "call-guard_probe" {
			if err := json.Unmarshal([]byte(last.Content), &ref); err != nil || ref.ArtifactID == "" {
				return nil, fmt.Errorf("缺少产物引用: %s", last.Content)
			}
			readCalls++
			return peCall(artifactReadToolName, map[string]any{"artifact_id": ref.ArtifactID, "offset": 1000, "limit": 6000}), nil
		}
		var page artifactReadPage
		if err := json.Unmarshal([]byte(last.Content), &page); err != nil || page.NextOffset <= page.Offset {
			return nil, fmt.Errorf("无有效产物分页: %s", last.Content)
		}
		rebuilt.WriteString(page.Content)
		if !page.Complete {
			readCalls++
			return peCall(artifactReadToolName, map[string]any{"artifact_id": ref.ArtifactID, "offset": page.NextOffset, "limit": 6000}), nil
		}
		return schema.AssistantMessage(researchSummary, nil), nil
	}
	events := collectPEEvents(t, ctx, e, Request{Query: "检索并比对文档", UserID: "u", SessionID: "artifact-read"}, m)
	completed := make(map[string]bool)
	var answers, done int
	for _, ev := range events {
		if ev.Type == EventError || ev.TaskStatus == TaskError {
			t.Fatalf("超限读取中断了深度流程: %+v", ev)
		}
		if ev.Type == EventTaskResult && ev.TaskStatus == TaskSuccess {
			completed[ev.TaskID] = true
		}
		if ev.Type == EventAnswer {
			answers++
		}
		if ev.Type == EventDone {
			done++
		}
	}
	want := []rune(strings.Repeat(guardProbeRaw, 800) + guardProbeTail)[1000:]
	if rebuilt.String() != string(want) || readCalls < 2 || firstTaskCalls != readCalls+2 || probe.calls.Load() != 1 {
		t.Fatalf("读取缺失、重试或重新执行原工具: read=%d model=%d source=%d", readCalls, firstTaskCalls, probe.calls.Load())
	}
	if !completed["t1"] || !completed["t2"] || !completed["t3"] || answers != 1 || done != 1 {
		t.Fatalf("依赖任务或最终响应未完成: completed=%v answers=%d done=%d", completed, answers, done)
	}
}

func TestPlanExecuteArtifactArgumentCorrection(t *testing.T) {
	for _, scenario := range []string{"type", "missing", "negative", "offset", "json", "ignored", "budget"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			e := deepTestEngine()
			e.cfg.DeepMaxSubagentIterations = 5
			e.WithArtifactRepo(&memoryArtifactRepo{})
			probe := guardProbeTool{calls: new(atomic.Int32)}
			e.RegisterInternal("guard_probe", 1, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return probe })
			ctx = withPrebuiltTools(ctx, prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainKnowledge: {probe}}})
			plan := `{"strategy":"sequential","tasks":[{"id":"t1","agent":"knowledge_research","title":"抽样核验清单"},{"id":"t2","agent":"synthesis_review","title":"汇总","depends_on":["t1"]}]}`
			m := peModel(plan, new(atomic.Int32))
			base := m.respond
			var ref artifactReference
			var researchCalls, feedbackSeen int
			badCall := func() *schema.Message {
				args := map[string]any{"artifact_id": ref.ArtifactID, "offset": "wrong"}
				switch scenario {
				case "missing":
					delete(args, "artifact_id")
				case "negative":
					args["offset"] = -1
				case "offset":
					args["offset"] = 999999
				}
				msg := peCall(artifactReadToolName, args)
				if scenario == "json" {
					msg.ToolCalls[0].Function.Arguments = "{"
				}
				return msg
			}
			m.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
				if !strings.Contains(messages[0].Content, "研究知识库") {
					return base(ctx, messages)
				}
				researchCalls++
				last := messages[len(messages)-1]
				if last.Role != schema.Tool {
					return peCall("guard_probe", map[string]any{}), nil
				}
				if last.ToolCallID == "call-guard_probe" {
					if err := json.Unmarshal([]byte(last.Content), &ref); err != nil || ref.ArtifactID == "" {
						return nil, fmt.Errorf("缺少产物引用")
					}
					return badCall(), nil
				}
				if _, recoverable := artifactArgumentFailure(artifactReadToolName, last.Content); recoverable {
					feedbackSeen++
					var feedback artifactArgumentFeedback
					_ = json.Unmarshal([]byte(last.Content), &feedback)
					if scenario == "json" && feedback.Field != "arguments" {
						return nil, fmt.Errorf("非法JSON被替换，模型没有收到原参数格式错误")
					}
					if last.ToolCallID != "call-read_context_artifact" {
						return nil, fmt.Errorf("工具调用与错误反馈未配对")
					}
					if scenario == "ignored" {
						return schema.AssistantMessage("伪称核验完成", nil), nil
					}
					if scenario == "budget" {
						return badCall(), nil
					}
					return peCall(artifactReadToolName, map[string]any{"artifact_id": ref.ArtifactID, "offset": 0, "limit": 100}), nil
				}
				var page artifactReadPage
				if json.Unmarshal([]byte(last.Content), &page) != nil || page.NextOffset != 100 || page.Content != string([]rune(strings.Repeat(guardProbeRaw, 800))[:100]) {
					return nil, fmt.Errorf("纠正后没有读取真实原文")
				}
				return schema.AssistantMessage("已完成原文抽样核验", nil), nil
			}
			events := collectPEEvents(t, ctx, e, Request{Query: "读取清单证据", UserID: "u", SessionID: "argument-" + scenario}, m)
			var succeeded, failed, done, recoverableEvents bool
			for _, ev := range events {
				if ev.Type == EventTaskResult && ev.TaskID == "t1" {
					succeeded = succeeded || ev.TaskStatus == TaskSuccess
					failed = failed || ev.TaskStatus == TaskError
				}
				done = done || ev.Type == EventDone
				recoverableEvents = recoverableEvents || ev.Type == EventToolResult && ev.Status == TaskError && ev.Retryable
				if scenario != "budget" && scenario != "ignored" && ev.Type == EventError {
					t.Fatalf("纠错过程终止: %+v", ev)
				}
			}
			if probe.calls.Load() != 1 || feedbackSeen == 0 || !recoverableEvents || researchCalls > e.cfg.DeepMaxSubagentIterations {
				t.Fatalf("反馈、事件或预算无效: source=%d feedback=%d events=%t calls=%d", probe.calls.Load(), feedbackSeen, recoverableEvents, researchCalls)
			}
			if scenario == "ignored" || scenario == "budget" {
				if succeeded || !failed {
					t.Fatalf("未纠正参数却标记成功: success=%t failed=%t", succeeded, failed)
				}
			} else if !succeeded || failed || !done || researchCalls != 4 || feedbackSeen != 1 {
				t.Fatalf("纠正后未完成或仍残留失败: success=%t failed=%t done=%t calls=%d feedback=%d", succeeded, failed, done, researchCalls, feedbackSeen)
			}
		})
	}
}
