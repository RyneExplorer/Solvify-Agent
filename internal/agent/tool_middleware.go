package agent

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/compose"

	"solvify-agent/pkg/logger"
)

// DangerousToolState 审批中间件持久化到 checkpoint 的状态
type DangerousToolState struct {
	ToolName  string `json:"tool_name"`
	Arguments string `json:"arguments"`
}

// ClarifyState 澄清追问中间件持久化到 checkpoint 的状态
type ClarifyState struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Context  string   `json:"context,omitempty"` // LLM 为什么要澄清
}

func init() {
	// gob 序列化 checkpoint 时需要能识别这些 interface 实现类型
	gob.Register(DangerousToolState{})
	gob.Register(ClarifyState{})
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
				info := marshalInterruptInfo("danger", map[string]any{
					"tool_name": input.Name,
					"arguments": input.Arguments,
					"message":   fmt.Sprintf("即将执行危险工具 %s，请确认是否继续", input.Name),
				})
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

// buildClarifyMiddleware 构建澄清追问中间件。
// ask_clarify 工具被 LLM 调用时触发 Interrupt，前端显示澄清问题/选项，
// 用户回答后从 checkpoint 恢复，LLM 继续推理。
func buildClarifyMiddleware(clarifyNames map[string]bool) compose.InvokableToolMiddleware {
	return func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
			if !clarifyNames[input.Name] {
				return next(ctx, input)
			}

			wasInterrupted, _, state := compose.GetInterruptState[ClarifyState](ctx)

			if !wasInterrupted {
				q := extractClarifyQuestion(input.Arguments)
				info := marshalInterruptInfo("clarify", map[string]any{
					"question": q,
					"options":  extractClarifyOptions(input.Arguments),
					"context":  extractClarifyContext(input.Arguments),
				})
				logger.Infof("[ClarifyMiddleware] ask_clarify 触发中断: args=%s", truncateStr(input.Arguments, 200))
				return nil, compose.StatefulInterrupt(ctx, info, ClarifyState{
					Question: q,
					Options:  extractClarifyOptions(input.Arguments),
					Context:  extractClarifyContext(input.Arguments),
				})
			}

			// 恢复执行：拿用户的回答
			isResumeFlow, hasData, answer := compose.GetResumeContext[string](ctx)
			if !isResumeFlow || !hasData {
				logger.Warnf("[ClarifyMiddleware] 恢复流程异常：wasInterrupted=%v, isResumeFlow=%v", wasInterrupted, isResumeFlow)
				return &compose.ToolOutput{Result: "恢复流程异常：未收到用户回答"}, nil
			}

			logger.Infof("[ClarifyMiddleware] 恢复执行: question=%q, answer=%q", state.Question, answer)

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("✅ 用户已回答澄清问题\n\n问题：%s\n\n回答：%s", state.Question, answer))
			return &compose.ToolOutput{Result: sb.String()}, nil
		}
	}
}

func extractClarifyQuestion(argsJSON string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return ""
	}
	if q, ok := m["question"].(string); ok {
		return q
	}
	return ""
}

func extractClarifyOptions(argsJSON string) []string {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return nil
	}
	raw, ok := m["options"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func extractClarifyContext(argsJSON string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return ""
	}
	if c, ok := m["context"].(string); ok {
		return c
	}
	return ""
}

type interruptInfoSchema struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data,omitempty"`
}

func marshalInterruptInfo(typ string, data map[string]any) string {
	b, err := json.Marshal(interruptInfoSchema{Type: typ, Data: data})
	if err != nil {
		logger.Warnf("[Middleware] marshalInterruptInfo 失败: %v", err)
		return fmt.Sprintf(`{"type":"%s"}`, typ)
	}
	return string(b)
}

func parseInterruptInfo(infoStr string) (typ string, data map[string]any) {
	var s interruptInfoSchema
	if err := json.Unmarshal([]byte(infoStr), &s); err == nil && s.Type != "" {
		return s.Type, s.Data
	}
	// 兼容旧格式：尝试直接当 map 解析
	var m map[string]any
	if err := json.Unmarshal([]byte(infoStr), &m); err == nil {
		if t, ok := m["type"].(string); ok {
			if d, ok := m["data"].(map[string]any); ok {
				return t, d
			}
		}
	}
	return "", nil
}
