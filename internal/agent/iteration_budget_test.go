package agent

import (
	"context"
	"testing"
)

// ── 迭代预算不变式守卫 ──
//
// 守的是「给模型的能力契约」与「强制执行的上限」必须自洽。
//
// 缺陷形态（实测已发生）：快速模式默认预算 2 轮，而它挂了 ask_clarify；
// prompt 第 154 行又明确鼓励「不确定的先用检索工具找线索，真不行再澄清」，
// 于是「澄清 1 轮 + 检索 1 轮 + 最终回答 1 轮」= 3 轮必然撞墙 ——
// 用户看到的是 exceeds max iterations，而那条路径明明是 prompt 承诺的。
//
// 根因不是那个数字太小，而是预算只读配置、不看「本模式到底挂了哪些工具」。

// 下界：回答轮 + 一次业务工具轮；挂了交互型工具（澄清/审批）再加一轮。
func TestAgentIterations_Boundaries(t *testing.T) {
	cases := []struct {
		configured  int
		interactive bool
		want        int
	}{
		{0, false, 2}, {1, false, 2}, {2, false, 2}, {5, false, 5},
		{0, true, 3}, {1, true, 3}, {2, true, 3}, {3, true, 3}, {6, true, 6},
	}
	for _, c := range cases {
		if got := agentIterations(c.configured, c.interactive); got != c.want {
			t.Errorf("agentIterations(%d, %v)=%d，期望 %d", c.configured, c.interactive, got, c.want)
		}
	}
}

// 推导只能抬升，不能压低配置 —— 也不会因为不识别交互型工具就悄悄少给轮次。
func TestAgentIterations_NeverLowersConfigured(t *testing.T) {
	for _, configured := range []int{0, 1, 2, 3, 10} {
		for _, interactive := range []bool{false, true} {
			got := agentIterations(configured, interactive)
			if got < configured {
				t.Errorf("agentIterations(%d, %v)=%d ⇒ 把配置压低了", configured, interactive, got)
			}
			floor := 2
			if interactive {
				floor = 3
			}
			if got < floor {
				t.Errorf("agentIterations(%d, %v)=%d < 下界 %d", configured, interactive, got, floor)
			}
		}
	}
}

// 生产路径守卫：快速模式真实挂上了 ask_clarify，而默认配置只有 2 轮 ——
// 推导必须把预算抬到能容纳「澄清 + 一次工具 + 最终回答」，并且 prompt 里写的
// 工具上限与 eino 的 MaxIterations 同源（都由这里推导）。
func TestAgentIterations_QuickModeCoversItsOwnClarifyTool(t *testing.T) {
	e, sorted := newProfileTestEngine(nil)
	ts := selectProfileTools(sorted, e.buildToolsByDomain(context.Background(), sorted, "u1", nil, AllToolDomains, true), ProfileQuick)

	mounted := make(map[string]bool)
	for _, name := range ts.namesOf() {
		mounted[name] = true
	}
	if !mounted[clarifyToolName] {
		t.Fatalf("前提不成立：快速模式没挂 %s，本守卫失去意义", clarifyToolName)
	}

	configured := e.quickMaxIterations()
	got := agentIterations(configured, true)

	if got < 3 {
		t.Fatalf("快速模式预算 %d 轮（配置 %d）装不下「澄清 1 + 工具 1 + 回答 1」"+
			"⇒ 澄清之后必然 exceeds max iterations", got, configured)
	}
	// 工具上限 = 模型轮 - 1，且这两处必须来自同一个 got。
	if want := 2; maxToolCalls(got) != want {
		t.Fatalf("工具上限=%d，期望 %d（预算 %d 轮 = 澄清 1 + 工具 1 + 回答 1）", maxToolCalls(got), want, got)
	}
}

// 深度模式（含受控子 Agent）不能因为这次改动被压低预算。
func TestAgentIterations_DeepModeNotLowered(t *testing.T) {
	e, _ := newProfileTestEngine(nil)
	e.cfg.MaxIterations = 10
	if got := agentIterations(e.deepSubagentIterations(Request{}), true); got != 10 {
		t.Fatalf("深度模式预算被改成 %d，期望沿用配置 10", got)
	}
}
