package errors

import (
	stderrors "errors"
	"fmt"
	"testing"
)

// errTestNotFound 是本包测试用的假哨兵，避免测试依赖任何具体 ORM / 驱动。
var errTestNotFound = stderrors.New("test: record not found")

func init() {
	RegisterNotFoundSentinel(errTestNotFound)
}

func TestIsNotFoundTraversesWrappedErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"裸哨兵", errTestNotFound, true},
		{"fmt.Errorf %w 包装一层", fmt.Errorf("查询失败: %w", errTestNotFound), true},
		{"多层包装", fmt.Errorf("外层: %w", fmt.Errorf("内层: %w", errTestNotFound)), true},
		{"业务错误包装哨兵", NewWithErr(CodeInternalError, "查询失败", errTestNotFound), true},
		{"无关错误", stderrors.New("connection refused"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := IsNotFound(tc.err); got != tc.want {
			t.Errorf("%s: IsNotFound(%v) = %v，期望 %v", tc.name, tc.err, got, tc.want)
		}
	}
}

func TestTranslatePassesThroughNil(t *testing.T) {
	if got := Translate(nil); got != nil {
		t.Errorf("Translate(nil) 应返回 nil，得到 %v", got)
	}
}

// TestTranslateMapsRawNotFoundTo404 覆盖「漏失」方向：
// repository 原样上抛的哨兵（可能还裹了几层 fmt.Errorf）必须变成 404，而不是 500。
func TestTranslateMapsRawNotFoundTo404(t *testing.T) {
	raw := fmt.Errorf("查询系统模型失败: %w", errTestNotFound)

	got := Translate(raw)
	code := bizCode(t, got)
	if code != CodeNotFound {
		t.Fatalf("Translate 后业务码 = %d，期望 %d", code, CodeNotFound)
	}
	// 原始错误必须仍在链上 —— 否则排障时看不到真因。
	if !stderrors.Is(got, errTestNotFound) {
		t.Error("翻译后原始哨兵丢失：错误链被截断，排障会失去真因")
	}
}

// TestTranslateUpgradesFallbackCodeWhenCauseIsKnown 是本次的核心规则：
// 「服务内部错误」是「原因不明」的兜底码；链里已存在确定的「记录不存在」事实时，
// 兜底码必须让位于已知事实。这条规则让「忘了判断」的新代码也不会退化成假的 500。
func TestTranslateUpgradesFallbackCodeWhenCauseIsKnown(t *testing.T) {
	wrapped := WrapDefault(CodeInternalError, fmt.Errorf("查询失败: %w", errTestNotFound))

	got := Translate(wrapped)
	if code := bizCode(t, got); code != CodeNotFound {
		t.Fatalf("兜底码 + 链中不存在的哨兵，应升级为 %d，实际 %d", CodeNotFound, code)
	}
	if !stderrors.Is(got, errTestNotFound) {
		t.Error("升级后原始哨兵丢失")
	}
}

// 对照组：兜底码 + 原因不明 ⇒ 必须保持 500，不能一律升级成 404。
func TestTranslateKeepsFallbackCodeForUnknownCause(t *testing.T) {
	wrapped := WrapDefault(CodeInternalError, stderrors.New("connection refused"))

	got := Translate(wrapped)
	if code := bizCode(t, got); code != CodeInternalError {
		t.Fatalf("原因不明时应保持 %d，实际 %d（把真故障说成 404 会让监控盲掉）",
			CodeInternalError, code)
	}
}

// 具体业务码不是兜底码，必须原样保留（不能被「链里恰好有 not-found」篡改）。
func TestTranslateKeepsSpecificBusinessCode(t *testing.T) {
	original := WrapDefault(CodeSessionNotFound, errTestNotFound)

	got := Translate(original)
	if got != original {
		t.Fatalf("具体业务码应原样返回，实际被改成了 %v", got)
	}
}

func TestTranslateLeavesUnknownErrorUntouched(t *testing.T) {
	raw := stderrors.New("connection refused")
	if got := Translate(raw); got != raw {
		t.Errorf("无关错误应原样返回，实际 %v", got)
	}
}

// TestNotFoundOrInternalSplitsBothDirections 钉住 service 层唯一推荐的写法：
// 不存在 → 具体业务码；查询失败 → 500。两个方向都要有断言，
// 只测一个方向的话，「一律当不存在」的实现也能通过。
func TestNotFoundOrInternalSplitsBothDirections(t *testing.T) {
	if code := bizCode(t, NotFoundOrInternal(CodeSessionNotFound, errTestNotFound)); code != CodeSessionNotFound {
		t.Errorf("不存在时应给 %d，实际 %d", CodeSessionNotFound, code)
	}
	if code := bizCode(t, NotFoundOrInternal(CodeSessionNotFound, stderrors.New("timeout"))); code != CodeInternalError {
		t.Errorf("查询失败时应给 %d，实际 %d", CodeInternalError, code)
	}
	// 包装后的哨兵同样要识别
	wrapped := fmt.Errorf("repo: %w", errTestNotFound)
	if code := bizCode(t, NotFoundOrInternal(CodeDocumentNotFound, wrapped)); code != CodeDocumentNotFound {
		t.Errorf("包装后的哨兵应被识别为不存在，实际码 %d", code)
	}
}

func bizCode(t *testing.T, err error) int {
	t.Helper()
	var bizErr *BizError
	if !stderrors.As(err, &bizErr) {
		t.Fatalf("期望 *BizError，实际 %T: %v", err, err)
	}
	return bizErr.Code
}
