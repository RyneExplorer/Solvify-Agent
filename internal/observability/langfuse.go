package observability

import (
	"context"
	"time"

	langfuse "github.com/cloudwego/eino-ext/callbacks/langfuse/v2"
	"github.com/cloudwego/eino/callbacks"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/traceid"
)

// 本文件是「eino → Langfuse」这一条三方链路的**全部**内容：装配官方 callback、
// 开一条 trace、交最终答复、关闭。
//
// 边界（改动前先读这段）：本项目**不再有**自研的 span 树 / 落库 / 指标出口 ——
// eino 组件（ChatModel / Retriever / Tool / Embedding / Agent）的观测统一由官方 callback
// 产出并直发平台，业务侧只需要「开一条 trace」和「把最终答复交出去」两个动作。
//
// 官方 v2 与 v1 的生命周期完全不同（官方 README「从 v1 迁移」）：
//
//	v1: NewLangfuseHandler / SetTrace（选项塞 ctx，首次 eino 回调才建 trace）/ UpdateTraceOutput
//	v2: NewHandler / StartTrace（立刻建 root span，并把 traceRun 放进 ctx）/ EndTrace
//
// 关键差异是【trace 由谁持有】：v2 把 traceRun 放在 ctx 上，EndTrace 从 ctx 取回它。
// ⇒ ctx 就是唯一载体：StartTrace 返回的那个 ctx 必须一路传到 EndTrace，
//    中途换成 context.Background() 会让上报**静默失效**（官方取不到 traceRun 就什么都不做）。

// TraceRootAttrs 携带一条 trace 的根级属性。
type TraceRootAttrs struct {
	UserID     string
	SessionID  string
	MessageID  string
	RequestID  string
	SearchMode string
	ModelID    string
	// Input 是用户本次提问的原文，作为平台上的 trace 级输入。
	//
	// 为什么原文直出、不在这里截断：官方 v2 的 MaskFunc 覆盖 trace 级 input
	// （StartTrace 写 langfuse.observation.input 时调 c.prepare，trace.go:114）
	// 与观测级 input / output，所以脱敏是自动的；而「看完整提问」正是接三方平台的价值，
	// 本地截断等于把它废掉。长度上限交给官方 MaxAttributeValueLength / MaxSpanAttributeBytes 管。
	Input string
}

// Tracer 是官方 Langfuse callback 的封装面：只暴露「开 / 关一条 trace」需要的四个动作。
//
// 为什么还要封一层、不直接在业务里用官方 handler：
//
//  1. 业务侧不该知道 langfuse 这个包存在 —— 它只需要「本次 trace 开在哪、答复怎么交出去」；
//  2. trace 选项（name / release / tags / user / session / input / metadata）的拼装规则
//     必须只有一处（见 langfuseOptions），散到调用点必然漏下发；
//  3. 「未配置凭据」时返回 nil，业务侧靠 nil 接收者安全空转（见各方法的首行判空）。
//
// ⚠️ 下面所有方法都必须对 nil 接收者安全：本地开发不配凭据时 Tracer 就是 nil，
// 而调用点（chatService）不做分支。
type Tracer struct {
	handler *langfuse.CallbackHandler
	cfg     config.ObservabilityConfig
}

// NewTracer 装配官方 eino → Langfuse callback（v2）。
//
// 返回 (nil, nil) 表示「没配凭据 ⇒ 本次不接三方」，这是本地开发的正常状态，不算错误；
// 配了凭据却建不起来（host 写错、证书/代理问题等）返回 error —— 调用方必须响亮地记日志，
// 但**不要因此阻断启动**：可观测性不该让业务起不来。
//
// 「是否启用」的判据只有 `config.LangfuseEnabled()` 一处，而且它就卡在这里：
// 判据不成立 ⇒ 返回 nil Tracer ⇒ 调用点拿到的是 nil 接收者（各方法首行都判空）。
// 这样「handler 没注册但每条请求都在开 trace」这类错位在结构上不可能发生 ——
// 不靠调用点各自记得再判一次。
func NewTracer(ctx context.Context, cfg config.ObservabilityConfig) (*Tracer, error) {
	if !cfg.LangfuseEnabled() {
		logger.Infof("Langfuse 未配置完整（host/public_key/secret_key 有空缺），本次不接三方平台")
		return nil, nil
	}

	handler, err := langfuse.NewHandler(ctx, newLangfuseConfig(cfg))
	if err != nil {
		return nil, err
	}

	// 官方 README 的用法就是注册成 eino 全局 handler：所有走 eino 标准接口的组件
	// （ChatModel / Retriever / Tool / Embedding / Agent / Graph）自动产出 span。
	// 业务代码只在「一轮问答」的起止各调一次 Tracer.Start / Tracer.End，
	// 中间不需要手写任何埋点。
	callbacks.AppendGlobalHandlers(handler)

	t := &Tracer{handler: handler, cfg: cfg}
	logger.Infof("Langfuse 已接入（官方 eino callback v2 / OTLP）: host=%s batch=%d/%dms sample_rate=%.2f",
		cfg.LangfuseHost, cfg.LangfuseMaxExportBatchSize, cfg.LangfuseBatchTimeoutMs, cfg.LangfuseSampleRate)
	return t, nil
}

// newLangfuseConfig 把项目配置映射成官方 Config —— **生产与测试共用这一份**。
//
// 为什么必须共用：测试里手抄一份官方 Config，等于让「测试造的对端」与「生产装配的对端」
// 各写一遍。历史上就是这里少的，导致 `Config.Tags` 只在生产侧被设置、测试侧为空，
// 于是「静态标签被下发两遍」这个真缺陷在测试里完全不可见（全绿）。
// 抽成函数后，「同参」是结构保证的，不是靠人记得同步。
func newLangfuseConfig(cfg config.ObservabilityConfig) *langfuse.Config {
	return &langfuse.Config{
		Host:        cfg.LangfuseHost,
		PublicKey:   cfg.LangfusePublicKey,
		SecretKey:   cfg.LangfuseSecretKey,
		ServiceName: cfg.OTelServiceName,
		// Name 是「没传 WithName 时」的兜底 trace 名；正常路径由 langfuseOptions 显式下发。
		Name:       cfg.OTelServiceName,
		Release:    cfg.LangfuseRelease,
		Tags:       cfg.LangfuseTags,
		Timeout:    time.Duration(cfg.LangfuseTimeoutMs) * time.Millisecond,
		SampleRate: cfg.LangfuseSampleRate,

		MaxQueueSize:       cfg.LangfuseMaxQueueSize,
		MaxExportBatchSize: cfg.LangfuseMaxExportBatchSize,
		BatchTimeout:       time.Duration(cfg.LangfuseBatchTimeoutMs) * time.Millisecond,

		MaxSpanAttributeBytes: cfg.LangfuseMaxSpanAttributeBytes,
		// ⚠️ 刻意【不传】TracerProvider：官方只保证「callback 自己建的 provider」才认
		// WithID 钉过来的 traceID（其 Config 注释原文：Custom trace IDs ... are only
		// guaranteed when the callback creates the provider）。传一个外部 provider
		// 会让平台上的 traceID 变成随机值，与自研 id 对不上。
		// 同理不传 HTTPClient / SpanExporter，全走官方默认（OTLP/HTTP + gzip）。
		MaskFunc: func(text string) string { return MaskPII(text, cfg.PIIMaskSecret) },
	}
}

// Start 在 ctx 上开一条 trace，返回带 traceRun 的新 ctx。
//
// ⚠️ 返回的 ctx 必须一路传给 End：官方 EndTrace 是从 ctx 里取 traceRun 的，
// 取不到就静默什么都不做（没有 error 出口，所以丢了不会有任何报错）。
//
// trace id 取自 ctx（没有就新生成一个并写回 ctx），这样
// 「SSE / 日志里拿到的 id」与「平台上的 traceID」是同一个值。生成规则见 pkg/traceid。
func (t *Tracer) Start(ctx context.Context, attrs TraceRootAttrs) context.Context {
	if t == nil || t.handler == nil {
		return ctx
	}
	ctx, id := traceid.Ensure(ctx)
	return t.handler.StartTrace(ctx, langfuseOptions(t.cfg, id, attrs)...)
}

// End 记录本条 trace 的最终答复，并请求结束 trace。
//
// 两个直接返回的边界，都很常见且都无事可做：没接三方（t == nil）、答复为空
// （错误 / 中断路径本来就没有答复）。
//
// 官方的语义是「请求结束」而不是「立刻结束」：它要等所有仍活跃的 eino 子 span 结束
// 才真正收尾（trace.go 的 requestEnd + childEnded），所以这里不必也不能自己排空。
// 另外反复调用是安全的（官方按 run.ended 幂等）。
func (t *Tracer) End(ctx context.Context, output string) {
	if t == nil || t.handler == nil || output == "" {
		return
	}
	t.handler.EndTrace(ctx, output)
}

// Close 关闭官方 handler。
//
// 为什么必须 Shutdown 而不是只 Flush：官方 handler 自己持有 OTLP 批处理器与
// TracerProvider，只 Flush 不会释放 provider，退出前最后一批（最多一个 BatchTimeout 窗口）
// 会丢掉。官方 Shutdown 内部会先 endAll()（把仍未结束的 root 收尾）再关 provider。
func (t *Tracer) Close(ctx context.Context) error {
	if t == nil || t.handler == nil {
		return nil
	}
	return t.handler.Shutdown(ctx)
}

// langfuseOptions 组装一次请求要挂在官方 trace 上的选项。
//
// ⚠️ 官方 StartTrace 的语义是「opts 逐个覆盖 handler 默认值」
// （trace.go：`options := c.traceDefaults; for ... { opt(&options) }`），
// **但 WithTags 例外 —— 它是 append**（trace.go 的 WithTags 直接 `options.Tags = append(...)`）。
// 所以：
//
//   - 凡「每条 trace 都该带上」且官方逐项覆盖的字段（name / release / user / session / input），
//     必须在这里显式给出；少给一个，平台上那条 trace 的对应字段就是空的，而且全程不报错。
//   - **静态标签（langfuse_tags）刻意不在这里给**：官方 traceDefaults.Tags 已经装了 Config.Tags，
//     这里再送一遍就会在平台上得到 `[prod prod quick]` 这种重复。静态标签的唯一来源是 Config.Tags，
//     这里只补「本请求才知道」的那一个。
func langfuseOptions(cfg config.ObservabilityConfig, id string, attrs TraceRootAttrs) []langfuse.TraceOption {
	opts := make([]langfuse.TraceOption, 0, 8)

	// 自带 trace id 是官方推荐做法，理由正是「便于从自有 UI / 日志 deeplink」。
	// 用它成立的前提是 id 本来就合规（32 位小写 hex，见 pkg/traceid.New）。
	if id != "" {
		opts = append(opts, langfuse.WithID(id))
	}

	opts = append(opts,
		langfuse.WithName(cfg.OTelServiceName),
		langfuse.WithRelease(cfg.LangfuseRelease),
	)

	// trace 级输入 = 用户提问原文（官方 MaskFunc 会作用于它）。
	if attrs.Input != "" {
		opts = append(opts, langfuse.WithInput(attrs.Input))
	}

	// 本请求的业务标签（检索模式）。静态标签不在这里，原因见函数头注释。
	if attrs.SearchMode != "" {
		opts = append(opts, langfuse.WithTags(attrs.SearchMode))
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

// rootTraceMetadata 挑出要带到平台上的根级标识。
//
// ⚠️ 只放 ID 类字段：官方 v2 也会给 metadata 的每个值过一遍 MaskFunc，
// 但「能被脱敏」不等于「可以随便放」—— 明文内容的取舍应按业务语义单独决定，
// 而不是默认交给 masker。这里放的都是自研侧的关联 id，用来跨系统反查。
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
