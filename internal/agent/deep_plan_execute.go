package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	"github.com/google/uuid"

	"solvify-agent/internal/observability"
	"solvify-agent/pkg/eventch"
)

const peSessionKey = "SolvifyPlanExecuteV1"

type peRuntime struct {
	mu             sync.Mutex
	engine         *Engine
	model          model.ToolCallingChatModel
	request        Request
	knowledgeNames []string
	external       bool
	externalNames  []string
	document       bool
	documentNames  []string
	state          *peState
	answerOutput   chan<- Event
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
		// 恢复时采用可信状态中的工具选择，刷新后的新消息选项不改变原流程。
		// 工具工厂会重新查询当前启用配置及权限。
		if len(r.request.ResumeData) > 0 {
			buckets := r.engine.buildToolsByDomain(ctx, r.engine.sortedInternalTools(), s.Request.UserID, s.Request.KnowledgeBaseIDs, []ToolDomain{DomainKnowledge, DomainDocMgmt}, false)
			if !s.Request.MCPDisabled {
				if r.engine.toolFactory != nil {
					buckets[DomainExternal] = r.engine.toolFactory.CreateAgentTools(ctx, s.Request.UserID, s.Request.UserToolConfigIDs...)
				} else if bundle, ok := prebuiltToolsFromContext(ctx); ok {
					buckets[DomainExternal] = bundle.Buckets[DomainExternal]
				}
			}
			if err := r.describeTools(ctx, buckets); err != nil {
				return nil, err
			}
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
	r := &peRuntime{engine: e, model: chatModel, request: req, external: len(buckets[DomainExternal]) > 0, answerOutput: out}
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
	defer func() {
		phase := "failed"
		if ctx.Err() != nil {
			phase = "cancelled"
		} else if r.state != nil {
			phase = r.state.Plan.Phase
		}
		r.traceContext(ctx, phase, "")
	}()
	store := e.contextStore(req)
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
	// 内部过程通过结构化事件发送；Replanner 单独消费上游流并发送最终回答片段。
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
		iter = runner.Run(ctx, buildInputMessages(req.Query, req.History, req.ContextData...), adk.WithCheckPointID(id), adk.WithSessionValues(map[string]any{peSessionKey: string(data)}))
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
		r.failPlan(ctx, out)
		e.emitPlanError(ctx, out, failure)
		return
	}
	if interrupted {
		return
	}
	if r.state == nil || r.state.Final == "" {
		r.failPlan(ctx, out)
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
	eventch.Send(ctx, out, Event{Type: EventDone, Content: s.Final, Sources: flattenSources(evidence), PlanID: s.Plan.ID, Done: true})
}

// 所有失败出口统一关闭未完成任务，避免终止后的任务卡仍显示运行或等待审批。
func (r *peRuntime) failPlan(ctx context.Context, out chan<- Event) {
	if r.state == nil {
		return
	}
	r.state.Plan.Status, r.state.Plan.Phase = TaskError, "failed"
	for i := range r.state.Plan.Tasks {
		task := &r.state.Plan.Tasks[i]
		if task.Status == TaskPending || task.Status == TaskRunning || task.Status == TaskWaiting {
			task.Status = TaskCanceled
		}
	}
	eventch.Send(ctx, out, r.snapshot(r.state))
}

func (e *Engine) emitPlan(ctx context.Context, out chan<- Event, plan ExecutionPlan) {
	planCopy := plan
	planCopy.Tasks = append([]TaskSpec(nil), plan.Tasks...)
	eventch.Send(ctx, out, Event{Type: EventPlan, PlanID: plan.ID, Status: plan.Status, TaskStatus: plan.Status, TaskTotal: len(plan.Tasks), Detail: plan.Goal, Plan: &planCopy})
	for i, task := range plan.Tasks {
		eventch.Send(ctx, out, Event{Type: EventTaskCreated, PlanID: plan.ID, TaskID: task.ID, AgentName: task.Agent, TaskStatus: task.Status, TaskIndex: i + 1, TaskTotal: len(plan.Tasks), Title: task.Title, Detail: task.Summary, DependsOn: task.DependsOn})
	}
}

func (e *Engine) emitTask(ctx context.Context, out chan<- Event, plan ExecutionPlan, task TaskSpec, status, summary string) {
	eventch.Send(ctx, out, Event{Type: EventTaskUpdated, PlanID: plan.ID, TaskID: task.ID, AgentName: task.Agent, TaskStatus: status, Title: task.Title, Detail: summary, DependsOn: task.DependsOn})
}

func (e *Engine) emitTaskResult(ctx context.Context, out chan<- Event, plan ExecutionPlan, task TaskSpec, result TaskResult) {
	detail := truncateTaskSummary(result.Summary, 500)
	if result.Error != "" {
		detail = result.Error
	}
	eventch.Send(ctx, out, Event{Type: EventTaskResult, PlanID: plan.ID, TaskID: task.ID, AgentName: task.Agent, TaskStatus: result.Status, Title: task.Title, Detail: detail, Error: result.Error, DependsOn: task.DependsOn})
}

func (r *peRuntime) traceContext(ctx context.Context, phase, taskID string) context.Context {
	metadata := observability.ExecutionMetadataFromContext(ctx)
	metadata.Phase, metadata.TaskID = phase, taskID
	r.mu.Lock()
	s, err := r.session(ctx)
	if _, present := adk.GetSessionValue(ctx, peSessionKey); !present {
		// 根请求上下文位于 Runner 会话之外；收尾时使用本次运行的可信状态。
		s, err = r.state, nil
	}
	if err == nil && s != nil {
		metadata.PlanID = s.Plan.ID
		metadata.PlanVersion = s.Plan.Version
		metadata.Round = s.Round
	}
	r.mu.Unlock()
	return observability.WithExecutionMetadata(ctx, metadata)
}

// startPEProfessionalTrace 为直接调用的专业 ChatModelAgent 接入 Eino 原生回调。
// AgentTool 已有独立 Runner；这里保留父会话/中断状态，只增加专业 Agent 的观测边界。
func startPEProfessionalTrace(ctx context.Context, name string, input *adk.AgentInput, resume *adk.ResumeInfo) (context.Context, func(TaskResult, error)) {
	ctx = callbacks.ReuseHandlers(ctx, &callbacks.RunInfo{Name: name + ".professional", Type: "ChatModelAgent", Component: adk.ComponentOfAgent})
	ctx = callbacks.OnStart(ctx, &adk.AgentCallbackInput{Input: input, ResumeInfo: resume})
	return ctx, func(result TaskResult, err error) {
		if err != nil {
			callbacks.OnError(ctx, err)
			return
		}
		// 使用结果值而非共享事件迭代器，多个 callback 可安全读取同一结构化输出。
		callbacks.OnEnd(ctx, result)
	}
}
