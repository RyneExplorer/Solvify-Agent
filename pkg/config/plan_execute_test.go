package config

import "testing"

func TestPlanExecuteDefaultsAndExample(t *testing.T) {
	defaults := Default().Agent
	if !defaults.DeepPlanExecuteEnabled || defaults.DeepMaxPlanExecuteRounds != 8 || defaults.DeepMaxExecutorIterations != 6 {
		t.Fatalf("深度模式默认配置无效: %+v", defaults)
	}
	values := loadExampleConfigValues(t)
	var example AgentConfig
	if err := decodeStrict(values["agent"], &example); err != nil {
		t.Fatal(err)
	}
	if !example.DeepPlanExecuteEnabled || example.DeepMaxPlanExecuteRounds != defaults.DeepMaxPlanExecuteRounds || example.DeepMaxExecutorIterations != defaults.DeepMaxExecutorIterations {
		t.Fatalf("示例配置与默认值不一致: %+v", example)
	}
}

func TestPlanExecuteEnvironmentOverrides(t *testing.T) {
	t.Setenv("AGENT_DEEP_PLAN_EXECUTE_ENABLED", "true")
	t.Setenv("AGENT_DEEP_MAX_PLAN_EXECUTE_ROUNDS", "12")
	t.Setenv("AGENT_DEEP_MAX_EXECUTOR_ITERATIONS", "9")
	cfg := Default()
	applyEnv(cfg)
	if !cfg.Agent.DeepPlanExecuteEnabled || cfg.Agent.DeepMaxPlanExecuteRounds != 12 || cfg.Agent.DeepMaxExecutorIterations != 9 {
		t.Fatalf("深度模式环境变量未生效: %+v", cfg.Agent)
	}
	t.Setenv("AGENT_DEEP_PLAN_EXECUTE_ENABLED", "false")
	t.Setenv("AGENT_DEEP_MAX_PLAN_EXECUTE_ROUNDS", "invalid")
	applyEnv(cfg)
	if cfg.Agent.DeepPlanExecuteEnabled || cfg.Agent.DeepMaxPlanExecuteRounds != 12 {
		t.Fatalf("禁用开关或无效数值回退错误: %+v", cfg.Agent)
	}
}

func TestPlanExecuteLimitsValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*AgentConfig)
	}{
		{"零外层轮次", func(c *AgentConfig) { c.DeepMaxPlanExecuteRounds = 0 }},
		{"负外层轮次", func(c *AgentConfig) { c.DeepMaxPlanExecuteRounds = -1 }},
		{"过大外层轮次", func(c *AgentConfig) { c.DeepMaxPlanExecuteRounds = 33 }},
		{"零执行迭代", func(c *AgentConfig) { c.DeepMaxExecutorIterations = 0 }},
		{"过大执行迭代", func(c *AgentConfig) { c.DeepMaxExecutorIterations = 33 }},
		{"零任务上限", func(c *AgentConfig) { c.DeepMaxPlanTasks = 0 }},
		{"过大任务上限", func(c *AgentConfig) { c.DeepMaxPlanTasks = 17 }},
		{"零子任务迭代", func(c *AgentConfig) { c.DeepMaxSubagentIterations = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.edit(&cfg.Agent)
			if err := cfg.Validate(); err == nil {
				t.Fatal("启用深度模式时应拒绝无效限制")
			}
			cfg.Agent.DeepPlanExecuteEnabled = false
			if err := cfg.Validate(); err != nil {
				t.Fatalf("禁用深度模式不应校验其限制: %v", err)
			}
		})
	}
	for _, limit := range []int{1, 32} {
		cfg := Default()
		cfg.Agent.DeepMaxPlanExecuteRounds, cfg.Agent.DeepMaxExecutorIterations = limit, limit
		if err := cfg.Validate(); err != nil {
			t.Fatalf("有效边界 %d 被拒绝: %v", limit, err)
		}
	}
}
