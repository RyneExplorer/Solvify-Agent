package service

import (
	"context"

	"solvify-agent/internal/observability"
)

// 以下 helper 统一做「Recorder 可能为 nil」的空安全降级，消除业务代码里散落的
// `if obsOk { obs.XXX(...) }` 样板。obs 为 nil（可观测性未注入）时静默跳过，
// 行为与原来的 `if obsOk == false` 分支完全一致。

// obsIncr 空安全的指标计数。
func obsIncr(ctx context.Context, obs observability.Recorder, metric string, labels map[string]string, delta int64) {
	if obs != nil {
		obs.Incr(ctx, metric, labels, delta)
	}
}

// obsObserve 空安全的指标观测。
func obsObserve(ctx context.Context, obs observability.Recorder, metric string, labels map[string]string, value float64) {
	if obs != nil {
		obs.Observe(ctx, metric, labels, value)
	}
}

// obsMarkError 空安全的错误标记。
func obsMarkError(ctx context.Context, obs observability.Recorder, err error) {
	if obs != nil && err != nil {
		obs.MarkTraceError(ctx, err)
	}
}

// obsAddRootAttrs 空安全的根属性追加。
func obsAddRootAttrs(ctx context.Context, obs observability.Recorder, attrs observability.Attrs) {
	if obs != nil {
		obs.AddRootAttrs(ctx, attrs)
	}
}

// obsSetTraceOutput 空安全的「把本次 trace 的最终答复推给三方平台」。
//
// 为什么不再校验 traceID：官方 v2 的 EndTrace 只认 ctx 里的 traceRun，
// 「本次有没有开 trace」由 ctx 自己说了算（取不到就静默跳过）。再补一层
// 「traceID 非空」是给同一条事实加了第二个来源，只会掩盖「StartTrace 没被调用」这类真问题。
//
// 为什么由这个 helper 承担判空、而不是让 Recorder 实现承担：两者都要判 —— 这里省掉的是
// `if obs != nil` 这类散落样板，实现内部那一层判的是「三方没接 / 答复为空」，
// 语义不同、都不该合并。
func obsSetTraceOutput(ctx context.Context, obs observability.Recorder, output string) {
	if obs == nil || output == "" {
		return
	}
	obs.SetTraceOutput(ctx, output)
}

// obsEndSpan 空安全的 span 结束。
func obsEndSpan(ctx context.Context, obs observability.Recorder, span *observability.Span, status observability.SpanStatus, err error, attrs observability.Attrs) {
	if obs != nil && span != nil {
		obs.EndSpan(ctx, span, status, err, attrs)
	}
}
