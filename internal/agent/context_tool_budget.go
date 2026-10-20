package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/llm"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/tokenutil"
)

// 原文存储额度与模型阅读额度分开；无模型窗口的旧恢复路径沿用原策略。
func (e *Engine) artifactReadingPolicy(req Request) config.RuntimeContextPolicy {
	p := e.runtimePolicy()
	budget, err := requestContextBudget(req)
	if err != nil {
		return p
	}
	base := buildInputMessages(req.Query, req.History, req.ContextData...)
	remaining := budget.Target - llm.MessagesTokens(base, req.ModelName) - tokenutil.CountTokens(req.SystemPrompt, req.ModelName)
	p.ReadTokens = max(1, int(float64(max(0, remaining))*p.ToolContextShare()))
	p.ReadRunes = int(min(p.ArtifactBytes, 1_000_000))
	return p
}

func requestContextBudget(req Request) (llm.ContextBudget, error) {
	p := config.ContextDefaults()
	output := req.OutputLimit
	if output <= 0 {
		output = p.OutputLimit
	}
	b, err := llm.ResolveContextBudget(req.ModelID, req.ModelName, req.MaxContextLength, output, req.effectiveProfile() == ProfileQuick, p)
	return b.WithInputLimit(req.InputLimit), err
}

func artifactReadBudgetKey(scope repository.ArtifactScope) string {
	return "SolvifyArtifactReadBudgetV1:" + contentHash(scope.UserID+"\x00"+scope.SessionID+"\x00"+scope.RunID+"\x00"+scope.AgentID+"\x00"+scope.TaskID)
}

// 每轮重新计算工具阅读余量；旧的大结果可缩减，已读取的精确片段和必要指令先计费。
// 并行工具读取同一个只读额度，最终消息仍统一分配，避免共享递减计数器的时序竞争。
func (m *runtimeContextMiddleware) publishArtifactReadBudget(ctx context.Context, state *adk.ChatModelAgentState, budget llm.ContextBudget) {
	used := llm.ToolsTokens(state.ToolInfos, m.req.ModelName) + llm.MessageTokens(schema.SystemMessage(m.instruction), m.req.ModelName)
	for _, msg := range state.Messages {
		var ref artifactReference
		if msg != nil && msg.Role == schema.Tool && json.Unmarshal([]byte(msg.Content), &ref) == nil && ref.ArtifactID != "" && ref.Hash != "" && ref.Selection == nil && ref.Runes > 0 {
			ref.Content, ref.Preview = "", ""
			raw, _ := json.Marshal(ref)
			copy := *msg
			copy.Content = string(raw)
			used += llm.MessageTokens(&copy, m.req.ModelName)
		} else {
			used += llm.MessageTokens(msg, m.req.ModelName)
		}
	}
	allowance := max(1, int(float64(max(0, budget.Target-used))*m.engine.runtimePolicy().ToolContextShare()))
	adk.AddSessionValue(ctx, artifactReadBudgetKey(artifactScope(m.req)), allowance)
}

// 在全部并行工具返回后统一选择可见内容，不在工具 goroutine 中竞争剩余额度。
// 返回新消息，原文与原工具结果保留在产物/Runner状态中。
func (e *Engine) expandToolContext(ctx context.Context, req Request, state *adk.ChatModelAgentState) (*adk.ChatModelAgentState, error) {
	if e.artifactRepo == nil {
		return state, nil
	}
	budget, err := requestContextBudget(req)
	if err != nil {
		return state, nil // 原有预算边界负责未知模型和调用级输出修正。
	}
	messages := append([]*schema.Message(nil), state.Messages...)
	for i, msg := range messages {
		if msg == nil || msg.Role != schema.Tool {
			continue
		}
		var ref artifactReference
		if json.Unmarshal([]byte(msg.Content), &ref) != nil || ref.ArtifactID == "" || ref.Hash == "" || ref.Selection != nil || ref.Runes <= 0 {
			continue
		}
		a, err := findArtifactWithGrants(ctx, e.artifactRepo, artifactScope(req), req.artifactReads, ref.ArtifactID)
		if err != nil {
			return nil, fmt.Errorf("读取工具上下文原文失败: %w", err)
		}
		if a.Version != ref.Version || a.Hash != ref.Hash || contentHash(a.Content) != ref.Hash {
			return nil, fmt.Errorf("工具上下文原文校验失败")
		}
		ref.Content, ref.Complete, ref.Preview = a.Content, true, ""
		ref.NextOffset = ref.Runes
		ref.Read = "完整原文在content中；仅在缺少所需证据时继续读取。"
		raw, _ := json.Marshal(ref)
		copy := *msg
		copy.Content = string(raw)
		messages[i] = &copy
	}
	messages, _ = fitStoredToolResults(messages, req.ModelName, budget, llm.ToolsTokens(state.ToolInfos, req.ModelName))
	next := *state
	next.Messages = messages
	return &next, nil
}

// 只缩减有可靠原文引用的结果；无存储的正文不能被静默丢弃。
// 每轮所有结果共享额度，保留全部ToolCallID、错误事实和有效JSON。
func fitStoredToolResults(messages []*schema.Message, modelName string, budget llm.ContextBudget, fixed int) ([]*schema.Message, int) {
	if llm.MessagesTokens(messages, modelName)+fixed <= budget.Soft {
		return messages, 0
	}
	type candidate struct {
		index int
		ref   artifactReference
		cost  int
	}
	var candidates []candidate
	base := fixed
	for i, msg := range messages {
		if msg == nil {
			continue
		}
		var ref artifactReference
		if msg.Role == schema.Tool && !protectedToolResult(msg.Content) && json.Unmarshal([]byte(msg.Content), &ref) == nil && ref.ArtifactID != "" && ref.Hash != "" && ref.Content != "" {
			if ref.Selection == nil && ref.TotalRunes > 0 {
				ref.Selection = &artifactSelection{Kind: "source"}
			}
			candidates = append(candidates, candidate{i, ref, tokenutil.CountTokens(msg.Content, modelName)})
			copy := *msg
			copy.Content = ""
			base += llm.MessageTokens(&copy, modelName)
		} else {
			base += llm.MessageTokens(msg, modelName)
		}
	}
	remaining := max(0, budget.Target-base)
	out := append([]*schema.Message(nil), messages...)
	changed := 0
	// 小结果先完整保留，较大的结果共享余下额度，不按完成先后争抢。
	for len(candidates) > 0 {
		selected := 0
		for i := range candidates {
			if candidates[i].cost < candidates[selected].cost {
				selected = i
			}
		}
		c := candidates[selected]
		allowance := remaining / len(candidates)
		raw := projectArtifact(c.ref, modelName, allowance)
		copy := *messages[c.index]
		copy.Content = raw
		out[c.index] = &copy
		remaining = max(0, remaining-tokenutil.CountTokens(raw, modelName))
		if raw != messages[c.index].Content {
			changed++
		}
		candidates = append(candidates[:selected], candidates[selected+1:]...)
	}
	return out, changed
}

func projectArtifact(ref artifactReference, modelName string, allowance int) string {
	full, _ := json.Marshal(ref)
	if tokenutil.CountTokens(string(full), modelName) <= allowance {
		return string(full)
	}
	original := ref.Content
	ref.Preview, ref.Content, ref.Complete = "", "", false
	ref.Read = "这里只展示部分原文；使用read_context_artifact按keyword、section或json_path定位，或从next_offset续读。"
	structured := json.Valid([]byte(original)) && ref.Selection == nil
	encode := func(content string) string {
		copy := ref
		copy.Content = content
		copy.NextOffset = copy.Offset + len([]rune(content))
		if structured {
			copy.NextOffset = 0
		}
		raw, _ := json.Marshal(copy)
		return string(raw)
	}
	if structured {
		// 结构投影的序列化位置不等于原文偏移，必须使用定位器补读。
		ref.Read = "JSON仅含可容纳的完整记录，存在省略；请用chunk_id或json_path定位原文，勿用投影长度推算原文偏移。"
		if selected, ok := projectJSONMembers(original, func(content string) bool {
			return tokenutil.CountTokens(encode(content), modelName) <= allowance
		}); ok {
			return encode(selected)
		}
		return encode("")
	}
	// 截取的是JSON字符串字段，整个返回对象始终可反序列化。
	runes := []rune(original)
	low, high := 0, len(runes)
	result := encode("")
	for low <= high {
		mid := low + (high-low)/2
		raw := encode(string(runes[:mid]))
		if tokenutil.CountTokens(raw, modelName) <= allowance {
			result, low = raw, mid+1
		} else {
			high = mid - 1
		}
	}
	return result
}

// 关键词查找以字符返回偏移，不让模型猜大文本的位置。
func locateArtifact(content, keyword string, offset int) (int, bool) {
	runes := []rune(content)
	if offset > len(runes) {
		return offset, false
	}
	index := strings.Index(string(runes[offset:]), keyword)
	if index < 0 {
		return offset, false
	}
	return offset + len([]rune(string(runes[offset:])[:index])), true
}
