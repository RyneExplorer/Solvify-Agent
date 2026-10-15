package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/google/uuid"

	"solvify-agent/internal/llm"
)

// Execute 启动 Agent 执行流程，通过事件通道异步返回推理结果
func (e *Engine) Execute(ctx context.Context, req Request, chatModel model.ToolCallingChatModel) (<-chan Event, error) {
	eventCh := make(chan Event, 100)

	go func() {
		defer close(eventCh)
		if limits, ok := chatModel.(interface{ ContextWindow() int }); ok && req.MaxContextLength <= 0 {
			req.MaxContextLength = limits.ContextWindow()
		}
		if limits, ok := chatModel.(interface{ OutputLimit() int }); ok {
			req.OutputLimit = limits.OutputLimit()
		}
		if limits, ok := chatModel.(interface{ InputLimit() int }); ok {
			req.InputLimit = limits.InputLimit()
		}
		var restoreErr error
		req, restoreErr = e.restoreContextRequest(ctx, req)
		if restoreErr != nil {
			e.emitPlanError(ctx, eventCh, restoreErr)
			return
		}
		if req.RunID == "" {
			req.RunID = uuid.NewString()
		}
		ctx = llm.WithBudgetIdentity(ctx, req.ModelID, req.MaxContextLength)
		chatModel = &budgetModel{inner: chatModel, guard: newBudgetGuard(req, "")}
		if req.effectiveProfile() == ProfileQuick {
			e.runQuickAgent(ctx, req, chatModel, eventCh)
			return
		}
		planExecute := strings.HasPrefix(req.CheckpointID, "deeppe-v1-") || (req.CheckpointID == "" && e.cfg.DeepPlanExecuteEnabled)
		if planExecute {
			e.runPlanExecute(ctx, req, chatModel, eventCh)
		} else if strings.HasPrefix(req.CheckpointID, "deep-") {
			e.resumeLegacyDeepOrchestrator(ctx, req, chatModel, eventCh)
		} else {
			e.runDeepAgent(ctx, req, chatModel, eventCh)
		}
	}()

	return eventCh, nil
}
