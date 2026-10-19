package config

import "fmt"

type RuntimeContextPolicy struct {
	ArtifactThreshold     int   `mapstructure:"artifact_threshold"`
	ArtifactBytes         int64 `mapstructure:"artifact_bytes"`
	RunBytes              int64 `mapstructure:"run_bytes"`
	UserBytes             int64 `mapstructure:"user_bytes"`
	TTLHours              int   `mapstructure:"ttl_hours"`
	ReadRunes             int   `mapstructure:"read_runes"`
	ReadTokens            int   `mapstructure:"read_tokens"`
	SummaryInput          int   `mapstructure:"summary_input"`
	SummaryOutput         int   `mapstructure:"summary_output"`
	SummaryTimeoutSeconds int   `mapstructure:"summary_timeout_seconds"`
	SummaryMaxCalls       int   `mapstructure:"summary_max_calls"`
}

func DefaultRuntimeContextPolicy() RuntimeContextPolicy {
	return RuntimeContextPolicy{ArtifactThreshold: 1500, ArtifactBytes: 2 << 20, RunBytes: 20 << 20, UserBytes: 100 << 20, TTLHours: 48, ReadRunes: 4000, ReadTokens: 1200, SummaryInput: 6000, SummaryOutput: 800, SummaryTimeoutSeconds: 15, SummaryMaxCalls: 2}
}
func (p RuntimeContextPolicy) Validate() error {
	if p.ArtifactThreshold <= 0 || p.ArtifactBytes <= 0 || p.RunBytes < p.ArtifactBytes || p.UserBytes < p.RunBytes || p.TTLHours < 48 || p.TTLHours > 8760 || p.ReadRunes <= 0 || p.ReadRunes > 1000000 || p.ReadTokens <= 0 || p.SummaryInput <= p.SummaryOutput || p.SummaryOutput <= 0 || p.SummaryTimeoutSeconds <= 0 || p.SummaryTimeoutSeconds > 300 || p.SummaryMaxCalls <= 0 || p.SummaryMaxCalls > 10 {
		return fmt.Errorf("运行上下文策略无效：额度、摘要限额和有效期不合法")
	}
	return nil
}
