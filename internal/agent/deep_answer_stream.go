package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/eventch"
)

// 只对最终 respond 的 JSON 字符串增量解码。Runner 保持非流式，避免框架
// 为消息注入 ID 时与异步追踪共享可变消息；不增加模型调用或绕过输入预算。
func (m *peControlModel) readAnswerStream(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := m.inner.Stream(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	var chunks []*schema.Message
	var arguments strings.Builder
	var name string
	decoder := &peResponseDecoder{}
	var planID string
	var version int
	checked := false
	var sent strings.Builder
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if chunk == nil {
			continue
		}
		chunks = append(chunks, chunk)
		if len(chunk.ToolCalls) == 0 {
			continue
		}
		if len(chunk.ToolCalls) != 1 {
			return nil, fmt.Errorf("重规划必须只返回一个工具调用")
		}
		call := chunk.ToolCalls[0]
		if call.Index != nil && *call.Index != 0 {
			return nil, fmt.Errorf("重规划包含多个工具调用")
		}
		// Eino 的工具名是原子字段；部分供应商会在后续片段重复它。
		if call.Function.Name != "" {
			if name != "" && name != call.Function.Name {
				return nil, fmt.Errorf("重规划工具名发生变化")
			}
			name = call.Function.Name
		}
		arguments.WriteString(call.Function.Arguments)
		if name != "respond" {
			continue
		}
		if !checked {
			r := m.runtime
			r.mu.Lock()
			s, stateErr := r.session(ctx)
			if stateErr == nil {
				// 文本尚未生成；先验证可信任务状态，完整回答仍在阶段结束时校验。
				stateErr = validatePECompletion(s, "待生成最终回答")
				planID, version = s.Plan.ID, s.Plan.Version
			}
			r.mu.Unlock()
			if stateErr != nil {
				return nil, stateErr
			}
			checked = true
		}
		text, err := decoder.push(arguments.String())
		arguments.Reset()
		if err != nil {
			return nil, fmt.Errorf("最终回答流格式无效（%v）: %w", err, errModelOutputInvalid)
		}
		if text != "" {
			sent.WriteString(text)
			eventch.Send(ctx, m.runtime.answerOutput, Event{Type: EventAnswer, Content: text, PlanID: planID, PlanVersion: version, Phase: "replanning"})
		}
	}
	msg, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, fmt.Errorf("模型响应流无法合并（%v）: %w", err, errModelOutputInvalid)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 先判定结束原因，避免把输出截断误报为 JSON 格式错误或空回答。
	if err = validateModelCompletion(msg); err != nil {
		return nil, err
	}
	if len(msg.ToolCalls) == 1 && msg.ToolCalls[0].Function.Name == "respond" {
		var response struct {
			Response string `json:"response"`
		}
		if !checked || json.Unmarshal([]byte(msg.ToolCalls[0].Function.Arguments), &response) != nil || !decoder.done || response.Response != sent.String() {
			return nil, fmt.Errorf("最终回答流格式无效: %w", errModelOutputInvalid)
		}
	} else if checked {
		return nil, fmt.Errorf("最终回答流包含非法工具调用")
	}
	return msg, nil
}

// pending 仅保留未完整的 UTF-8 字符或 JSON 转义，包括拆开的 UTF-16 代理对。
type peResponseDecoder struct {
	header  string
	pending string
	value   bool
	started bool
	done    bool
}

func (d *peResponseDecoder) push(fragment string) (string, error) {
	d.pending += fragment
	var out strings.Builder
	for len(d.pending) > 0 && !d.done {
		c := d.pending[0]
		if !d.value {
			d.header += string(c)
			d.pending = d.pending[1:]
			if c == ':' {
				var fields map[string]json.RawMessage
				if json.Unmarshal([]byte(d.header+"null}"), &fields) != nil || len(fields) != 1 || fields["response"] == nil {
					return "", fmt.Errorf("最终回答 response 字段无效")
				}
				d.value = true
			}
			continue
		}
		if !d.started {
			d.pending = d.pending[1:]
			if c == ' ' || c == '\n' || c == '\r' || c == '\t' {
				continue
			}
			if c != '"' {
				return "", fmt.Errorf("最终回答必须是字符串")
			}
			d.started = true
			continue
		}
		if c == '"' {
			d.done = true
			break
		}
		if c == '\\' {
			n := 2
			if len(d.pending) < n {
				break
			}
			if d.pending[1] == 'u' {
				n = 6
				if len(d.pending) < n {
					break
				}
				code, err := strconv.ParseUint(d.pending[2:6], 16, 16)
				if err != nil {
					return "", fmt.Errorf("最终回答 Unicode 转义无效")
				}
				if code >= 0xD800 && code <= 0xDBFF {
					n = 12
					if len(d.pending) < n {
						break
					}
				}
			}
			var text string
			if err := json.Unmarshal([]byte(`"`+d.pending[:n]+`"`), &text); err != nil {
				return "", fmt.Errorf("最终回答 JSON 转义无效: %w", err)
			}
			out.WriteString(text)
			d.pending = d.pending[n:]
			continue
		}
		if !utf8.FullRuneInString(d.pending) {
			break
		}
		r, n := utf8.DecodeRuneInString(d.pending)
		if c < 0x20 || (r == utf8.RuneError && n == 1) {
			return "", fmt.Errorf("最终回答包含非法字符")
		}
		out.WriteString(d.pending[:n])
		d.pending = d.pending[n:]
	}
	return out.String(), nil
}
