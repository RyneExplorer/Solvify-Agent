package config

import "testing"

func TestContextPolicyValidation(t *testing.T) {
	p := DefaultContextPolicy()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.QuickTargetRatio = p.QuickSoftRatio
	if p.Validate() == nil {
		t.Fatal("水位无效未拒绝")
	}
	p = DefaultContextPolicy()
	p.OutputLimit = 0
	if p.Validate() == nil {
		t.Fatal("输出无效未拒绝")
	}
}

func TestExampleContextPolicyDecodes(t *testing.T) {
	values := loadExampleConfigValues(t)
	var cfg Config
	if err := decodeStrict(values, &cfg); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Agent.Context.Validate(); err != nil {
		t.Fatal("示例配置未正确解码", err)
	}
	if cfg.Agent.Context.OutputLimit != DefaultContextPolicy().OutputLimit {
		t.Fatal("示例输出默认值不一致")
	}
}
