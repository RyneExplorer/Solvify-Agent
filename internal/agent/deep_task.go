package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/tool"
	bizerrors "solvify-agent/pkg/errors"
)

// peTaskAgent 是 AgentTool 的可恢复边界，只信任服务器保存的任务和前置结果。
type peTaskAgent struct {
	runtime *peRuntime
	role    string
}

func (a *peTaskAgent) Name(context.Context) string { return a.role }

func (a *peTaskAgent) Description(context.Context) string {
	return "执行已计划的" + a.role + "任务，必须提供 task_id"
}

func (a *peTaskAgent) GetType() string { return "ResearchTask" }

func (a *peTaskAgent) Run(ctx context.Context, in *adk.AgentInput, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	return a.execute(ctx, in, nil, opts...)
}

func (a *peTaskAgent) Resume(ctx context.Context, info *adk.ResumeInfo, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	return a.execute(ctx, nil, info, opts...)
}

func (a *peTaskAgent) execute(ctx context.Context, in *adk.AgentInput, resume *adk.ResumeInfo, opts ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	iter, g := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		defer g.Close()
		r := a.runtime
		var taskID string
		var saved *peChildState
		if resume != nil {
			var ok bool
			saved, ok = resume.InterruptState.(*peChildState)
			if !ok {
				g.Send(&adk.AgentEvent{Err: fmt.Errorf("子任务中断状态无效")})
				return
			}
			taskID = saved.TaskID
		} else {
			var args struct {
				TaskID string `json:"task_id"`
			}
			if in == nil || len(in.Messages) == 0 || json.Unmarshal([]byte(in.Messages[len(in.Messages)-1].Content), &args) != nil {
				g.Send(&adk.AgentEvent{Err: fmt.Errorf("子任务参数无效")})
				return
			}
			taskID = args.TaskID
		}
		r.mu.Lock()
		s, err := r.session(ctx)
		var task TaskSpec
		var req Request
		var artifactInputs map[string]TaskResult
		var inputs TaskSpec
		if err == nil {
			for _, candidate := range s.Batch {
				if candidate.ID == taskID {
					task = candidate
					break
				}
			}
			if task.ID == "" || task.Agent != a.role {
				err = fmt.Errorf("任务不属于当前批次或角色不匹配: %s", taskID)
			} else if previous, ok := s.Results[taskID]; ok && previous.Status != TaskWaiting {
				err = fmt.Errorf("任务已结束，不允许重复委派: %s", taskID)
			} else if taskByID(s.Plan, taskID).Status == TaskRunning && resume == nil {
				err = fmt.Errorf("任务正在执行，不允许重复委派: %s", taskID)
			}
			if err == nil {
				for _, dep := range task.DependsOn {
					if s.Results[dep].Status != TaskSuccess {
						err = fmt.Errorf("任务前置结果未成功: %s", dep)
						break
					}
				}
			}
			if err == nil {
				req = s.Request
				req.TaskID = task.ID
				req.AgentName = task.Agent
				req.ToolDomains = taskToolDomains(task.Agent)
				req.CheckpointID = ""
				req.ResumeData = nil
				req.Query = "原问题（仅作背景）：" + s.Request.Query + "\n只执行当前任务：" + task.Title + "\n任务说明：" + task.Description
				req.History = append(req.History[:0:0], s.Request.History...)
				inputs = task
				if task.Agent == "synthesis_review" {
					inputs.DependsOn = peResearchAncestors(s.Plan, task.ID)
				}
				req.Query = buildTaskQuery(req.Query, inputs, s.Results)
				artifactInputs = make(map[string]TaskResult, len(inputs.DependsOn))
				for _, dep := range inputs.DependsOn {
					artifactInputs[dep] = s.Results[dep]
				}
				for i := range s.Plan.Tasks {
					if s.Plan.Tasks[i].ID == task.ID {
						s.Plan.Tasks[i].Status = TaskRunning
					}
				}
				peEvent(g, Event{Type: EventTaskUpdated, PlanID: s.Plan.ID, PlanVersion: s.Plan.Version, TaskID: task.ID, AgentName: task.Agent, Title: task.Title, TaskStatus: TaskRunning, DependsOn: task.DependsOn})
			}
		}
		r.mu.Unlock()
		if err != nil {
			g.Send(&adk.AgentEvent{Err: err})
			return
		}
		ctx = r.traceContext(ctx, "researching", taskID)
		privateContext := &taskContextSnapshot{}
		if saved != nil {
			privateContext.Raw = saved.RuntimeContext
		}
		ctx = context.WithValue(ctx, taskContextSnapshotKey{}, privateContext)
		if req.AgentName == "synthesis_review" {
			evidence, readErr := r.engine.taskArtifactEvidence(ctx, req, inputs, artifactInputs)
			if readErr != nil {
				g.Send(&adk.AgentEvent{Err: readErr})
				return
			}
			req.Query += evidence
		}
		agent, ks, err := a.buildAgent(ctx, req)
		if err != nil {
			g.Send(&adk.AgentEvent{Err: err})
			return
		}
		var child *adk.AsyncIterator[*adk.AgentEvent]
		var result TaskResult
		var taskError error
		var childInput *adk.AgentInput
		var childResume *adk.ResumeInfo
		if resume != nil {
			childResume = &adk.ResumeInfo{EnableStreaming: resume.EnableStreaming, WasInterrupted: true, InterruptState: saved.Data}
		} else {
			childInput = &adk.AgentInput{Messages: buildInputMessages(req.Query, req.History, req.ContextData...), EnableStreaming: in.EnableStreaming}
		}
		ctx, finishTrace := startPEProfessionalTrace(ctx, a.role, childInput, childResume)
		defer func() { finishTrace(result, taskError) }()
		if resume != nil {
			child = agent.Resume(ctx, childResume, opts...)
		} else {
			child = agent.Run(ctx, childInput, opts...)
		}
		var answer string
		var artifacts []artifactReference
		var interrupt *adk.AgentEvent
		for {
			ev, ok := child.Next()
			if !ok {
				break
			}
			if ev.Err != nil {
				if isModelRetryNotification(ev.Err) {
					continue
				}
				taskError = ev.Err
				continue
			}
			if ev.Action != nil && ev.Action.Interrupted != nil {
				interrupt = ev
				continue
			}
			if ev.Output != nil && ev.Output.MessageOutput != nil {
				mv := ev.Output.MessageOutput
				msg, mErr := mv.GetMessage()
				if mErr != nil {
					if isModelRetryNotification(mErr) {
						continue
					}
					taskError = mErr
					continue
				}
				if msg == nil {
					continue
				}
				if len(msg.ToolCalls) > 0 {
					for _, call := range msg.ToolCalls {
						peEvent(g, Event{Type: EventToolCall, TaskID: task.ID, AgentName: task.Agent, Title: "调用工具 " + call.Function.Name, Detail: truncateTaskSummary(call.Function.Arguments, 200), Status: TaskRunning})
					}
				} else if mv.Role == schema.Tool {
					var ref artifactReference
					if json.Unmarshal([]byte(msg.Content), &ref) == nil && ref.ArtifactID != "" {
						artifacts = appendArtifactReference(artifacts, ref)
					}
					toolEvent := Event{Type: EventToolResult, TaskID: task.ID, AgentName: task.Agent, Title: "工具执行结果", ToolResult: msg.Content, Status: TaskSuccess}
					r.mu.Lock()
					terminal := s.Denied || s.Blocked || s.Unsafe[task.ID]
					r.mu.Unlock()
					peEvent(g, peToolResultEvent(mv.ToolName, toolEvent, terminal))
				} else if mv.Role == schema.Assistant {
					answer = msg.Content
				}
			}
		}
		if ctx.Err() != nil {
			taskError = ctx.Err()
			return
		}
		result = TaskResult{TaskID: task.ID, Agent: task.Agent, Status: TaskSuccess, Summary: strings.TrimSpace(answer)}
		result.Artifacts = artifacts
		if taskError != nil {
			result.Status = TaskError
			result.Error = taskError.Error()
		} else if interrupt != nil {
			result.Status = TaskWaiting
		} else if result.Summary == "" {
			result.Status = TaskError
			result.Error = "子 Agent 未返回有效结果"
		}
		if ks != nil {
			sources := collectSources(ks.Sources())
			if len(sources) > 0 {
				result.Evidence = []AgentEvidence{{ID: task.ID + "-evidence", AgentName: task.Agent, Summary: result.Summary, Sources: sources}}
			}
		}
		r.mu.Lock()
		s, err = r.session(ctx)
		if err == nil {
			if previous, ok := s.Results[task.ID]; ok && resume != nil {
				result.Evidence = append(previous.Evidence, result.Evidence...)
				for _, ref := range previous.Artifacts {
					result.Artifacts = appendArtifactReference(result.Artifacts, ref)
				}
			}
			if reason := s.Failures[task.ID]; reason != "" && interrupt == nil {
				result.Status, result.Error = TaskError, reason
			} else if reason := s.ArtifactArgumentFailures[task.ID]; reason != "" && taskError == nil && interrupt == nil {
				result.Status, result.Error = TaskError, "产物读取参数尚未纠正："+reason
			}
			applyPEToolProgress(s, &result)
			mergeTaskResult(&s.Plan, s.Results, result)
			detail := truncateTaskSummary(result.Summary, 500)
			if result.Error != "" {
				detail = result.Error
			}
			peEvent(g, Event{Type: EventTaskResult, PlanID: s.Plan.ID, PlanVersion: s.Plan.Version, TaskID: task.ID, AgentName: task.Agent, Title: task.Title, TaskStatus: result.Status, Detail: detail, DependsOn: task.DependsOn})
		}
		r.mu.Unlock()
		if err != nil {
			g.Send(&adk.AgentEvent{Err: err})
			return
		}
		if taskError != nil {
			// 模型、运行时及基础设施错误终止根流程，不能交给 Replanner 自动重试。
			g.Send(&adk.AgentEvent{Err: taskError})
			return
		}
		if interrupt != nil {
			data, ok := interrupt.Action.Interrupted.Data.(*adk.ChatModelAgentInterruptInfo)
			if !ok {
				g.Send(&adk.AgentEvent{Err: fmt.Errorf("缺少子任务恢复数据")})
				return
			}
			ev := adk.CompositeInterrupt(ctx, map[string]any{"task_id": task.ID}, &peChildState{TaskID: task.ID, Data: data.Data, RuntimeContext: privateContext.Raw}, adk.FromInterruptContexts(interrupt.Action.Interrupted.InterruptContexts))
			ev.AgentName = a.role
			g.Send(ev)
			return
		}
		encoded, _ := json.Marshal(result)
		g.Send(adk.EventFromMessage(schema.AssistantMessage(string(encoded), nil), nil, schema.Assistant, ""))
	}()
	return iter
}

func (a *peTaskAgent) buildAgent(ctx context.Context, req Request) (*adk.ChatModelAgent, *tool.KnowledgeSearchTool, error) {
	r := a.runtime
	e := r.engine
	sorted := e.sortedInternalTools()
	buckets, _ := e.bucketsFor(ctx, sorted, req)
	if req.AgentName == "knowledge_research" {
		copyBuckets := make(map[ToolDomain][]einoTool.BaseTool, len(buckets))
		for domain, tools := range buckets {
			copyBuckets[domain] = tools
		}
		copyBuckets[DomainKnowledge] = nil
		for _, entry := range sorted {
			if entry.Domain == DomainKnowledge {
				copyBuckets[DomainKnowledge] = append(copyBuckets[DomainKnowledge], entry.Build(ctx, req.UserID, req.KnowledgeBaseIDs))
			}
		}
		buckets = copyBuckets
	}
	if req.AgentName == "web_research" || req.AgentName == "document_operation" {
		// 从可信任务请求重建操作工具；恢复不沿用新请求的工具选择或权限范围。
		operations := e.buildToolsByDomain(ctx, sorted, req.UserID, req.KnowledgeBaseIDs, []ToolDomain{DomainDocMgmt}, false)
		operations[DomainExternal] = buckets[DomainExternal]
		if e.toolFactory != nil {
			operations[DomainExternal] = nil
			if !req.MCPDisabled {
				operations[DomainExternal] = e.toolFactory.CreateAgentTools(ctx, req.UserID, req.UserToolConfigIDs...)
			}
		}
		buckets = operations
	}
	ts := selectRequestTools(sorted, buckets, req)
	config := buildToolsNodeConfig()
	config.Tools = e.addArtifactTool(req, ts.Tools)
	config.ToolCallMiddlewares = append(config.ToolCallMiddlewares, compose.ToolMiddleware{Invokable: e.artifactMiddleware(req)})
	dangerous := e.dangerousToolNames(ctx, ts.Tools)
	clarify := e.clarifyToolNames()
	// 预算与中间件注入读同一个结果（见 agentIterations）。
	iterations := agentIterations(e.deepSubagentIterations(req), len(dangerous) > 0 || len(clarify) > 0)
	config.ToolCallMiddlewares = append(config.ToolCallMiddlewares, compose.ToolMiddleware{Invokable: a.resultMiddleware(req.TaskID, dangerous)})
	if len(dangerous) > 0 {
		config.ToolCallMiddlewares = append(config.ToolCallMiddlewares, compose.ToolMiddleware{Invokable: buildDangerousToolMiddleware(dangerous)})
	}
	if len(clarify) > 0 {
		config.ToolCallMiddlewares = append(config.ToolCallMiddlewares, compose.ToolMiddleware{Invokable: buildClarifyMiddleware(clarify)})
	}
	instruction := composeSystemPrompt(buildDeepTaskPrompt(ctx, a.role, ts, iterations), req.SystemPrompt)
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: a.role, Description: a.Description(ctx), Instruction: instruction, Model: r.model, ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: e.withToolExecutionTimeout(config)}, MaxIterations: iterations, ModelRetryConfig: e.modelRetryConfig(), Handlers: []adk.ChatModelAgentMiddleware{e.runtimeHandler(req, instruction, r.model), newBudgetGuard(req)}, GenModelInput: func(_ context.Context, instruction string, input *adk.AgentInput) ([]*schema.Message, error) {
		return append([]*schema.Message{schema.SystemMessage(instruction)}, input.Messages...), nil
	}})
	return agent, knowledgeSearchToolOf(ts.Tools), err
}

// 最外层中间件记录真实工具结果，审批拒绝不能被模型的普通文本包装成成功。
func (a *peTaskAgent) resultMiddleware(taskID string, dangerous map[string]bool) compose.InvokableToolMiddleware {
	return func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
			r := a.runtime
			r.mu.Lock()
			s, err := r.session(ctx)
			if err == nil {
				if reason := s.Failures[taskID]; reason != "" && (s.Denied || s.Blocked || s.Unsafe[taskID]) {
					r.mu.Unlock()
					return &compose.ToolOutput{Result: failedToolOutput(reason, 0)}, nil
				}
				if dangerous[input.Name] {
					s.Unsafe[taskID] = true
					target, hasData, approval := compose.GetResumeContext[*ApprovalResult](ctx)
					if target && hasData && approval != nil && !approval.Approved {
						s.Denied = true
						s.Failures[taskID] = "用户拒绝审批，操作未执行"
					}
				}
			}
			r.mu.Unlock()
			if err != nil {
				return nil, err
			}
			output, err := next(ctx, input)
			var biz *bizerrors.BizError
			if errors.As(err, &biz) {
				if biz.Code == bizerrors.CodeForbidden || biz.Code == bizerrors.CodeUnauthorized || biz.Err == nil {
					output, err = &compose.ToolOutput{Result: failedToolOutput(biz.Message, biz.Code)}, nil
				}
			}
			if output != nil && err == nil {
				r.mu.Lock()
				if reason := s.Failures[taskID]; reason != "" && (s.Denied || s.Blocked || s.Unsafe[taskID]) {
					// 审批中间件可能返回纯文本；事件仍以可信拒绝事实判定失败。
					output = &compose.ToolOutput{Result: failedToolOutput(reason, 0)}
				}
				r.mu.Unlock()
				if input.Name == artifactReadToolName {
					message, recoverable := artifactArgumentFailure(input.Name, output.Result)
					r.mu.Lock()
					if recoverable {
						if s.ArtifactArgumentFailures == nil {
							s.ArtifactArgumentFailures = map[string]string{}
						}
						s.ArtifactArgumentFailures[taskID] = message
					} else {
						delete(s.ArtifactArgumentFailures, taskID)
					}
					r.mu.Unlock()
					if recoverable {
						return output, nil
					}
				}
				outcome := classifyToolResult(output.Result)
				r.mu.Lock()
				if input.Name != artifactReadToolName && !r.engine.clarifyToolNames()[input.Name] {
					recordPEToolResult(s, taskID, input.Name, outcome)
				}
				// 只有真实安全边界失败不可被后续查询纠正；一般只读失败由进度记录处理。
				if outcome.Failed && (outcome.Blocked || s.Unsafe[taskID]) {
					s.Failures[taskID] = outcome.Message
					s.Blocked = s.Blocked || outcome.Blocked
				}
				r.mu.Unlock()
			}
			return output, err
		}
	}
}

func peResearchAncestors(plan ExecutionPlan, taskID string) []string {
	seen := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		for _, dep := range taskByID(plan, id).DependsOn {
			if !seen[dep] {
				seen[dep] = true
				visit(dep)
			}
		}
	}
	visit(taskID)
	var ids []string
	for _, task := range plan.Tasks {
		if seen[task.ID] && task.Agent != "synthesis_review" {
			ids = append(ids, task.ID)
		}
	}
	return ids
}

const synthesisInputLimit = 6000

func buildTaskQuery(query string, task TaskSpec, results map[string]TaskResult) string {
	inputs := make([]TaskResult, 0, len(task.DependsOn))
	budget := synthesisInputLimit / max(1, len(task.DependsOn))
	for _, dep := range task.DependsOn {
		if result, ok := results[dep]; ok && strings.TrimSpace(result.Summary) != "" {
			result.Artifacts = append([]artifactReference(nil), result.Artifacts...)
			for i := range result.Artifacts {
				result.Artifacts[i].Preview = ""
				result.Artifacts[i].Read = ""
			}
			result.Summary = truncateTaskSummary(result.Summary, budget)
			result.Evidence = append([]AgentEvidence(nil), result.Evidence...)
			for i := range result.Evidence {
				result.Evidence[i].Summary = ""
				result.Evidence[i].Sources = append([]SourceInfo(nil), result.Evidence[i].Sources...)
				for j := range result.Evidence[i].Sources {
					source := &result.Evidence[i].Sources[j]
					source.Chunks = append([]ChunkSource(nil), source.Chunks...)
					for k := range source.Chunks {
						source.Chunks[k].Content = ""
						source.Chunks[k].Quote = ""
					}
				}
			}
			inputs = append(inputs, result)
		}
	}
	if len(inputs) == 0 {
		return query
	}
	data, _ := json.Marshal(inputs)
	return query + "\n\n前置研究结果（JSON 证据数据）：\n" + string(data)
}

func truncateTaskSummary(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
