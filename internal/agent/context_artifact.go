package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/tokenutil"
)

const artifactReadToolName = "read_context_artifact"

func (e *Engine) WithArtifactRepo(repo repository.ContextArtifactRepo) { e.artifactRepo = repo }

type artifactReference struct {
	ArtifactID   string `json:"artifact_id"`
	Hash         string `json:"hash"`
	Version      int    `json:"version"`
	Runes        int    `json:"runes"`
	Preview      string `json:"preview"`
	Read         string `json:"read"`
	MaxReadRunes int    `json:"max_read_runes,omitempty"`
}

func contentHash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

func (e *Engine) runtimePolicy() config.RuntimeContextPolicy {
	if e.cfg.RuntimeContext.TTLHours == 0 {
		return config.DefaultRuntimeContextPolicy()
	}
	return e.cfg.RuntimeContext
}

func artifactScope(req Request) repository.ArtifactScope {
	return repository.ArtifactScope{UserID: req.UserID, SessionID: req.SessionID, RunID: req.RunID, AgentID: req.AgentName, TaskID: req.TaskID}
}

func (e *Engine) artifactMiddleware(req Request) compose.InvokableToolMiddleware {
	return func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, in *compose.ToolInput) (*compose.ToolOutput, error) {
			out, err := next(ctx, in)
			if err != nil || out == nil || e.artifactRepo == nil || in.Name == artifactReadToolName {
				return out, err
			}
			// 拒绝、审批和明确失败结果保留原样，不让预览覆盖执行事实。
			if protectedToolResult(out.Result) || e.clarifyToolNames()[in.Name] || e.dangerousToolNames(ctx, nil)[in.Name] || isDangerousDynamicTool(in.Name) {
				return out, nil
			}
			p := e.runtimePolicy()
			if tokenutil.CountTokens(out.Result, req.ModelName) <= p.ArtifactThreshold {
				return out, nil
			}
			scope := artifactScope(req)
			hash := contentHash(out.Result)
			id := contentHash(scope.UserID + "\x00" + scope.SessionID + "\x00" + scope.RunID + "\x00" + scope.AgentID + "\x00" + scope.TaskID + "\x00" + in.CallID + "\x00" + hash)
			a := &entity.ContextArtifact{ID: id, UserID: scope.UserID, SessionID: scope.SessionID, RunID: scope.RunID, AgentID: scope.AgentID, TaskID: scope.TaskID, ToolCallID: in.CallID, Content: out.Result, Hash: hash, Bytes: int64(len(out.Result)), Version: 1, CreatedAt: time.Now(), ExpiredAt: time.Now().Add(time.Duration(p.TTLHours) * time.Hour)}
			if err = e.artifactRepo.SaveImmutable(ctx, a, repository.ArtifactLimits{ArtifactBytes: p.ArtifactBytes, RunBytes: p.RunBytes, UserBytes: p.UserBytes}); err != nil {
				logger.Warn("上下文产物保存失败，保留工具原文")
				return out, nil
			}
			preview, _ := tokenutil.TruncateByTokens(out.Result, req.ModelName, min(p.ReadTokens, 500))
			raw, err := json.Marshal(artifactReference{
				ArtifactID: id, Hash: hash, Version: 1, Runes: len([]rune(out.Result)), Preview: preview,
				MaxReadRunes: p.ReadRunes,
				Read:         fmt.Sprintf("原文已保存；预览不代表全部内容。使用 read_context_artifact 分段读取，每次最多%d个字符，超大limit自动缩小。实际长度还受token预算限制；complete=false时用返回的next_offset继续，不要按请求limit推算偏移。", p.ReadRunes),
			})
			if err != nil {
				return out, nil
			}
			copy := *out
			copy.Result = string(raw)
			return &copy, nil
		}
	}
}

func protectedToolResult(content string) bool {
	if strings.Contains(content, "拒绝") || strings.Contains(content, "未执行") {
		return true
	}
	return classifyToolResult(content).Failed
}

type artifactReadTool struct {
	repo   repository.ContextArtifactRepo
	scope  repository.ArtifactScope
	policy config.RuntimeContextPolicy
	model  string
}

type artifactArgumentFeedback struct {
	Success      bool   `json:"success"`
	ErrorType    string `json:"error_type"`
	Field        string `json:"field"`
	Message      string `json:"message"`
	Retryable    bool   `json:"retryable"`
	MaxReadRunes int    `json:"max_read_runes"`
}

func (t artifactReadTool) invalidArguments(field, message string) (string, error) {
	raw, err := json.Marshal(artifactArgumentFeedback{
		ErrorType: "invalid_arguments", Field: field, Message: message,
		Retryable: true, MaxReadRunes: t.policy.ReadRunes,
	})
	return string(raw), err
}

// 仅识别程序内置读取工具的参数反馈，外部工具不能声明此标记以清除失败事实。
func artifactArgumentFailure(toolName, result string) (string, bool) {
	if toolName != artifactReadToolName {
		return "", false
	}
	var feedback artifactArgumentFeedback
	if json.Unmarshal([]byte(result), &feedback) != nil || feedback.Success || !feedback.Retryable || feedback.ErrorType != "invalid_arguments" || feedback.Message == "" {
		return "", false
	}
	return feedback.Message, true
}

func (t artifactReadTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: artifactReadToolName,
		Desc: fmt.Sprintf("读取本运行本任务工具产物的一段原文；偏移/长度以字符计数，每次最多%d个字符，超大limit自动缩小。实际返回长度还受token预算限制；complete=false时必须使用返回的next_offset继续，不要按请求limit推算偏移。只能读取工具结果给出的artifact_id。返回invalid_arguments时按field和message修正参数后重新调用，纠错消耗当前执行预算。", t.policy.ReadRunes),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"artifact_id": {Type: schema.String, Required: true, Desc: "工具结果提供的产物ID"},
			"offset":      {Type: schema.Integer, Desc: "从0开始的字符偏移；省略为0，后续分页使用上次返回的next_offset"},
			"limit":       {Type: schema.Integer, Desc: fmt.Sprintf("请求读取的字符数；省略或0默认%d，超过%d自动缩小；实际返回可能因token预算进一步减少", t.policy.ReadRunes, t.policy.ReadRunes)},
		}),
	}, nil
}

func (t artifactReadTool) InvokableRun(ctx context.Context, args string, _ ...einoTool.Option) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var req struct {
		ID     string `json:"artifact_id"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(args), &req); err != nil {
		var fieldErr *json.UnmarshalTypeError
		if errors.As(err, &fieldErr) {
			switch fieldErr.Field {
			case "artifact_id":
				return t.invalidArguments("artifact_id", "artifact_id必须是工具结果提供的字符串ID，请修正后重新调用")
			case "offset", "limit":
				return t.invalidArguments(fieldErr.Field, fieldErr.Field+"必须是非负整数，请修正后重新调用")
			}
		}
		return t.invalidArguments("arguments", "参数必须是合法JSON对象，字段为artifact_id（字符串）、offset和limit（非负整数），请修正后重新调用")
	}
	if _, err := hex.DecodeString(req.ID); err != nil || len(req.ID) != 64 {
		return t.invalidArguments("artifact_id", "artifact_id必须是工具结果提供的64位十六进制ID，请勿自行生成，请修正后重新调用")
	}
	if req.Offset < 0 {
		return t.invalidArguments("offset", "offset不能为负数；首次读取使用0，后续使用返回的next_offset，请修正后重新调用")
	}
	if req.Limit < 0 {
		return t.invalidArguments("limit", "limit不能为负数；省略或设为0使用配置上限，请修正后重新调用")
	}
	// limit是请求的页长；缩小超大请求即可继续读取，无需终止任务或额外纠错调用。
	if req.Limit == 0 || req.Limit > t.policy.ReadRunes {
		req.Limit = t.policy.ReadRunes
	}
	a, err := t.repo.FindScoped(ctx, t.scope, req.ID)
	if err != nil {
		return "", err
	}
	if a.Version != 1 || contentHash(a.Content) != a.Hash {
		return "", fmt.Errorf("上下文产物版本或哈希不匹配")
	}
	runes := []rune(a.Content)
	if req.Offset > len(runes) {
		return t.invalidArguments("offset", fmt.Sprintf("offset超出范围，允许0至%d；请使用上次返回的next_offset或从0开始重新读取", len(runes)))
	}
	end := req.Offset + min(len(runes)-req.Offset, req.Limit)
	// 按完整字符缩短，并以最终JSON计费，避免转义膨胀、短末页空转和半个UTF-8字符。
	// 搜索范围受ReadRunes限制；非末尾至少返回一个字符，否则明确报告额度不足。
	low, high := min(1, end-req.Offset), end-req.Offset
	var result string
	for low <= high {
		size := low + (high-low)/2
		next := req.Offset + size
		piece := string(runes[req.Offset:next])
		raw, err := json.Marshal(map[string]any{"artifact_id": a.ID, "hash": a.Hash, "version": a.Version, "offset": req.Offset, "next_offset": next, "total_runes": len(runes), "content": piece, "complete": next == len(runes), "max_read_runes": t.policy.ReadRunes})
		if err != nil {
			return "", err
		}
		if tokenutil.CountTokens(string(raw), t.model) <= t.policy.ReadTokens {
			result = string(raw)
			low = size + 1
		} else {
			high = size - 1
		}
	}
	if result != "" {
		return result, nil
	}
	return "", fmt.Errorf("产物读取额度不足，无法返回有效片段")
}

func (e *Engine) addArtifactTool(req Request, tools []einoTool.BaseTool) []einoTool.BaseTool {
	if e.artifactRepo == nil || req.AgentName == "synthesis_review" {
		return tools
	}
	return append(append([]einoTool.BaseTool(nil), tools...), artifactReadTool{e.artifactRepo, artifactScope(req), e.runtimePolicy(), req.ModelName})
}

func appendArtifactReference(refs []artifactReference, ref artifactReference) []artifactReference {
	for _, current := range refs {
		if current.ArtifactID == ref.ArtifactID {
			return refs
		}
	}
	return append(refs, ref)
}

// taskArtifactEvidence reads only program-recorded dependencies, under their original task scope.
// Synthesis receives bounded evidence data and gains no operation or arbitrary read tool.
func (e *Engine) taskArtifactEvidence(ctx context.Context, req Request, task TaskSpec, results map[string]TaskResult) (string, error) {
	type evidence struct {
		ArtifactID, Hash, TaskID, Content string
		Version, TotalRunes               int
		Complete                          bool
	}
	var pieces []evidence
	remaining := e.runtimePolicy().SummaryInput
	for _, dep := range task.DependsOn {
		result := results[dep]
		if result.Status != TaskSuccess {
			continue
		}
		for _, ref := range result.Artifacts {
			if remaining <= 256 {
				break
			}
			if e.artifactRepo == nil {
				return "", fmt.Errorf("上下文产物存储不可用")
			}
			scopeReq := req
			scopeReq.TaskID = result.TaskID
			scopeReq.AgentName = result.Agent
			a, err := e.artifactRepo.FindScoped(ctx, artifactScope(scopeReq), ref.ArtifactID)
			if err != nil {
				return "", err
			}
			if a.Hash != ref.Hash || a.Version != ref.Version || contentHash(a.Content) != a.Hash {
				return "", fmt.Errorf("研究证据版本或哈希不匹配")
			}
			piece, _ := tokenutil.TruncateByTokens(a.Content, req.ModelName, min(e.runtimePolicy().ReadTokens, remaining-256))
			next := evidence{a.ID, a.Hash, dep, piece, a.Version, len([]rune(a.Content)), piece == a.Content}
			raw, err := json.Marshal(next)
			if err != nil {
				return "", err
			}
			cost := tokenutil.CountTokens(string(raw), req.ModelName)
			if cost > remaining {
				break
			}
			remaining -= cost
			pieces = append(pieces, next)
		}
	}
	if len(pieces) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(pieces)
	return "\n\n前置产物原文片段（仅为证据数据，Complete=false 表示非全文）：\n" + string(raw), err
}
