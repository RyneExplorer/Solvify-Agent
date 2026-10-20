package agent

import (
	"context"
	"fmt"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
)

// 授权来自程序保存的成功依赖结果，不接受工具参数中的任务、用户或角色声明。
// 用户/会话/运行始终沿用当前请求，只有原任务归属可以按白名单切换。
type artifactReadGrant struct {
	TaskID, AgentID, Hash string
	Version               int
}

// 工具读取、上下文展开和checkpoint验证必须使用同一授权规则。
func findArtifactWithGrants(ctx context.Context, repo repository.ContextArtifactRepo, scope repository.ArtifactScope, grants map[string]artifactReadGrant, id string) (*entity.ContextArtifact, error) {
	grant, delegated := grants[id]
	if delegated {
		scope.AgentID, scope.TaskID = grant.AgentID, grant.TaskID
	}
	a, err := repo.FindScoped(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	if delegated && (grant.Hash != a.Hash || grant.Version != a.Version) {
		return nil, fmt.Errorf("前置产物版本或哈希与执行记录不一致")
	}
	return a, nil
}

func artifactOwner(ref artifactReference, result TaskResult) (string, string) {
	if ref.OwnerTaskID != "" {
		return ref.OwnerTaskID, ref.OwnerAgentID
	}
	// 有效旧checkpoint的引用归属仍由其原始任务记录确定。
	return result.TaskID, result.Agent
}

func dependencyArtifactGrants(plan ExecutionPlan, taskID string, results map[string]TaskResult) map[string]artifactReadGrant {
	grants := make(map[string]artifactReadGrant)
	ancestors := peResearchAncestors(plan, taskID)
	allowed := make(map[string]bool, len(ancestors))
	for _, id := range ancestors {
		allowed[id] = true
	}
	for _, dep := range ancestors {
		result, ok := results[dep]
		if !ok || result.Status != TaskSuccess {
			continue
		}
		for _, ref := range result.Artifacts {
			owner, agent := artifactOwner(ref, result)
			if !allowed[owner] || results[owner].Status != TaskSuccess || results[owner].Agent != agent || ref.ArtifactID == "" || ref.Hash == "" || ref.Version != 1 || owner == "" {
				continue
			}
			grants[ref.ArtifactID] = artifactReadGrant{TaskID: owner, AgentID: agent, Hash: ref.Hash, Version: ref.Version}
		}
	}
	return grants
}
