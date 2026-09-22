package service

import (
	"errors"
	"testing"
)

// ─── 错误文案匹配 getFriendlyError 的回归测试 ───────────────────────────────
//
// 旧实现 for range map 找第一个命中的 key，而 Go 的 map 遍历顺序是随机的：
// 同一个错误在多次请求里会随机命中不同规则，用户看到的提示时好时坏。
// 下面先用「同一输入重复调用结果必须一致」钉住确定性，再逐条锁定命中优先级。

// 同一输入反复调用必须返回完全相同的文案（旧实现在多规则命中时每次都可能不同）。
func TestGetFriendlyError_AmbiguousInputIsDeterministic(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		rawError string
		want     ErrorMessage
	}{
		{
			// 三段都命中 429 / Too Many Requests / LLM 调用失败，文案分属两类
			name:     "限流被包装成通用 LLM 调用失败",
			err:      errors.New("LLM 调用失败: 429 Too Many Requests"),
			rawError: "快速检索执行失败",
			want:     errorMessages["Too Many Requests"],
		},
		{
			name:     "超时被包装成历史加载失败",
			err:      errors.New("context deadline exceeded"),
			rawError: "加载历史对话失败",
			want:     errorMessages["context deadline exceeded"],
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			first := getFriendlyError(c.err, c.rawError)
			for i := 0; i < 200; i++ {
				got := getFriendlyError(c.err, c.rawError)
				if got != first {
					t.Fatalf("第 %d 次调用结果与首次不一致（命中规则随机）: 首次=%+v，本次=%+v", i+1, first, got)
				}
			}
			if first != c.want {
				t.Errorf("文案不对: got %+v, want %+v", first, c.want)
			}
		})
	}
}

// 底层错误详情必须压过业务层兜底话术：rawError 常是 "LLM 调用失败" 这类我们自己的描述，
// 不能让它掩盖掉上游真实原因。
func TestGetFriendlyError_UnderlyingErrorWinsOverGenericWrapper(t *testing.T) {
	got := getFriendlyError(errors.New("LLM 调用失败: 503 Service Unavailable"), "LLM 调用失败")
	if want := errorMessages["Service Unavailable"]; got != want {
		t.Errorf("应返回上游 503 的文案（可重试的「AI 服务暂时不可用」），实际 %+v", got)
	}
	if !got.Retryable {
		t.Errorf("503 应标记为可重试，实际 Retryable=false")
	}
}

// 逐条锁定典型错误的映射结果。
func TestGetFriendlyError_TypicalMapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		rawError string
		want     ErrorMessage
	}{
		{"503 状态码", errors.New("503"), "", errorMessages["503"]},
		{"429 状态码", errors.New("429"), "", errorMessages["429"]},
		{"上下文超长", errors.New("this model's maximum context length is 8192"), "", errorMessages["context length"]},
		{"仅业务层描述", nil, "会话不存在", errorMessages["会话不存在"]},
		{"模型配置", nil, "模型配置无效或无权访问", errorMessages["模型配置无效或无权访问"]},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := getFriendlyError(c.err, c.rawError); got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

// 认不出来的错误回落到通用文案，且标记为可重试。
func TestGetFriendlyError_UnknownErrorFallsBack(t *testing.T) {
	got := getFriendlyError(errors.New("dial tcp 127.0.0.1:11434: connect: connection refused"), "嵌入模型不可用")
	if got.Title != "操作失败" || !got.Retryable {
		t.Errorf("未知错误应回落到可重试的通用文案，实际 %+v", got)
	}
}

// 顺序表的构造不变量：每条 key 都必须能被自己的文本命中，
// 即不存在「被更长 key 遮蔽、永远匹配不到」的死规则。
func TestErrorKeysBySpecificity_EveryKeyReachable(t *testing.T) {
	if len(errorKeysBySpecificity) != len(errorMessages) {
		t.Fatalf("顺序表 key 数 %d 与 errorMessages 的 %d 不一致", len(errorKeysBySpecificity), len(errorMessages))
	}
	for _, key := range errorKeysBySpecificity {
		got, ok := matchErrorMessage(key)
		if !ok {
			t.Errorf("key %q 无法匹配自身", key)
			continue
		}
		if got != errorMessages[key] {
			t.Errorf("key %q 被其它规则遮蔽: got %+v, want %+v", key, got, errorMessages[key])
		}
	}
}

// 空片段不产生匹配，避免 err 为 nil 时误命中。
func TestMatchErrorMessage_EmptyText(t *testing.T) {
	if _, ok := matchErrorMessage(""); ok {
		t.Errorf("空文本不应命中任何规则")
	}
}
