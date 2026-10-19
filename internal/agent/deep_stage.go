package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
)

type peStage struct {
	runtime *peRuntime
	inner   adk.Agent
	phase   string
}

func (a *peStage) Name(ctx context.Context) string { return a.inner.Name(ctx) }

func (a *peStage) Description(ctx context.Context) string { return a.inner.Description(ctx) }

func (a *peStage) GetType() string { return "PlanExecuteStage" }

func (a *peStage) Run(ctx context.Context, in *adk.AgentInput, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	return a.execute(ctx, in, nil, opts...)
}

func (a *peStage) Resume(ctx context.Context, info *adk.ResumeInfo, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	return a.execute(ctx, nil, info, opts...)
}

func (a *peStage) execute(ctx context.Context, in *adk.AgentInput, resume *adk.ResumeInfo, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	iter, g := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		defer g.Close()
		r := a.runtime
		r.mu.Lock()
		s, err := r.session(ctx)
		if err == nil {
			s.Plan.Phase = a.phase
			s.Plan.Status = TaskRunning
			if a.phase == "executing" && resume == nil {
				s.Round++
				s.Plan.Round = s.Round
				s.Batch = s.Plan.ReadyTasks()
				if s.Plan.Strategy != "parallel" && len(s.Batch) > 1 {
					s.Batch = s.Batch[:1]
				}
				if len(s.Batch) == 0 {
					err = fmt.Errorf("计划没有可执行任务")
				} else {
					adk.AddSessionValue(ctx, planexecute.PlanSessionKey, &pePlan{ExecutionPlan: clonePEPlan(s.Plan), Batch: append([]TaskSpec(nil), s.Batch...)})
				}
			}
			if s.Plan.ID != "" {
				peEvent(g, r.snapshot(s))
			}
			if err == nil {
				err = r.publish(ctx, s)
			}
		}
		r.mu.Unlock()
		if err != nil {
			g.Send(&adk.AgentEvent{Err: err})
			return
		}
		ctx = r.traceContext(ctx, a.phase, "")
		var child *adk.AsyncIterator[*adk.AgentEvent]
		if resume != nil {
			ra, ok := a.inner.(adk.ResumableAgent)
			if !ok {
				g.Send(&adk.AgentEvent{Err: fmt.Errorf("阶段不支持恢复")})
				return
			}
			child = ra.Resume(ctx, resume, opts...)
		} else {
			child = a.inner.Run(ctx, in, opts...)
		}
		var action *adk.AgentEvent
		var response string
		for {
			ev, ok := child.Next()
			if !ok {
				break
			}
			if ev.Err != nil {
				err = ev.Err
			}
			if ev.Output != nil && ev.Output.MessageOutput != nil {
				mv := ev.Output.MessageOutput
				msg, mErr := mv.GetMessage()
				if mErr != nil {
					err = mErr
				}
				if msg != nil {
					copied, output := *ev, *ev.Output
					output.MessageOutput = &adk.MessageVariant{Message: msg, Role: mv.Role, ToolName: mv.ToolName}
					copied.Output = &output
					ev = &copied
					if a.phase == "replanning" {
						var res planexecute.Response
						if json.Unmarshal([]byte(msg.Content), &res) == nil && res.Response != "" {
							response = res.Response
						}
					}
					if ev.AgentName == "" || ev.AgentName == a.inner.Name(ctx) {
						// 专业 Agent 的正文只用于 TaskResult；外层阶段输出作为过程信息。
						peEvent(g, Event{Type: EventThinking, AgentName: a.inner.Name(ctx), Phase: a.phase, Title: pePhaseTitle(a.phase), Detail: truncateTaskSummary(msg.Content, 1200), Status: TaskRunning})
					}
				}
			}
			if ev.Action != nil && (ev.Action.Interrupted != nil || ev.Action.BreakLoop != nil) {
				action = ev
				continue
			}
			g.Send(ev)
		}
		if err != nil {
			g.Send(&adk.AgentEvent{Err: err})
			return
		}
		if action != nil && action.Action.Interrupted != nil {
			r.mu.Lock()
			err = r.publish(ctx, r.state)
			r.mu.Unlock()
			if err != nil {
				g.Send(&adk.AgentEvent{Err: err})
				return
			}
			g.Send(action)
			return
		}
		r.mu.Lock()
		s, err = r.session(ctx)
		if err == nil {
			switch a.phase {
			case "planning":
				v, ok := adk.GetSessionValue(ctx, planexecute.PlanSessionKey)
				p, valid := v.(*pePlan)
				if !ok || !valid {
					err = fmt.Errorf("Planner 未返回结构化计划")
				} else {
					err = r.installPlan(s, p)
				}
			case "executing":
				for _, t := range s.Batch {
					result, ok := s.Results[t.ID]
					if !ok || (result.Status != TaskSuccess && result.Status != TaskError) {
						err = fmt.Errorf("Executor 未完成任务 %s", t.ID)
						break
					}
				}
			case "replanning":
				if action != nil && action.Action.BreakLoop != nil {
					err = validatePECompletion(s, response)
					if err == nil {
						s.Final = strings.TrimSpace(response)
					}
				} else {
					v, ok := adk.GetSessionValue(ctx, planexecute.PlanSessionKey)
					p, valid := v.(*pePlan)
					if !ok || !valid {
						err = fmt.Errorf("Replanner 未返回有效计划")
					} else {
						err = r.installPlan(s, p)
					}
				}
			}
			if err == nil {
				if a.phase == "planning" || (a.phase == "replanning" && (action == nil || action.Action.BreakLoop == nil)) {
					adk.AddSessionValue(ctx, planexecute.PlanSessionKey, &pePlan{ExecutionPlan: clonePEPlan(s.Plan)})
				}
				err = r.publish(ctx, s)
				peEvent(g, r.snapshot(s))
			}
		}
		r.mu.Unlock()
		if err != nil {
			g.Send(&adk.AgentEvent{Err: err})
			return
		}
		ctx = r.traceContext(ctx, a.phase, "")
		if action != nil {
			g.Send(action)
		}
	}()
	return iter
}

func pePhaseTitle(phase string) string {
	switch phase {
	case "planning":
		return "生成任务计划"
	case "executing":
		return "执行当前任务批次"
	default:
		return "评估结果与调整计划"
	}
}
