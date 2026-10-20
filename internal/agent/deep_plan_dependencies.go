package agent

// 汇总覆盖范围是程序不变量。重规划遗漏有效研究依赖时，仅为未执行的汇总
// 补齐边，不修改研究任务、已完成结果、审批事实或既有终态任务。
func completeSynthesisDependencies(plan *ExecutionPlan, synthesisID string) {
	index := -1
	byID := make(map[string]TaskSpec, len(plan.Tasks))
	for i, task := range plan.Tasks {
		byID[task.ID] = task
		if task.ID == synthesisID {
			index = i
		}
	}
	if index < 0 || plan.Tasks[index].Status != TaskPending {
		return // 已结束的汇总不可改写，后续覆盖校验会拒绝不完整计划。
	}
	covered := make(map[string]bool)
	var visit func(string)
	visit = func(id string) {
		for _, dep := range byID[id].DependsOn {
			if !covered[dep] {
				covered[dep] = true
				visit(dep)
			}
		}
	}
	visit(synthesisID)
	for _, task := range plan.Tasks {
		if task.Agent == "synthesis_review" || task.Status == TaskError || task.Status == TaskCanceled || covered[task.ID] {
			continue
		}
		plan.Tasks[index].DependsOn = append(plan.Tasks[index].DependsOn, task.ID)
	}
}
