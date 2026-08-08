package agent

import (
	"context"
	"encoding/gob"
	"fmt"

	"github.com/cloudwego/eino/compose"

	"solvify-agent/pkg/logger"
)

// DangerousToolState 审批中间件持久化到 checkpoint 的状态
type DangerousToolState struct {
	ToolName  string `json:"tool_name"`
	Arguments string `json:"arguments"`
}

func init() {
	// gob 序列化 checkpoint 时需要能识别 DangerousToolState 这个 interface 实现类型
	// 只注册值类型，避免同类型值/指针重复注册导致 panic
	gob.Register(DangerousToolState{})
}

// buildDangerousToolMiddleware 构建统一的危险工具审批中间件。
// 所有 dangerousNames 里列出的工具名，在实际执行前都会被拦截：
//   1. 首次执行 → StatefulInterrupt 暂停，checkpoint 保存当前参数
//   2. 用户审批后恢复 → GetResumeContext 拿审批结果
//      - "approve"/"同意"/"确认" → 放行 next() 执行真实业务逻辑
//      - 其他 → 拒绝，返回"操作被取消"
//
// 工具本身（如 DeleteDocumentTool）不再需要写任何 Interrupt/Resume 代码，
// 只关心自己的业务逻辑即可。
func buildDangerousToolMiddleware(dangerousNames map[string]bool) compose.InvokableToolMiddleware {
	return func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
			// 不是危险工具 → 直接放行
			if !dangerousNames[input.Name] {
				return next(ctx, input)
			}

			// ── 检查是否从上次中断恢复 ──
			wasInterrupted, hasState, state := compose.GetInterruptState[DangerousToolState](ctx)

			if !wasInterrupted {
				// ── 首次执行：中断等待审批 ──
				// info 用 string（gob 原生类型，不需要额外注册）；不要用 map[string]any 这种 gob 不认识的类型
				// state 用 DangerousToolState（init 里已经 gob.Register 过）
				info := fmt.Sprintf("即将执行危险工具 %s，请确认是否继续", input.Name)
				logger.Infof("[ToolMiddleware] 危险工具 %s 触发审批中断, args=%s", input.Name, truncateStr(input.Arguments, 200))
				return nil, compose.StatefulInterrupt(ctx, info, DangerousToolState{
					ToolName:  input.Name,
					Arguments: input.Arguments,
				})
			}

			// ── 恢复执行：拿审批结果 ──
			isResumeFlow, hasData, approvalResult := compose.GetResumeContext[string](ctx)
			if !isResumeFlow || !hasData {
				logger.Warnf("[ToolMiddleware] 恢复流程异常：wasInterrupted=%v, isResumeFlow=%v, hasData=%v",
					wasInterrupted, isResumeFlow, hasData)
				return &compose.ToolOutput{Result: "恢复流程异常：未收到审批结果"}, nil
			}

			// 用 state 里的原始参数（防恢复时 LLM 重新生成导致不一致）
			// 但 input.Arguments 在 resume 时也是正确的 checkpoint 回放值，两者一致
			if hasState && state.ToolName != "" {
				logger.Infof("[ToolMiddleware] 恢复执行: tool=%s, approval=%q (state.tool=%s)",
					input.Name, approvalResult, state.ToolName)
			} else {
				logger.Infof("[ToolMiddleware] 恢复执行: tool=%s, approval=%q", input.Name, approvalResult)
			}

			switch approvalResult {
			case "approve", "同意", "确认", "yes", "y", "ok":
				return next(ctx, input) // 放行真实业务逻辑

			case "reject", "拒绝", "取消", "no", "n":
				logger.Infof("[ToolMiddleware] 用户拒绝执行危险工具 %s", input.Name)
				return &compose.ToolOutput{
					Result: fmt.Sprintf("❌ 操作被用户拒绝：%s 未执行", input.Name),
				}, nil

			default:
				logger.Warnf("[ToolMiddleware] 审批结果 %q 无法识别，默认拒绝 %s", approvalResult, input.Name)
				return &compose.ToolOutput{
					Result: fmt.Sprintf("⚠️ 审批结果 %q 无法识别，默认不执行 %s", approvalResult, input.Name),
				}, nil
			}
		}
	}
}
