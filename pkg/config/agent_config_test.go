package config

import "testing"

func TestModelRetryConfiguration(t *testing.T) {
	if Default().Agent.ModelMaxRetries != 3 {
		t.Fatal("默认应允许三次模型重试")
	}
	for _, value := range []string{"0", "2", "5"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("AGENT_MODEL_MAX_RETRIES", value)
			cfg := Default()
			applyEnv(cfg)
			if cfg.Agent.ModelMaxRetries != int(value[0]-'0') {
				t.Fatal("环境变量未覆盖模型重试次数")
			}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, value := range []int{-1, 6} {
		cfg := Default()
		cfg.Agent.ModelMaxRetries = value
		if err := cfg.Validate(); err == nil {
			t.Fatalf("非法重试次数 %d 被接受", value)
		}
	}
}

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
