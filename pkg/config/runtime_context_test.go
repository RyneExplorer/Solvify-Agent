package config

import (
	"math"
	"testing"
)

func TestRuntimeContextPolicyLimits(t *testing.T) {
	if err := DefaultRuntimeContextPolicy().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*RuntimeContextPolicy){func(p *RuntimeContextPolicy) { p.TTLHours = 24 }, func(p *RuntimeContextPolicy) { p.ReadRunes = -1 }, func(p *RuntimeContextPolicy) { p.RunBytes = p.ArtifactBytes - 1 }, func(p *RuntimeContextPolicy) { p.UserBytes = p.RunBytes - 1 }, func(p *RuntimeContextPolicy) { p.SummaryInput = p.SummaryOutput }, func(p *RuntimeContextPolicy) { p.SummaryTimeoutSeconds = 301 }, func(p *RuntimeContextPolicy) { p.SummaryMaxCalls = 0 }} {
		p := DefaultRuntimeContextPolicy()
		change(&p)
		if p.Validate() == nil {
			t.Fatal("非法策略被接受")
		}
	}
}

func TestRuntimeToolContextRatio(t *testing.T) {
	p := DefaultRuntimeContextPolicy()
	if p.ToolContextShare() != 0.5 {
		t.Fatal("旧配置缺失比例时未采用默认值")
	}
	for _, ratio := range []float64{-0.1, 1.01, math.NaN(), math.Inf(1)} {
		p.ToolContextRatio = ratio
		if p.Validate() == nil {
			t.Fatalf("非法比例被接受: %v", ratio)
		}
	}
	for _, ratio := range []float64{0, 0.25, 1} {
		p.ToolContextRatio = ratio
		if err := p.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}
