// Package traceid 提供「一次请求的 trace 标识」及其在 context 上的读写。
//
// 为什么单独成包、而不是放在观测实现里：这个 id 有两个互不相关的用途 ——
//
//  1. 作为 eino 官方 Langfuse callback 的 trace id 下发（让平台上的 traceID 与自研对齐，
//     便于从日志/反馈反查平台那条 trace）；
//  2. 随 SSE 返回给前端、写进日志，用于串查一条请求的全过程。
//
// 观测实现是可替换的（本项目已经从自研 OTLP 换成官方 callback），
// 而「这个 id 长什么样」是业务契约 —— 两者绑在一起，换实现就会顺带改契约。
package traceid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

type ctxKey struct{}

// New 生成一个新的 trace id：16 字节随机数的 hex 编码，即 32 位小写十六进制。
//
// ⚠️ 长度和大小写都不是随手定的：Langfuse 的 `WithID` 内部用 `trace.TraceIDFromHex` 解析，
// 只认「恰好 32 位小写 hex」。换成 UUID（36 位、带连字符）会被**静默忽略**，
// 结果就是平台上的 traceID 与自研对不上，而且全程不报错。
func New() string {
	var b [16]byte
	// crypto/rand.Read 在 Go 1.24+ 的契约是「永不返回错误」（失败即 panic），
	// 所以这里不引入 error 出口 —— 多一个恒为 nil 的返回值只会让调用方多写一段死代码。
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// WithContext 把 id 写进 ctx；id 为空时原样返回（不写入空值，避免 FromContext 拿到 ""）。
func WithContext(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext 取 id；没有则返回空串。调用方按「空 = 没有」处理。
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// Ensure 保证 ctx 上一定有一个 id：已有就复用（同一请求内不换 id），没有就新生成。
// 返回的 ctx 已带上该 id，可直接往下传。
func Ensure(ctx context.Context) (context.Context, string) {
	if id := FromContext(ctx); id != "" {
		return ctx, id
	}
	id := New()
	return WithContext(ctx, id), id
}
