package observability

// 本文件是「官方 eino → Langfuse callback（v2）」与自研轨之间的胶水层。
//
// 官方 v2 把生命周期整套换掉了（其 README「从 v1 迁移」一节的原文）：
//   v1: NewLangfuseHandler / SetTrace / UpdateTraceOutput
//   v2: NewHandler / StartTrace / EndTrace（另有 Flush / Shutdown）
// 关键差异是【trace 由谁持有】：
//   v1 把 trace 选项塞进 ctx，等第一次 eino 回调时才建 trace，output 靠 traceID 字符串找回；
//   v2 由 StartTrace 立刻建 root span、并把 traceRun 一起放进 ctx，EndTrace 从 ctx 取回它。
//   ⇒ v2 不需要 traceID 形参，ctx 就是唯一载体（SetTrace 那条路整个不需要了）。
//
// 两个方向各管一半：
//   - 入口：trace 级字段（id / name / release / tags / user / session / input / metadata）
//     由 Recorder.WithTraceRoot 经 handler.StartTrace 写进 ctx；
//   - 出口：trace 级 output 只能在答复产生之后补，由 Recorder.SetTraceOutput 转发给 EndTrace。
import (
	"context"

	langfuse "github.com/cloudwego/eino-ext/callbacks/langfuse/v2"
)

// LangfuseCallback 是官方 v2 Langfuse callback 中我们用到的最小能力面（两件事）。
//
// 为什么在 observability 侧定义接口、而不是让服务层直接依赖官方包：
// 由「消费方」定义接口 —— 服务层不需要知道 langfuse 这个包存在，只需要知道
// 「本次 trace 开在哪、最终答复怎么交出去」。官方 *langfuse.CallbackHandler 天然满足它。
//
// ⚠️ 为什么必须带上 StartTrace、不能只留 EndTrace：v2 里「开 trace」与「交答复」
// 是同一个 ctx 上的两个动作 —— 不在这里开，出口就找不到 traceRun，EndTrace 会静默什么都不做。
//
// 代价是接口签名里出现了官方的 langfuse.TraceOption。这是有意接受的：trace 选项本来就是
// 官方词汇，自己再造一套只会多一层没有价值的翻译；而它只在 recorder 内部被调用，
// 服务层依旧看不到 langfuse 这个包。
type LangfuseCallback interface {
	StartTrace(ctx context.Context, opts ...langfuse.TraceOption) context.Context
	EndTrace(ctx context.Context, output string)
}

// SetTraceOutput 把本次 trace 的最终答复推给三方平台（官方 EndTrace 语义）。
//
// ⚠️ 必须传「WithTraceRoot 返回的那个 ctx」：官方 EndTrace 是
// ctx.Value(traceRunKey{}) 取 traceRun（trace.go:135），取不到就【静默什么都不做】。
// 所以 ctx 一旦被换成 context.Background() 或另开一条链路，上报会无声失效。
//
// 这两种情况直接返回，因为它们都很常见且都无事可做：没接三方（callback 为 nil，
// 本地开发）、答复为空（错误 / 中断路径本来就没有答复）。
func (r *defaultRecorder) SetTraceOutput(ctx context.Context, output string) {
	if r == nil || r.langfuseCallback == nil || output == "" {
		return
	}
	r.langfuseCallback.EndTrace(ctx, output)
}

// MaskPII 按项目既定的 PII 规则脱敏，但【不截断】。
//
// 为什么不能直接用 SanitizeString：那个方法会按 ContentMaxChars（默认 200 rune）截断，
// 而三方平台的价值恰恰是看完整 prompt / 回复 —— 截到 200 字等于把内容废掉。
// 「长度」交给官方 SDK 自己的两个旋钮管：MaxAttributeValueLength（单值上限，我们没暴露，
// 吃官方默认 100 万字节）与 MaxSpanAttributeBytes（单 span 总预算，= 配置项
// langfuse_max_span_attribute_bytes）。这里只做脱敏，不截断。
//
// 签名刻意保持 func(string) string，直接就是官方 Config.MaskFunc 要的形状。
func (s *PIISanitizer) MaskPII(text string) string {
	return s.maskOnly(text)
}

// langfuseStartTrace 在 ctx 上开一条官方 trace，返回带 traceRun 的新 ctx。
//
// ⚠️ 调用时机有硬约束：必须放在「给自研 http 根 span 打 user.id / session.id」之后。
// 官方 StartTrace 第一步是 trace.ContextWithSpanContext(ctx, trace.SpanContext{})，
// 先把 ctx 里已有的 OTel span 清掉再起新 span ⇒ 在它之后再调 trace.SpanFromContext(ctx)，
// 拿到的是 Langfuse root span、而不是自研的 http.request span（会把自研轨的属性写歪）。
func (r *defaultRecorder) langfuseStartTrace(ctx context.Context, traceID string, attrs TraceRootAttrs) context.Context {
	if r == nil || r.langfuseCallback == nil {
		return ctx
	}
	return r.langfuseCallback.StartTrace(ctx, r.langfuseTraceOptions(traceID, attrs)...)
}

// langfuseTraceOptions 组装一次请求要挂在官方 trace 上的选项。
//
// ⚠️ 官方 StartTrace 的语义是「opts 覆盖 handler 默认值」
// （trace.go:97-102：options := c.traceDefaults; for ... { opt(&options) }），
// 所以凡是「每条 trace 都该带上」的字段都必须在这里显式给出：
// 少给一个，平台上那条 trace 的对应字段就是空的，而且【全程不会报错】。
func (r *defaultRecorder) langfuseTraceOptions(traceID string, attrs TraceRootAttrs) []langfuse.TraceOption {
	opts := make([]langfuse.TraceOption, 0, 8)

	// 自带 trace id 是官方推荐做法，官方给的理由正是「便于从自有 UI / 日志 deeplink」。
	// 用它成立的前提是自研 id 本来就合规：randomHex(16) 走 hex.EncodeToString，
	// 得到 32 位【小写】hex，正是 Langfuse 对 trace id 的格式要求。
	// 于是 chat_traces.id == 随助手消息返回给前端的 trace_id == 平台上的 traceID，三处同值。
	//
	// ⚠️ v2 里这条只在「callback 自己创建 TracerProvider」时才被保证（官方 Config 注释：
	// Custom trace IDs ... are only guaranteed when the callback creates the provider）。
	// 我们刻意不给 Config.TracerProvider，就是为了保住它。
	if traceID != "" {
		opts = append(opts, langfuse.WithID(traceID))
	}

	// name / release 显式给出，不依赖装配侧 Config 的同名字段：
	// v2 的 opts 是「逐个覆盖 handler 默认值」（StartTrace 先 options := c.traceDefaults，
	// 再 opt(&options)），所以这里给的值说了算；Config 那侧只是「没传 opt 时的兜底」
	// （Name 兜底 "eino-trace"，release 兜底为空）。
	opts = append(opts,
		langfuse.WithName(r.cfg.OTelServiceName),
		langfuse.WithRelease(r.cfg.LangfuseRelease),
	)

	// trace 级输入 = 用户提问原文。官方 MaskFunc 会作用于它 —— StartTrace 写
	// langfuse.observation.input 时会调 c.prepare（trace.go:114）—— 所以这里不做本地截断，
	// 平台上看完整提问才有价值。
	if attrs.Input != "" {
		opts = append(opts, langfuse.WithInput(attrs.Input))
	}

	// 静态标签 + 本请求的业务标签（检索模式）。自己合并以保证「静态在前、不重复」的顺序。
	if tags := mergeLangfuseTags(r.cfg.LangfuseTags, attrs.SearchMode); len(tags) > 0 {
		opts = append(opts, langfuse.WithTags(tags...))
	}
	if attrs.UserID != "" {
		opts = append(opts, langfuse.WithUserID(attrs.UserID))
	}
	if attrs.SessionID != "" {
		opts = append(opts, langfuse.WithSessionID(attrs.SessionID))
	}
	// 官方 WithMetadata 的入参是 map[string]string，内部按 key 合并进 trace metadata。
	if meta := rootTraceMetadata(attrs); len(meta) > 0 {
		opts = append(opts, langfuse.WithMetadata(meta))
	}
	return opts
}

// mergeLangfuseTags 合并静态标签与本次请求的业务标签：去空、去重、保持顺序（静态在前）。
func mergeLangfuseTags(static []string, requestTag string) []string {
	all := make([]string, 0, len(static)+1)
	all = append(all, static...)
	all = append(all, requestTag)

	out := make([]string, 0, len(all))
	seen := make(map[string]struct{}, len(all))
	for _, t := range all {
		if t == "" {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// rootTraceMetadata 挑出要带到平台上的根级标识。
//
// ⚠️ 只放 ID 类字段：不要因为「v2 也会给 metadata 过一遍 MaskFunc（trace.go:231，
// c.prepare 包住每个 metadata 值）」就把内容类字段塞进来 —— 脱敏是「保险」不是「许可」，
// 明文内容的取舍应该按业务语义单独决定，而不是默认交给 masker。
func rootTraceMetadata(attrs TraceRootAttrs) map[string]string {
	meta := make(map[string]string, 3)
	if attrs.MessageID != "" {
		meta["message_id"] = attrs.MessageID
	}
	if attrs.RequestID != "" {
		meta["request_id"] = attrs.RequestID
	}
	if attrs.ModelID != "" {
		meta["model_id"] = attrs.ModelID
	}
	return meta
}
