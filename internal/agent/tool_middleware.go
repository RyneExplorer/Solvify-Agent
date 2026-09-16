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

// ApprovalResult 对齐官方 eino-examples/adk/common/tool/approval_wrapper.go 的结构化审批结果。
// 作为 ResumeWithParams 的 Targets 值类型使用，替代原先"同意/approve"等字符串子串匹配，
// 与官方 Host→Resume 的数据契约保持一致。
type ApprovalResult struct {
	Approved         bool    `json:"approved"`
	DisapproveReason *string `json:"disapprove_reason,omitempty"`
}

// buildDangerousToolMiddleware 构建统一的危险工具审批中间件。
// 实现严格对齐官方 eino-examples/adk/common/tool/approval_wrapper.go 的 InvokableApprovableTool：
//  1. 首次执行 → StatefulInterrupt 暂停（持久化当前参数）
//  2. 恢复时按 isResumeTarget 分流：
//     - 是本次恢复目标且有数据 → 看 ApprovalResult.Approved 决定放行 / 拒绝
//     - 不是本次恢复目标（指向其它并发 interrupt）→ 重新 StatefulInterrupt 保活（官方 MUST）
//     - 是恢复目标但无数据 → 直接放行（与官方语义一致：isResumeTarget&&!hasData 走 Run）
//
// 工具本身（如 DeleteDocumentTool）不再需要写任何 Interrupt/Resume 代码。
func buildDangerousToolMiddleware(dangerousNames map[string]bool) compose.InvokableToolMiddleware {
	return func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
			// 不是危险工具 → 直接放行
			if !dangerousNames[input.Name] {
				return next(ctx, input)
			}

			// 检查是否从上次中断恢复（wasInterrupted 与 state 类型无关，仅取布尔）
			wasInterrupted, _, _ := compose.GetInterruptState[DangerousToolState](ctx)

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

			// 恢复执行：拿结构化审批结果（Host 在 ResumeWithParams 的 Targets 中放入 *ApprovalResult）
			isResumeTarget, hasData, approval := compose.GetResumeContext[*ApprovalResult](ctx)
			if isResumeTarget && hasData {
				if approval.Approved {
					logger.Infof("[ToolMiddleware] 用户审批通过，放行执行危险工具 %s", input.Name)
					return next(ctx, input) // 放行真实业务逻辑
				}
				if approval.DisapproveReason != nil {
					logger.Infof("[ToolMiddleware] 用户拒绝执行危险工具 %s, reason=%s", input.Name, *approval.DisapproveReason)
					return &compose.ToolOutput{
						Result: fmt.Sprintf("❌ 操作被用户拒绝：%s 未执行（原因：%s）", input.Name, *approval.DisapproveReason),
					}, nil
				}
				logger.Infof("[ToolMiddleware] 用户拒绝执行危险工具 %s", input.Name)
				return &compose.ToolOutput{
					Result: fmt.Sprintf("❌ 操作被用户拒绝：%s 未执行", input.Name),
				}, nil
			}

			// 本次恢复未指向当前工具（指向其它并发 interrupt）：重新中断保活，等待自己的审批信号
			// 对齐官方 approval_wrapper.go:84-90 的 MUST re-interrupt 约定
			if !isResumeTarget {
				logger.Infof("[ToolMiddleware] 危险工具 %s 不是本次恢复目标，重新中断保活", input.Name)
				info := marshalInterruptInfo("danger", map[string]any{
					"tool_name": input.Name,
					"arguments": input.Arguments,
					"message":   fmt.Sprintf("即将执行危险工具 %s，请确认是否继续", input.Name),
				})
				return nil, compose.StatefulInterrupt(ctx, info, DangerousToolState{
					ToolName:  input.Name,
					Arguments: input.Arguments,
				})
			}

			// isResumeTarget=true 但无数据：对齐官方语义，直接放行执行
			logger.Warnf("[ToolMiddleware] 危险工具 %s 为恢复目标但无审批数据，按官方语义放行", input.Name)
			return next(ctx, input)
		}
	}
}

// buildClarifyMiddleware 构建澄清追问中间件。
// 实现严格对齐官方 eino-examples/adk/common/tool/follow_up_tool.go 的 FollowUpTool：
//  1. 首次执行 → StatefulInterrupt 暂停（持久化问题/选项）
//  2. 恢复时按 isResumeTarget 分流：
//     - 是本次恢复目标且用户已回答 → 返回用户回答，LLM 继续推理
//     - 不是本次恢复目标（指向其它并发 interrupt）→ 重新 StatefulInterrupt 保活
//     - 是恢复目标但无回答 → 返回错误（对齐官方 "tool resumed without a user answer"）
//
// ask_clarify 工具被 LLM 调用时触发 Interrupt，前端显示澄清问题/选项，用户回答后从 checkpoint 恢复。
func buildClarifyMiddleware(clarifyNames map[string]bool) compose.InvokableToolMiddleware {
	return func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
			if !clarifyNames[input.Name] {
				return next(ctx, input)
			}

			wasInterrupted, hasState, state := compose.GetInterruptState[ClarifyState](ctx)

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

			// 恢复执行：拿用户的回答（字符串，与官方 FollowUpInfo.UserAnswer 语义一致）
			isResumeTarget, hasData, answer := compose.GetResumeContext[string](ctx)
			if !isResumeTarget {
				// 本次恢复未指向当前 clarify：重新中断保活
				logger.Infof("[ClarifyMiddleware] 当前 clarify 不是本次恢复目标，重新中断保活")
				q := extractClarifyQuestion(input.Arguments)
				info := marshalInterruptInfo("clarify", map[string]any{
					"question": q,
					"options":  extractClarifyOptions(input.Arguments),
					"context":  extractClarifyContext(input.Arguments),
				})
				return nil, compose.StatefulInterrupt(ctx, info, ClarifyState{
					Question: q,
					Options:  extractClarifyOptions(input.Arguments),
					Context:  extractClarifyContext(input.Arguments),
				})
			}

			if !hasData || answer == "" {
				return nil, fmt.Errorf("tool resumed without a user answer")
			}

			question := ""
			if hasState {
				question = state.Question
			}
			logger.Infof("[ClarifyMiddleware] 恢复执行: question=%q, answer=%q", question, answer)
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("✅ 用户已回答澄清问题\n\n问题：%s\n\n回答：%s", question, answer))
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
