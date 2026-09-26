package agent

import (
	"testing"
)

// TestSortedInternalToolsTotallyOrdered 断言「同 Order 的内置工具按 Name 裁决」。
//
// 为什么这条能真的变红（不是恒真）：比较函数只比 Order 时，两个 Order 相等的工具
// 谁在前完全由入参顺序决定 —— 这里故意把 beta 放在 alpha 前面，
// 若兜底被去掉，输出就是 [gamma, beta, alpha]，断言立刻失败。
// （n 小时 Go 用插入排序，比较函数恒 false ⇒ 不做任何交换 ⇒ 保留入参顺序，因此可复现。）
func TestSortedInternalToolsTotallyOrdered(t *testing.T) {
	e := &Engine{internalTools: []internalToolRegistryEntry{
		{Name: "beta", Order: 1},
		{Name: "alpha", Order: 1},
		{Name: "gamma", Order: 0},
	}}

	got := e.sortedInternalTools()
	want := []string{"gamma", "alpha", "beta"}
	if len(got) != len(want) {
		t.Fatalf("工具数=%d 期望 %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Fatalf("第 %d 位=%s 期望 %s（全序排序结果 %v）", i, got[i].Name, want[i], names(got))
		}
	}
}

// TestSortedInternalToolsPrefersOrderOverName 是上一条的**对照组**：
// Order 不同时必须按 Order 排，而不是按 Name。
//
// 没有它，上一条断言可能退化成一个更弱的命题（「结果按 Name 排序」）而照样全绿 ——
// 这里让 Name 更小的 `aardvark` 排在后面，若实现改成按 Name 排，本测试立刻变红。
func TestSortedInternalToolsPrefersOrderOverName(t *testing.T) {
	e := &Engine{internalTools: []internalToolRegistryEntry{
		{Name: "zzz-first", Order: 0},
		{Name: "aardvark-second", Order: 1},
	}}

	got := e.sortedInternalTools()
	if got[0].Name != "zzz-first" {
		t.Fatalf("Order 不同时应按 Order 排，首位应为 zzz-first，实际 %s（是不是变成按 Name 排了？）", got[0].Name)
	}
}

func names(entries []internalToolRegistryEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}
