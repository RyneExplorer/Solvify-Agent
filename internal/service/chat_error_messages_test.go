package service

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"solvify-agent/internal/agent"
	apperrors "solvify-agent/pkg/errors"
)

// ─── 错误文案翻译 messageFor 的守卫 ────────────────────────────────────────
//
// 这一层是面向用户文案的**唯一出口**。此前 agent 层另有 6 处内联文案，
// 同一个失败有两个来源、且都没有「模式」维度 ⇒ 快速模式的失败显示了深度模式的措辞。

// deepClaims 是「把失败归因于深度模式」的措辞。
// 允许「切换到深度模式」这类建议，但不允许把原因说成深度 —— 后者正是用户踩到的那条：
// 快速检索失败，前端却显示「深度思考模式执行异常，请重试或使用快速模式」。
var deepClaims = []string{"深度思考模式", "深度推理失败", "深度模式启动失败"}

// quickClaims 是上一条的反向版本。
var quickClaims = []string{"快速检索链路异常", "快速模式启动失败", "快速检索轮次用尽"}

func assertNoClaim(t *testing.T, kind apperrors.Kind, profile agent.Profile, claims []string) {
	t.Helper()
	msg := messageFor(kind, profile)
	text := msg.Title + " " + msg.Detail
	for _, claim := range claims {
		if strings.Contains(text, claim) {
			t.Errorf("种类 %q 在 %s 模式下的文案归因于另一个模式: %s / %s", kind, profile, msg.Title, msg.Detail)
		}
	}
}

// 快速模式的失败绝不把原因说成深度模式。覆盖全部种类 + 兜底。
func TestMessageFor_QuickModeNeverBlamesDeepMode(t *testing.T) {
	for kind := range errorMessages {
		assertNoClaim(t, kind, agent.ProfileQuick, deepClaims)
	}
	assertNoClaim(t, apperrors.KindUnknown, agent.ProfileQuick, deepClaims)
}

// 反向：深度模式的失败也不许把原因说成快速检索。
func TestMessageFor_DeepModeNeverBlamesQuickMode(t *testing.T) {
	for kind := range errorMessages {
		assertNoClaim(t, kind, agent.ProfileDeep, quickClaims)
	}
	assertNoClaim(t, apperrors.KindUnknown, agent.ProfileDeep, quickClaims)
}

// 写了 Quick/Deep 覆盖的种类，两种模式的文案必须真的不同 —— 防止覆盖写错字段却没生效。
func TestMessageFor_ModeOverrideTakesEffect(t *testing.T) {
	cases := []apperrors.Kind{
		apperrors.KindMaxIterations,
		apperrors.KindAgentRunFailed,
		apperrors.KindAgentInitFailed,
	}
	for _, kind := range cases {
		quick := messageFor(kind, agent.ProfileQuick)
		deep := messageFor(kind, agent.ProfileDeep)
		if quick == deep {
			t.Errorf("种类 %q 的快速/深度文案相同 ⇒ Quick/Deep 覆盖没接上: %+v", kind, quick)
		}
	}
}

// 每种模式都必须能拿到非空文案，包括既没有 Quick 也没有 Deep 覆盖的种类。
func TestMessageFor_EveryModeGetsCopy(t *testing.T) {
	for kind := range errorMessages {
		for _, profile := range []agent.Profile{agent.ProfileQuick, agent.ProfileDeep} {
			msg := messageFor(kind, profile)
			if msg.Title == "" || msg.Detail == "" {
				t.Errorf("种类 %q 在 %s 模式下文案为空: %+v", kind, profile, msg)
			}
		}
	}
}

// 迭代超限必须说出真实原因（轮次），而不是此前的通用措辞。
func TestMessageFor_MaxIterationsIsActionable(t *testing.T) {
	for _, profile := range []agent.Profile{agent.ProfileQuick, agent.ProfileDeep} {
		msg := messageFor(apperrors.KindMaxIterations, profile)
		if !strings.Contains(msg.Title+msg.Detail, "轮次") {
			t.Errorf("%s 模式的迭代超限文案没说出真实原因（轮次）: %+v", profile, msg)
		}
		if !msg.Retryable {
			t.Errorf("%s 模式的迭代超限应可重试: %+v", profile, msg)
		}
	}
}

// 端到端：Agent 只上报种类，SSE 事件的文案由 service 按模式补齐。
// 这是本次缺陷的直接回归 —— 快速请求跑到 runner 的错误分支，前端曾看到深度措辞。
func TestToStreamEvent_ErrorEventGetsModeAwareCopy(t *testing.T) {
	ev := agent.Event{Type: agent.EventError, Kind: apperrors.KindMaxIterations, Error: "exceeds max iterations", Done: true}

	quick := toStreamEvent(ev, agent.ProfileQuick)
	deep := toStreamEvent(ev, agent.ProfileDeep)

	want := messageFor(apperrors.KindMaxIterations, agent.ProfileQuick)
	if quick.Title != want.Title || quick.Detail != want.Detail {
		t.Errorf("快速模式的错误事件文案未按模式补齐: got %s/%s, want %s/%s", quick.Title, quick.Detail, want.Title, want.Detail)
	}
	if quick.Title == deep.Title {
		t.Errorf("两种模式的错误文案相同 ⇒ 模式维度没生效: %q", quick.Title)
	}
	// 原始英文错误仍要透传，便于排障；但它不能变成用户读的说明。
	if quick.Error != "exceeds max iterations" {
		t.Errorf("原始错误应原样透传给前端排障，实际 %q", quick.Error)
	}
}

// 每个已声明的种类都必须有文案：漏一条就会让用户在某个分支上看到空白提示。
//
// 种类清单从源码扫出来，不手写第二份 —— 手写清单会随新增种类而漂移。
func TestErrorMessages_CoversEveryDeclaredKind(t *testing.T) {
	for _, kind := range declaredKinds(t) {
		// KindUnknown 按定义就是「判定不出失败原因」，它的文案是 fallbackMessage，
		// 不在这张表里 —— 这是设计，不是漏项。
		if kind == apperrors.KindUnknown {
			continue
		}
		entry, ok := errorMessages[kind]
		if !ok {
			t.Errorf("种类 %q 已声明但没有文案", kind)
			continue
		}
		if entry.Title == "" || entry.Detail == "" {
			t.Errorf("种类 %q 的文案不完整: %+v", kind, entry.ErrorMessage)
		}
	}
}

// declaredKinds 扫 pkg/errors/kind.go 源码，取出所有 `KindXxx Kind = "..."` 的取值。
func declaredKinds(t *testing.T) []apperrors.Kind {
	t.Helper()
	path := filepath.Join("..", "..", "pkg", "errors", "kind.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	re := regexp.MustCompile(`(?m)^\s*(Kind\w+)\s+Kind\s*=\s*"([^"]+)"`)
	matches := re.FindAllStringSubmatch(string(raw), -1)
	if len(matches) == 0 {
		t.Fatalf("在 %s 里没扫到任何种类声明 —— 正则或文件结构变了", path)
	}
	kinds := make([]apperrors.Kind, 0, len(matches))
	for _, m := range matches {
		kinds = append(kinds, apperrors.Kind(m[2]))
	}
	return kinds
}

// 认不出的种类回落到通用文案，且标记为可重试。
func TestMessageFor_UnknownFallsBack(t *testing.T) {
	got := messageFor(apperrors.Kind("不存在的种类"), agent.ProfileQuick)
	if got.Title != "操作失败" || !got.Retryable {
		t.Errorf("未知种类应回落到可重试的通用文案，实际 %+v", got)
	}
}

// 底层错误详情必须压过调用方自己的兜底话术：判定走 Classify，
// 结构化的 503 不该因为外面包了一层「LLM 调用失败」被降级成通用文案。
func TestGetFriendlyError_UnderlyingErrorWinsOverGenericWrapper(t *testing.T) {
	got := getFriendlyError(errors.New("LLM 调用失败: 503 Service Unavailable"), agent.ProfileDeep)
	if want := errorMessages[apperrors.KindModelUnavailable].ErrorMessage; got != want {
		t.Errorf("应返回上游 503 的文案（可重试的「AI 服务暂时不可用」），实际 %+v", got)
	}
	if !got.Retryable {
		t.Errorf("503 应标记为可重试，实际 Retryable=false")
	}
}

// 判定必须确定：同一输入重复调用结果完全一致。
func TestGetFriendlyError_Deterministic(t *testing.T) {
	cases := []struct {
		err     error
		profile agent.Profile
	}{
		{errors.New("LLM 调用失败: 429 Too Many Requests"), agent.ProfileQuick},
		{errors.New("context deadline exceeded"), agent.ProfileDeep},
		{errors.New("联网搜索超时"), agent.ProfileQuick},
	}
	for _, c := range cases {
		first := getFriendlyError(c.err, c.profile)
		for i := 0; i < 200; i++ {
			if got := getFriendlyError(c.err, c.profile); got != first {
				t.Fatalf("第 %d 次调用结果与首次不一致（命中规则随机）: 首次=%+v，本次=%+v", i+1, first, got)
			}
		}
	}
}
