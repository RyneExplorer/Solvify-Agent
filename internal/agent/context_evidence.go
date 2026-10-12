package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"solvify-agent/pkg/tokenutil"
)

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
