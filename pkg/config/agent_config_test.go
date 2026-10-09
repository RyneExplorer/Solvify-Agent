package config

import "testing"

func TestDeepAgentEnvironmentAndLimits(t *testing.T) {
	t.Setenv("AGENT_DEEP_PLAN_EXECUTE_ENABLED", "true")
	t.Setenv("AGENT_DEEP_PARALLEL_ENABLED", "false")
	t.Setenv("AGENT_DEEP_MAX_PLAN_TASKS", "6")
	t.Setenv("AGENT_DEEP_MAX_SUBAGENT_ITERATIONS", "4")
	cfg := Default()
	applyEnv(cfg)
	if !cfg.Agent.DeepPlanExecuteEnabled || cfg.Agent.DeepParallelEnabled || cfg.Agent.DeepMaxPlanTasks != 6 || cfg.Agent.DeepMaxSubagentIterations != 4 {
		t.Fatalf("配置覆盖未生效: %+v", cfg.Agent)
	}
	for _, tasks := range []int{0, 17} {
		cfg.Agent.DeepMaxPlanTasks = tasks
		if err := cfg.Validate(); err == nil {
			t.Fatalf("非法任务上限 %d 被接受", tasks)
		}
	}
}
