package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
	"github.com/cloudwego/eino/components/model"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"solvify-agent/internal/observability"
	"solvify-agent/pkg/eventch"
)

const peSessionKey = "SolvifyPlanExecuteV1"

type peRuntime struct {
	mu       sync.Mutex
	engine   *Engine
	model    model.ToolCallingChatModel
	request  Request
	external bool
	state    *peState
}

func (r *peRuntime) session(ctx context.Context) (*peState, error) {
	v, ok := adk.GetSessionValue(ctx, peSessionKey)
	data, valid := v.(string)
	if !ok || !valid {
		return nil, fmt.Errorf("深度任务会话无效")
	}
	if r.state == nil {
		var s peState
		if err := json.Unmarshal([]byte(data), &s); err != nil {
			return nil, fmt.Errorf("深度任务状态无效: %w", err)
		}
		if s.Request.UserID != r.request.UserID || s.Request.SessionID != r.request.SessionID {
			return nil, fmt.Errorf("深度任务会话无效")
		}
		r.state = &s
	}
	return r.state, nil
}

// 仅在批次开始前和所有子 Runner 结束后发布不可变 JSON。
// Eino 子 checkpoint 会直接序列化父会话 map，批次内禁止修改会话值。
// 调用方必须持有 mu；恢复时只反序列化一次，运行状态由当前 runtime 管理。
func (r *peRuntime) publish(ctx context.Context, s *peState) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("保存深度业务状态失败: %w", err)
	}
	adk.AddSessionValue(ctx, peSessionKey, string(data))
	return nil
}

func peEvent(g *adk.AsyncGenerator[*adk.AgentEvent], ev Event) {
	g.Send(&adk.AgentEvent{Output: &adk.AgentOutput{CustomizedOutput: ev}})
}

func (r *peRuntime) snapshot(s *peState) Event {
	p := clonePEPlan(s.Plan)
	return Event{Type: EventPlan, PlanID: p.ID, PlanVersion: p.Version, Phase: p.Phase, ReplanReason: p.Reason, Round: p.Round, Plan: &p, TaskStatus: p.Status}
}

func (e *Engine) runPlanExecute(ctx context.Context, req Request, chatModel model.ToolCallingChatModel, out chan<- Event) {
	if _, ok := prebuiltToolsFromContext(ctx); !ok {
		sorted := e.sortedInternalTools()
		ctx = withPrebuiltTools(ctx, prebuiltToolsBundle{Sorted: sorted, Buckets: e.buildToolsByDomain(ctx, sorted, req.UserID, req.KnowledgeBaseIDs, AllToolDomains, !req.MCPDisabled, req.UserToolConfigIDs...)})
	}
	buckets, _ := e.bucketsFor(ctx, e.sortedInternalTools(), req)
	r := &peRuntime{engine: e, model: chatModel, request: req, external: len(buckets[DomainExternal]) > 0}
	root, err := r.build(ctx)
	if err != nil {
		e.emitPlanError(ctx, out, err)
		return
	}
	id := req.CheckpointID
	if id == "" {
		id = "deeppe-v1-" + uuid.NewString()
	}
	ctx = observability.WithExecutionMetadata(ctx, observability.ExecutionMetadata{CheckpointID: id, Phase: "planning", Resumed: len(req.ResumeData) > 0})
	defer func() { r.traceContext(ctx, "completed", "") }()
	store := e.buildCheckpointStore(req.SessionID)
	interrupted := false
	defer func() {
		if !interrupted {
			if deleter, ok := store.(adk.CheckPointDeleter); ok {
				cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				_ = deleter.Delete(cleanupCtx, id)
			}
		}
	}()
	// 过程通过结构化事件实时发送，内部正文无须 token 流。
	// Eino v0.9.14 流式 ID 注入会修改与异步回调共享的消息；使用非流式执行避免该竞争。
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: root, EnableStreaming: false, CheckPointStore: store})
	var iter *adk.AsyncIterator[*adk.AgentEvent]
	if req.CheckpointID != "" && len(req.ResumeData) == 0 {
		e.emitPlanError(ctx, out, fmt.Errorf("恢复深度任务缺少审批或澄清回复"))
		return
	}
	if len(req.ResumeData) > 0 {
		iter, err = runner.ResumeWithParams(ctx, id, &adk.ResumeParams{Targets: req.ResumeData})
	} else {
		s := &peState{Request: req, Results: map[string]TaskResult{}, Unsafe: map[string]bool{}, Failures: map[string]string{}}
		data, marshalErr := json.Marshal(s)
		if marshalErr != nil {
			e.emitPlanError(ctx, out, marshalErr)
			return
		}
		iter = runner.Run(ctx, buildInputMessages(req.Query, req.History), adk.WithCheckPointID(id), adk.WithSessionValues(map[string]any{peSessionKey: string(data)}))
	}
	if err != nil {
		e.emitPlanError(ctx, out, fmt.Errorf("恢复深度任务失败: %w", err))
		return
	}
	var failure error
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Output != nil && ev.Output.CustomizedOutput != nil {
			if event, ok := ev.Output.CustomizedOutput.(Event); ok {
				r.mu.Lock()
				if s := r.state; s != nil {
					if event.PlanID == "" {
						event.PlanID, event.PlanVersion = s.Plan.ID, s.Plan.Version
					}
					if event.Phase == "" {
						event.Phase = s.Plan.Phase
					}
					event.ReplanReason, event.Round = s.Plan.Reason, s.Round
				}
				r.mu.Unlock()
				eventch.Send(ctx, out, event)
			}
		}
		if ev.Err != nil {
			failure = ev.Err
			continue
		}
		if ev.Action != nil && ev.Action.Interrupted != nil {
			interrupted = true
			r.mu.Lock()
			s := r.state
			if s != nil {
				s.Plan.Status = TaskWaiting
				s.Plan.Phase = "waiting"
				eventch.Send(ctx, out, r.snapshot(s))
			}
			r.mu.Unlock()
			points := ev.Action.Interrupted.InterruptContexts
			// Eino 的根中断已等待本批所有分支；只暴露真正的审批叶子。
			var chosen *adk.InterruptCtx
			if s != nil {
				for _, task := range s.Plan.Tasks {
					for _, point := range points {
						if !point.IsRootCause {
							continue
						}
						for parent := point; parent != nil; parent = parent.Parent {
							if m, ok := parent.Info.(map[string]any); ok && m["task_id"] == task.ID {
								chosen = point
								break
							}
						}
						if chosen != nil {
							break
						}
					}
					if chosen != nil {
						break
					}
				}
			}
			if chosen == nil {
				for _, point := range points {
					if point.IsRootCause {
						chosen = point
						break
					}
				}
			}
			if chosen == nil {
				failure = fmt.Errorf("中断缺少审批信息")
				continue
			}
			infoText, _ := chosen.Info.(string)
			infoType, data := parseInterruptInfo(infoText)
			msg := getString(data, "message")
			if msg == "" {
				msg = formatInterruptInfo(chosen.Info)
			}
			interrupt := Event{Type: EventInterrupt, Title: "需要人工确认", Detail: msg, Status: "interrupt", CheckpointID: id, InterruptID: chosen.ID, InterruptInfo: data, Done: true}
			if s != nil {
				interrupt.PlanID = s.Plan.ID
				interrupt.PlanVersion = s.Plan.Version
			}
			if infoType == "clarify" {
				interrupt.IsClarify = true
				interrupt.Status = "clarify"
				interrupt.ClarifyQuestion = getString(data, "question")
				interrupt.ClarifyOptions = getStringSlice(data, "options")
				interrupt.Detail = interrupt.ClarifyQuestion
			}
			eventch.Send(ctx, out, interrupt)
		}
		// 标准模型输出属于框架内部上下文；仅业务事件及通过校验的最终回答公开。
		if ev.Output != nil && ev.Output.MessageOutput != nil {
			_, mErr := ev.Output.MessageOutput.GetMessage()
			if mErr != nil {
				failure = mErr
			}
		}
	}
	if ctx.Err() != nil {
		return
	}
	if failure != nil {
		interrupted = false
		if r.state != nil {
			r.state.Plan.Status = TaskError
			r.state.Plan.Phase = "failed"
			for i := range r.state.Plan.Tasks {
				if r.state.Plan.Tasks[i].Status == TaskPending {
					r.state.Plan.Tasks[i].Status = TaskCanceled
				}
			}
			for i := range r.state.Plan.Tasks {
				if r.state.Plan.Tasks[i].Status == TaskPending || r.state.Plan.Tasks[i].Status == TaskRunning {
					r.state.Plan.Tasks[i].Status = TaskCanceled
				}
			}
			eventch.Send(ctx, out, r.snapshot(r.state))
		}
		e.emitPlanError(ctx, out, failure)
		return
	}
	if interrupted {
		return
	}
	if r.state == nil || r.state.Final == "" {
		if r.state != nil {
			r.state.Plan.Status = TaskError
			r.state.Plan.Phase = "failed"
			eventch.Send(ctx, out, r.snapshot(r.state))
		}
		e.emitPlanError(ctx, out, fmt.Errorf("深度流程达到执行上限或未返回有效最终答复"))
		return
	}
	s := r.state
	s.Plan.Status = TaskSuccess
	s.Plan.Phase = "completed"
	eventch.Send(ctx, out, r.snapshot(s))
	var evidence []AgentEvidence
	for _, task := range s.Plan.Tasks {
		evidence = append(evidence, s.Results[task.ID].Evidence...)
	}
	eventch.Send(ctx, out, Event{Type: EventAnswer, Content: s.Final, PlanID: s.Plan.ID, PlanVersion: s.Plan.Version})
	eventch.Send(ctx, out, Event{Type: EventDone, Content: s.Final, Sources: flattenSources(evidence), PlanID: s.Plan.ID, Done: true})
}

func (r *peRuntime) build(ctx context.Context) (adk.ResumableAgent, error) {
	planSchema := schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"strategy": {Type: schema.String, Required: true}, "replan_reason": {Type: schema.String},
		"tasks": {Type: schema.Array, Required: true, ElemInfo: &schema.ParameterInfo{Type: schema.Object, SubParams: map[string]*schema.ParameterInfo{
			"id": {Type: schema.String, Required: true}, "agent": {Type: schema.String, Required: true}, "title": {Type: schema.String, Required: true}, "description": {Type: schema.String}, "depends_on": {Type: schema.Array, ElemInfo: &schema.ParameterInfo{Type: schema.String}},
		}}},
	})
	planTool := &schema.ToolInfo{Name: "plan", Desc: "生成或更新结构化任务计划", ParamsOneOf: planSchema}
	newPlan := func(context.Context) planexecute.Plan { return &pePlan{} }
	instruction := fmt.Sprintf(`你是 Solvify Planner，只规划，不执行研究。可用角色 knowledge_research、synthesis_review；web_research 可用=%t。
每版最多 %d 个待执行任务，并行允许=%t。strategy 为 direct/sequential/parallel。
独立研究可并行；depends_on 必须存在且无环；最后一个汇总必须覆盖全部有效研究。
简单问题只有 synthesis_review。任务 ID 唯一且稳定；历史终态任务不可改写，重试或新汇总用新 ID。
重规划必须填写 replan_reason，保留已成功任务的原描述和依赖，移除不再需要的待执行任务。
不得规划文档、视频或 PPT 等未实现功能。`, r.external, deepMaxPlanTasks(r.engine.cfg), r.engine.cfg.DeepParallelEnabled)
	planner, err := planexecute.NewPlanner(ctx, &planexecute.PlannerConfig{ToolCallingChatModel: r.model, ToolInfo: planTool, NewPlan: newPlan, GenInputFn: func(_ context.Context, input []adk.Message) ([]adk.Message, error) {
		return append([]adk.Message{schema.SystemMessage(composeSystemPrompt(instruction, r.request.SystemPrompt))}, input...), nil
	}})
	if err != nil {
		return nil, err
	}
	var tools []einoTool.BaseTool
	for _, role := range []string{"knowledge_research", "web_research", "synthesis_review"} {
		if role == "web_research" && !r.external {
			continue
		}
		tools = append(tools, adk.NewAgentTool(ctx, &peTaskAgent{runtime: r, role: role}, adk.WithAgentInputSchema(schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"task_id": {Type: schema.String, Required: true}}))))
	}
	iterations := r.engine.cfg.DeepMaxExecutorIterations
	if iterations <= 0 {
		iterations = 6
	}
	executor, err := planexecute.NewExecutor(ctx, &planexecute.ExecutorConfig{Model: &peExecutorModel{inner: r.model, runtime: r}, MaxIterations: iterations, ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: tools}, EmitInternalEvents: true}, GenInputFn: func(ctx context.Context, in *planexecute.ExecutionContext) ([]adk.Message, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		s, err := r.session(ctx)
		if err != nil {
			return nil, err
		}
		data, _ := json.Marshal(s.Batch)
		return []*schema.Message{schema.SystemMessage("你是 Solvify Executor。仅执行当前批次，通过角色同名工具委派任务，参数只能包含 task_id。并行批次必须在同一条消息中发出全部独立 tool_calls；不得调用未计划任务或直接研究。已有成功结果不可再次调用。完成全部任务后返回简短执行摘要，失败如实报告。"), schema.UserMessage("当前批次：" + string(data))}, nil
	}})
	if err != nil {
		return nil, err
	}
	replanner, err := planexecute.NewReplanner(ctx, &planexecute.ReplannerConfig{ChatModel: r.model, PlanTool: planTool, NewPlan: newPlan, GenInputFn: func(ctx context.Context, in *planexecute.ExecutionContext) ([]adk.Message, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		s, err := r.session(ctx)
		if err != nil {
			return nil, err
		}
		data, _ := json.Marshal(struct {
			Plan          ExecutionPlan
			Results       map[string]TaskResult
			RetryUsed     bool
			Denied        bool
			Blocked       bool
			ExecutedSteps []planexecute.ExecutedStep
		}{s.Plan, s.Results, s.RetryUsed, s.Denied, s.Blocked, in.ExecutedSteps})
		return []*schema.Message{schema.SystemMessage(instruction + "\n你是 Replanner：评估完成情况，调用 plan 调整或保留剩余任务；仅全部必要任务完成且最新汇总覆盖全部成功研究时调用 respond。失败只允许一次只读重试或替代研究，副作用失败与审批拒绝不得绕过。respond.response 必须基于汇总，保留引用，不输出计划 JSON。"), schema.UserMessage(s.Request.Query + "\n执行状态：" + string(data))}, nil
	}})
	if err != nil {
		return nil, err
	}
	rounds := r.engine.cfg.DeepMaxPlanExecuteRounds
	if rounds <= 0 {
		rounds = 8
	}
	return planexecute.New(ctx, &planexecute.Config{Planner: &peStage{runtime: r, inner: planner, phase: "planning"}, Executor: &peStage{runtime: r, inner: executor, phase: "executing"}, Replanner: &peStage{runtime: r, inner: replanner, phase: "replanning"}, MaxIterations: rounds})
}

type peStage struct {
	runtime *peRuntime
	inner   adk.Agent
	phase   string
}

func (a *peStage) Name(ctx context.Context) string        { return a.inner.Name(ctx) }
func (a *peStage) Description(ctx context.Context) string { return a.inner.Description(ctx) }
func (a *peStage) GetType() string                        { return "PlanExecuteStage" }
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
