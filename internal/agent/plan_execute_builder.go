package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

func (r *peRuntime) describeTools(ctx context.Context, buckets map[ToolDomain][]einoTool.BaseTool) error {
	r.externalNames, r.documentNames = nil, nil
	for _, domain := range []ToolDomain{DomainExternal, DomainDocMgmt} {
		for _, t := range buckets[domain] {
			info, err := t.Info(ctx)
			if err != nil {
				return fmt.Errorf("读取外部工具描述失败: %w", err)
			}
			if info == nil || info.Name == "" {
				return fmt.Errorf("操作工具描述无效")
			}
			description := info.Name + "：" + info.Desc
			if domain == DomainExternal {
				r.externalNames = append(r.externalNames, description)
			} else {
				r.documentNames = append(r.documentNames, description)
			}
		}
	}
	r.external, r.document = len(r.externalNames) > 0, len(r.documentNames) > 0
	return nil
}

func (r *peRuntime) plannerInstruction() string {
	return fmt.Sprintf(`你是 Solvify Planner，只规划，不执行研究。必须调用 plan 工具提交结构化计划，不得用普通文本回答。可用角色 knowledge_research、synthesis_review；web_research 可用=%t。
角色职责：knowledge_research 仅查询知识库；web_research 仅使用用户配置的外部工具；document_operation 仅使用系统文档管理工具；synthesis_review 仅生成文本与汇总，不具备工具权限，不能执行工具操作或触发审批。
用户配置工具（名称与用途）：%s。只有用途支持联网查询的工具才可安排联网研究；用户工具非空不代表有联网能力。
系统文档操作可用=%t，工具：%s。文档操作分配 document_operation，之后安排依赖该结果的 synthesis_review。缺少对应能力时汇总中明确说明限制，不安排虚假的研究或无工具重试。
每版最多 %d 个待执行任务，并行允许=%t。strategy 为 direct/sequential/parallel。
每个任务必须输出 depends_on，无依赖填 []，汇总填写所依赖的研究任务 ID；依赖必须存在且无环；最后一个汇总必须覆盖全部有效研究。
简单问题只有 synthesis_review。任务 ID 唯一且稳定；历史终态任务不可改写，重试或新汇总用新 ID。
重规划必须填写 replan_reason，保留已成功任务的原描述和依赖，移除不再需要的待执行任务。
不得规划文档、视频或 PPT 等未实现功能。`, r.external, strings.Join(r.externalNames, "、"), r.document, strings.Join(r.documentNames, "、"), deepMaxPlanTasks(r.engine.cfg), r.engine.cfg.DeepParallelEnabled)
}

func (r *peRuntime) build(ctx context.Context) (adk.ResumableAgent, error) {
	planSchema := schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
		"strategy": {Type: schema.String, Required: true}, "replan_reason": {Type: schema.String},
		"tasks": {Type: schema.Array, Required: true, ElemInfo: &schema.ParameterInfo{Type: schema.Object, SubParams: map[string]*schema.ParameterInfo{
			"id": {Type: schema.String, Required: true}, "agent": {Type: schema.String, Required: true}, "title": {Type: schema.String, Required: true}, "description": {Type: schema.String}, "depends_on": {Type: schema.Array, Required: true, ElemInfo: &schema.ParameterInfo{Type: schema.String}},
		}}},
	})
	planTool := &schema.ToolInfo{Name: "plan", Desc: "生成或更新结构化任务计划", ParamsOneOf: planSchema}
	newPlan := func(context.Context) planexecute.Plan { return &pePlan{} }
	buckets, _ := r.engine.bucketsFor(ctx, r.engine.sortedInternalTools(), r.request)
	if err := r.describeTools(ctx, buckets); err != nil {
		return nil, err
	}
	planner, err := planexecute.NewPlanner(ctx, &planexecute.PlannerConfig{ToolCallingChatModel: &peControlModel{inner: r.model, runtime: r}, ToolInfo: planTool, NewPlan: newPlan, GenInputFn: func(_ context.Context, input []adk.Message) ([]adk.Message, error) {
		return append([]adk.Message{schema.SystemMessage(composeSystemPrompt(r.plannerInstruction(), r.request.SystemPrompt))}, input...), nil
	}})
	if err != nil {
		return nil, err
	}
	var tools []einoTool.BaseTool
	// 原生 checkpoint 根据工具名恢复子 Agent，委派结构不能随新请求的工具选择变化。
	// 实际可用能力仍由计划校验及专业 Agent 的工具权限限制。
	for _, role := range []string{"knowledge_research", "web_research", "document_operation", "synthesis_review"} {
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
	replanner, err := planexecute.NewReplanner(ctx, &planexecute.ReplannerConfig{ChatModel: &peControlModel{inner: r.model, runtime: r}, PlanTool: planTool, NewPlan: newPlan, GenInputFn: func(ctx context.Context, in *planexecute.ExecutionContext) ([]adk.Message, error) {
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
		return []*schema.Message{schema.SystemMessage(r.plannerInstruction() + "\n你是 Replanner：评估完成情况，调用 plan 调整或保留剩余任务；仅全部必要任务完成且最新汇总覆盖全部成功研究时调用 respond。失败只允许一次只读重试或替代研究，副作用失败与审批拒绝不得绕过。respond.response 必须基于汇总，保留引用，不输出计划 JSON。"), schema.UserMessage(s.Request.Query + "\n执行状态：" + string(data))}, nil
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
