package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	apperrors "solvify-agent/pkg/errors"
)

func TestModelCompletionReasonAndShape(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		tool         bool
		want         error
	}{
		{"文本完成", "stop", false, nil},
		{"工具请求", "tool_calls", true, nil},
		{"正文截断", "length", false, errModelOutputLimit},
		{"工具截断", "length", true, errModelOutputLimit},
		{"内容过滤", "content_filter", false, errModelOutputFiltered},
		{"缺失终态", "", false, errModelOutputInvalid},
		{"未知终态", "other", false, errModelOutputInvalid},
		{"工具结束却无工具", "tool_calls", false, errModelOutputInvalid},
		{"文本结束却有工具", "stop", true, errModelOutputInvalid},
		{"旧函数类型不支持", "function_call", true, errModelOutputInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := schema.AssistantMessage("部分正文", nil)
			msg.ResponseMeta = &schema.ResponseMeta{FinishReason: tc.reason}
			if tc.tool {
				msg.ToolCalls = peCall("lookup", map[string]string{}).ToolCalls
			}
			if err := validateModelCompletion(msg); !errors.Is(err, tc.want) {
				t.Fatalf("结束判定错误: %v", err)
			}
		})
	}
	for _, msg := range []*schema.Message{nil, schema.AssistantMessage("有正文但缺元数据", nil)} {
		if !errors.Is(validateModelCompletion(msg), errModelOutputInvalid) {
			t.Fatal("缺失结束状态不能成功")
		}
	}
}

func TestPlanExecuteInvalidTaskToolCompletionDoesNotExecute(t *testing.T) {
	for _, reason := range []string{"length", "content_filter", "", "unknown", "stop"} {
		t.Run(reason, func(t *testing.T) {
			var tools, research, attempts atomic.Int32
			e := deepTestEngine()
			e.cfg.ModelMaxRetries = 3
			e.RegisterInternal("lookup_record", 1, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool {
				return taskBudgetTool{calls: &tools}
			})
			m := peModel(`{"strategy":"sequential","tasks":[{"id":"a","agent":"knowledge_research","title":"研究"},{"id":"s","agent":"synthesis_review","title":"审查","depends_on":["a"]}]}`, &research)
			base := m.respond
			m.respond = func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
				if strings.Contains(msgs[0].Content, "研究知识库") {
					attempts.Add(1)
					msg := peCall("lookup_record", map[string]any{})
					msg.ResponseMeta = &schema.ResponseMeta{FinishReason: reason}
					return msg, nil
				}
				return base(ctx, msgs)
			}
			events := collectPEEvents(t, deepTestContext(t), e, Request{Query: "核对资料", UserID: "u", SessionID: "tool-completion"}, m)
			requirePEError(t, events)
			if tools.Load() != 0 || attempts.Load() != 1 {
				t.Fatalf("异常结束的工具请求被执行或重试: tools=%d attempts=%d", tools.Load(), attempts.Load())
			}
		})
	}
}

func TestPlanExecuteRetainsTruncatedTaskMaterial(t *testing.T) {
	for _, role := range []string{"研究知识库", "仅使用结构化研究结果"} {
		t.Run(role, func(t *testing.T) {
			ctx := deepTestContext(t)
			m := peModel(`{"strategy":"sequential","tasks":[{"id":"a","agent":"knowledge_research","title":"研究"},{"id":"s","agent":"synthesis_review","title":"审查","depends_on":["a"]}]}`, new(atomic.Int32))
			base := m.respond
			m.respond = func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
				if strings.Contains(msgs[0].Content, role) {
					msg := schema.AssistantMessage("已经生成的证据与要点，尚未完成", nil)
					msg.ResponseMeta = &schema.ResponseMeta{FinishReason: "length"}
					return msg, nil
				}
				return base(ctx, msgs)
			}
			r := &peRuntime{engine: deepTestEngine(), model: m, request: Request{Query: "核对", UserID: "u", SessionID: "partial-material", MaxContextLength: 64000, OutputLimit: 4096, ModelName: "gpt-4o"}}
			root, err := r.build(ctx)
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(peState{Request: r.request, Results: map[string]TaskResult{}, Unsafe: map[string]bool{}, Failures: map[string]string{}})
			it := adk.NewRunner(ctx, adk.RunnerConfig{Agent: root}).Run(ctx, []*schema.Message{schema.UserMessage(r.request.Query)}, adk.WithSessionValues(map[string]any{peSessionKey: string(data)}))
			var failed bool
			for {
				ev, ok := it.Next()
				if !ok {
					break
				}
				failed = failed || errors.Is(ev.Err, errModelOutputLimit)
			}
			id := "a"
			if role == "仅使用结构化研究结果" {
				id = "s"
			}
			result := r.state.Results[id]
			if !failed || result.Status != TaskError || result.Summary != "已经生成的证据与要点，尚未完成" || result.Error == "" {
				t.Fatalf("部分材料未保留为失败结果: failed=%t result=%+v", failed, result)
			}
		})
	}
}

// 经过真实 Engine/Eino，覆盖每个生产模型边界，不以校验函数单测替代执行行为。
func TestPlanExecuteRejectsAbnormalModelCompletion(t *testing.T) {
	for _, phase := range []string{"planner", "executor", "research", "review", "respond"} {
		for _, reason := range []string{"length", "content_filter", "", "unknown"} {
			t.Run(phase+"/"+reason, func(t *testing.T) {
				var research, badCalls atomic.Int32
				m := peModel(`{"strategy":"sequential","tasks":[{"id":"a","agent":"knowledge_research","title":"研究"},{"id":"s","agent":"synthesis_review","title":"审查","depends_on":["a"]}]}`, &research)
				m.rawCompletion = true
				base := m.respond
				m.respond = func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
					msg, err := base(ctx, msgs)
					if err != nil {
						return msg, err
					}
					system := msgs[0].Content
					bad := phase == "planner" && strings.Contains(system, "Solvify Planner") ||
						phase == "executor" && strings.Contains(system, "Solvify Executor") ||
						phase == "research" && strings.Contains(system, "研究知识库") ||
						phase == "review" && strings.Contains(system, "仅使用结构化研究结果") ||
						phase == "respond" && len(msg.ToolCalls) == 1 && msg.ToolCalls[0].Function.Name == "respond"
					finish := "stop"
					if len(msg.ToolCalls) > 0 {
						finish = "tool_calls"
					}
					if bad {
						finish = reason
						badCalls.Add(1)
					}
					msg.ResponseMeta = &schema.ResponseMeta{FinishReason: finish}
					return msg, nil
				}
				events := collectPEEvents(t, deepTestContext(t), deepTestEngine(), Request{Query: "核对", UserID: "u", SessionID: "completion"}, m)
				want := apperrors.KindModelOutputInvalid
				if reason == "length" {
					want = apperrors.KindOutputLimit
				}
				if reason == "content_filter" {
					want = apperrors.KindOutputFiltered
				}
				failed, partial := false, false
				for _, ev := range events {
					if ev.Type == EventDone {
						t.Fatal("异常结束错误地发送成功终态")
					}
					if ev.Type == EventError {
						failed = true
						if ev.Kind != want {
							t.Fatalf("错误类型错误: %+v", ev)
						}
					}
					if ev.Type == EventTaskResult && ((phase == "research" && ev.TaskID == "a") || (phase == "review" && ev.TaskID == "s")) {
						if ev.TaskStatus != TaskError {
							t.Fatal("截断任务被标成功")
						}
					}
					if ev.Plan != nil {
						for _, task := range ev.Plan.Tasks {
							if (phase == "research" && task.ID == "a" || phase == "review" && task.ID == "s") && task.Status == TaskError {
								partial = task.ResultRef != ""
							}
						}
					}
				}
				if !failed || badCalls.Load() != 1 {
					t.Fatalf("异常结束不得纠错重试: failed=%t calls=%d", failed, badCalls.Load())
				}
				if (phase == "research" || phase == "review") && !partial {
					t.Fatal("未保留失败任务结果引用")
				}
				if (phase == "planner" || phase == "executor") && research.Load() != 0 {
					t.Fatal("异常委派启动了研究")
				}
			})
		}
	}
}

// 固定离线样本验证真实装配的角色/交付边界，不把模拟输出当作真实模型质量评估。
func TestPlanExecuteReviewThenSingleFinalAnswer(t *testing.T) {
	for _, query := range []string{"Go channel是什么", "最详细的Go channel教程", "资料不足时说明缺口"} {
		t.Run(query, func(t *testing.T) {
			var research atomic.Int32
			m := peModel(parallelTestPlan, &research)
			base := m.respond
			var mu sync.Mutex
			var reviews, finals int
			m.respond = func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
				system := msgs[0].Content
				if strings.Contains(system, "仅使用结构化研究结果") {
					mu.Lock()
					reviews++
					mu.Unlock()
					if !strings.Contains(system, "不生成完整文章") || !strings.Contains(system, "冲突/缺口") || !strings.Contains(system, "600中文字以内") || strings.Contains(system, "面向用户的普通回答") {
						t.Error("审查角色仍要求全文")
					}
					return schema.AssistantMessage("回答要点：channel传递值；来源见研究。大纲：语义、示例；缺口：未核验版本实现。", nil), nil
				}
				msg, err := base(ctx, msgs)
				if strings.Contains(system, "你是 Replanner") {
					if !strings.Contains(system, "先给直接结论") || !strings.Contains(system, "用户明确要求最详细") || !strings.Contains(system, "输出上限为4096 tokens") || !strings.Contains(system, "300—600中文字以内") || !strings.Contains(system, "不是对正文硬截断") {
						t.Error("最终回答未传递篇幅策略或实际输出预算")
					}
					if strings.Contains(system, "必须调用 plan 工具提交结构化计划，不得用普通文本回答") {
						t.Error("Planner指令污染Replanner")
					}
					if msg != nil && len(msg.ToolCalls) == 1 && msg.ToolCalls[0].Function.Name == "respond" {
						finals++
					}
				}
				return msg, err
			}
			events := collectPEEvents(t, deepTestContext(t), deepTestEngine(), Request{Query: query, UserID: "u", SessionID: "answer-policy"}, m)
			done := 0
			for _, ev := range events {
				if ev.Type == EventError {
					t.Fatal(ev.Error)
				}
				if ev.Type == EventDone {
					done++
				}
			}
			if reviews != 1 || finals != 1 || done != 1 {
				t.Fatalf("重复生成/交付: review=%d final=%d done=%d", reviews, finals, done)
			}
		})
	}
}
