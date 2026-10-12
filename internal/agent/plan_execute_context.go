package agent

import "solvify-agent/pkg/tokenutil"

// peReplannerContext 是模型输入投影，不替换 checkpoint 中的业务状态。
type peReplannerContext struct {
	Plan      ExecutionPlan
	Results   map[string]TaskResult
	RetryUsed bool
	Denied    bool
	Blocked   bool
}

func replannerContext(s *peState) peReplannerContext {
	plan := s.Plan
	plan.Tasks = append([]TaskSpec(nil), s.Plan.Tasks...)
	for i := range plan.Tasks {
		plan.Tasks[i].Summary = ""
	}
	results := make(map[string]TaskResult, len(s.Results))
	for id, result := range s.Results {
		copy := result
		copy.Artifacts = append([]artifactReference(nil), result.Artifacts...)
		for i := range copy.Artifacts {
			copy.Artifacts[i].Preview = ""
			copy.Artifacts[i].Read = ""
		}
		// 汇总答复是 respond 的直接依据，不能静默裁掉；研究结果仅供进度决策。
		if result.Agent != "synthesis_review" && tokenutil.CountTokens(result.Summary, s.Request.ModelName) > 600 {
			copy.Summary, _ = tokenutil.TruncateByTokens(result.Summary, s.Request.ModelName, 560)
			copy.Summary += "\n（研究结果预览，完整内容仍在任务状态中）"
		}
		copy.Evidence = make([]AgentEvidence, len(result.Evidence))
		for i, evidence := range result.Evidence {
			next := evidence
			next.Summary = ""
			next.Sources = append([]SourceInfo(nil), evidence.Sources...)
			for j := range next.Sources {
				next.Sources[j].Chunks = append([]ChunkSource(nil), evidence.Sources[j].Chunks...)
				for k := range next.Sources[j].Chunks {
					next.Sources[j].Chunks[k].Content = ""
					next.Sources[j].Chunks[k].Quote = ""
				}
			}
			copy.Evidence[i] = next
		}
		copy.Interrupt = nil
		results[id] = copy
	}
	return peReplannerContext{Plan: plan, Results: results, RetryUsed: s.RetryUsed, Denied: s.Denied, Blocked: s.Blocked}
}
