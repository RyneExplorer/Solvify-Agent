package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	ArtifactID string `json:"artifact_id"`
	Hash       string `json:"hash"`
	Version    int    `json:"version"`
	Runes      int    `json:"runes"`
	Preview    string `json:"preview"`
	Read       string `json:"read"`
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
			raw, err := json.Marshal(artifactReference{ArtifactID: id, Hash: hash, Version: 1, Runes: len([]rune(out.Result)), Preview: preview, Read: "原文已保存；预览不代表全部内容。使用 read_context_artifact 按 offset/limit 分段读取。"})
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
	var result struct {
		Success *bool `json:"success"`
	}
	return json.Unmarshal([]byte(content), &result) == nil && result.Success != nil && !*result.Success
}

type artifactReadTool struct {
	repo   repository.ContextArtifactRepo
	scope  repository.ArtifactScope
	policy config.RuntimeContextPolicy
	model  string
}

func (t artifactReadTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: artifactReadToolName, Desc: "读取本运行本任务工具产物的一段原文；偏移/长度以字符计数，返回next_offset继续。只能读取工具结果给出的artifact_id。", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"artifact_id": {Type: schema.String, Required: true}, "offset": {Type: schema.Integer}, "limit": {Type: schema.Integer}})}, nil
}
func (t artifactReadTool) InvokableRun(ctx context.Context, args string, _ ...einoTool.Option) (string, error) {
	var req struct {
		ID     string `json:"artifact_id"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if json.Unmarshal([]byte(args), &req) != nil || len(req.ID) != 64 || req.Offset < 0 || req.Limit < 0 {
		return "", fmt.Errorf("产物读取参数无效")
	}
	if req.Limit == 0 {
		req.Limit = t.policy.ReadRunes
	}
	if req.Limit > t.policy.ReadRunes {
		return "", fmt.Errorf("产物读取范围过大")
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
		return "", fmt.Errorf("产物偏移超出范围")
	}
	end := req.Offset + min(len(runes)-req.Offset, req.Limit)
	piece := string(runes[req.Offset:end])
	pieceBudget := t.policy.ReadTokens
	for range 8 {
		piece, _ = tokenutil.TruncateByTokens(piece, t.model, pieceBudget)
		next := req.Offset + len([]rune(piece))
		raw, err := json.Marshal(map[string]any{"artifact_id": a.ID, "hash": a.Hash, "version": a.Version, "offset": req.Offset, "next_offset": next, "total_runes": len(runes), "content": piece, "complete": next == len(runes)})
		if err != nil {
			return "", err
		}
		cost := tokenutil.CountTokens(string(raw), t.model)
		if cost <= t.policy.ReadTokens && (next > req.Offset || next == len(runes)) {
			return string(raw), nil
		}
		pieceBudget -= max(1, cost-t.policy.ReadTokens)
		if pieceBudget <= 0 {
			break
		}
	}
	return "", fmt.Errorf("产物读取额度不足，无法返回有效片段")
}

func (e *Engine) addArtifactTool(req Request, tools []einoTool.BaseTool) []einoTool.BaseTool {
	if e.artifactRepo == nil || req.AgentName == "synthesis_review" {
		return tools
	}
	return append(append([]einoTool.BaseTool(nil), tools...), artifactReadTool{e.artifactRepo, artifactScope(req), e.runtimePolicy(), req.ModelName})
}
