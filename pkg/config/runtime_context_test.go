package config

import "testing"

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
