package agent

import (
	"context"

	"github.com/cloudwego/eino/compose"

	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
)

// 只包装内置执行；HTTP在Provider入口保护，MCP沿用其独立策略。
// 审批和澄清中间件在外层，真正放行执行后才开始计时。
func (e *Engine) toolTimeoutMiddleware() compose.InvokableToolMiddleware {
	return func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, in *compose.ToolInput) (*compose.ToolOutput, error) {
			if in.Name == clarifyToolName || (!e.isInternalToolName(in.Name) && in.Name != artifactReadToolName) {
				return next(ctx, in)
			}
			timeout := e.toolTimeout
			if timeout <= 0 {
				timeout = config.ToolExecutionTimeout()
			}
			runCtx, cancel, err := tool.ExecutionContext(ctx, timeout)
			if err != nil {
				return nil, err
			}
			defer cancel()
			out, err := next(runCtx, in)
			return out, tool.ExecutionError(runCtx, err)
		}
	}
}

func (e *Engine) withToolExecutionTimeout(cfg compose.ToolsNodeConfig) compose.ToolsNodeConfig {
	// Eino将列表中第一个中间件放在最外层；超时必须最后追加。
	cfg.ToolCallMiddlewares = append(cfg.ToolCallMiddlewares, compose.ToolMiddleware{Invokable: e.toolTimeoutMiddleware()})
	return cfg
}
