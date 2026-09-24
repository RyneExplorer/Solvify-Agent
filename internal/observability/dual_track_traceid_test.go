package observability

import (
	"context"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"solvify-agent/pkg/config"
)

// 这一组测试覆盖「双轨 traceID 对齐」。
//
// 背景：项目里同时存在两条互不相干的 traceID 轨道 ——
//   - 自研轨道：randomHex(16) 生成，写 chat_traces.id，前端追踪详情页按它查数据
//   - OTel 轨道：SDK 生成，推给三方追踪平台（ByteAPM / Jaeger / Tempo），平台按它检索
// 对齐前两者毫无关联，所以「我们系统里的这条 trace」无法跳到「三方平台上的同一条链路」，
// 只能靠时间戳人工肉眼比对。对齐后 chat_traces.otel_trace_id 携带 OTel 轨道的 traceID。
//
// 本文件把下面三条性质变成可证伪的断言：
//  1. 自研 Span 记录了 OTel 轨道的 traceID / spanID，同一 trace 内 OTel traceID 一致，
//     且两套 ID 不相等（不是把同一个 ID 抄两遍）；
//  2. FlushTrace 落库的 Trace.OTelTraceID，必须等于**接收端真实收到的那个 span 的 trace_id**
//     —— 这是「DB 里的 ID 真能在三方平台查到」的直接证据，而不是只断言两个内部字符串相等；
//  3. 没挂 exporter（OTelExporter=noop，开发环境默认）时，OTelTraceID 依然非空（SpanContext
//     本来就合法），但 OTelExported 必须为 false，避免前端给出点进去一片空白的跳转入口。

// capturingDBSink 是一个只做记录的 DBSink 实现，用来抓取落库前的 *Trace。
type capturingDBSink struct {
	mu     sync.Mutex
	traces []*Trace
}

func (s *capturingDBSink) Write(context.Context, *SinkRecord) error { return nil }
func (s *capturingDBSink) Shutdown(context.Context) error           { return nil }

func (s *capturingDBSink) WriteTraces(_ context.Context, traces []*Trace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traces = append(s.traces, traces...)
	return nil
}

func (s *capturingDBSink) WriteFeedbacks(context.Context, []*Feedback) error { return nil }
func (s *capturingDBSink) WriteAgentSteps(context.Context, []*AgentStep) error {
	return nil
}

// onlyTrace 返回唯一一条被抓到的 trace；数量不为 1 时直接失败。
func (s *capturingDBSink) onlyTrace(t *testing.T) *Trace {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.traces) != 1 {
		t.Fatalf("期望 DBSink 收到恰好 1 条 trace，实际 %d 条", len(s.traces))
	}
	return s.traces[0]
}

// testObsConfig 造一份「自研采样率拉满 + 写 trace 表」的配置，保证 FlushTrace 一定会落库。
func testObsConfig() config.ObservabilityConfig {
	return config.ObservabilityConfig{
		Enabled:              true,
		SamplingRate:         1.0,
		ErrorAlwaysSample:    true,
		FeedbackAlwaysSample: true,
		TraceTableEnabled:    true,
		ExportLogEnabled:     false, // 关掉日志 sink，避免测试输出被 payload 刷屏
		SinkBufferSize:       64,
		SinkBatchSize:        8,
		SinkFlushIntervalMs:  10,
		PIIContentMaxChars:   200,
		PIIMaskSecret:        true,
	}
}

// installSDKTracer 把全局 tracer 换成真实 SDK TracerProvider（AlwaysSample），
// 测试结束还原 —— 不还原会让同包其它测试拿到一个已关闭的 provider。
func installSDKTracer(t *testing.T) {
	t.Helper()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	prev := globalTracer
	globalTracer = tp.Tracer("dual-track-test")
	t.Cleanup(func() {
		globalTracer = prev
		_ = tp.Shutdown(context.Background())
	})
}

// installOTelExportActive 覆盖全局 otelExportActive 标志并在测试结束还原。
// 生产里由 InitTracerProvider 依据「exporter 是否构造成功」设置，测试需要单独控制。
func installOTelExportActive(t *testing.T, active bool) {
	t.Helper()
	prev := otelExportActive.Load()
	otelExportActive.Store(active)
	t.Cleanup(func() { otelExportActive.Store(prev) })
}

// newDualTrackRecorder 造一个带 capturingDBSink 的 recorder，并在测试结束时关闭 sink。
func newDualTrackRecorder(t *testing.T, sink *capturingDBSink) Recorder {
	t.Helper()
	rec := NewRecorderWithDBSink(testObsConfig(), sink)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = rec.Shutdown(ctx)
	})
	return rec
}

// exportedTraceIDs 摊平接收端收到的所有 span，返回其中出现过的 trace_id（十六进制）。
func exportedTraceIDs(reqs []*collectortracev1.ExportTraceServiceRequest) map[string]struct{} {
	out := map[string]struct{}{}
	for _, req := range reqs {
		for _, rs := range req.GetResourceSpans() {
			for _, ss := range rs.GetScopeSpans() {
				for _, sp := range ss.GetSpans() {
					out[hex.EncodeToString(sp.GetTraceId())] = struct{}{}
				}
			}
		}
	}
	return out
}

// TestSpanRecordsOTelTrackIDs 断言自研 Span 上记录了 OTel 轨道的 ID，且两条轨道互相独立、
// 各自在 trace 内保持一致。
//
// 回归价值：删掉 StartSpan 里那段 `sc := otelSpan.SpanContext()` 的捕获，本测试立刻变红。
func TestSpanRecordsOTelTrackIDs(t *testing.T) {
	installSDKTracer(t)
	installOTelExportActive(t, true)

	rec := newDualTrackRecorder(t, &capturingDBSink{})
	ctx, root := rec.StartSpan(context.Background(), "http.request", ComponentHTTPServer, Attrs{"route": "/api/v1/chat"})

	if len(root.OTelTraceID) != 32 {
		t.Fatalf("OTelTraceID 期望 32 位十六进制（128bit），实际 %q", root.OTelTraceID)
	}
	if _, err := hex.DecodeString(root.OTelTraceID); err != nil {
		t.Errorf("OTelTraceID 不是合法十六进制: %q", root.OTelTraceID)
	}
	if len(root.OTelSpanID) != 16 {
		t.Errorf("OTelSpanID 期望 16 位十六进制（64bit），实际 %q", root.OTelSpanID)
	}
	// 两套轨道必须是不同的 ID：自研 ID 是自己 randomHex 生成的，OTel ID 由 SDK 生成。
	if root.OTelTraceID == root.TraceID {
		t.Errorf("自研 traceID 与 OTel traceID 不该相同（说明抄成了同一个 ID）: %q", root.TraceID)
	}

	_, child := rec.StartSpan(ctx, "chat.quick", ComponentServiceChat, Attrs{"model_id": "deepseek-v4-flash"})

	if child.TraceID != root.TraceID {
		t.Errorf("自研 traceID 应在同一 trace 内保持一致: root=%q child=%q", root.TraceID, child.TraceID)
	}
	if child.OTelTraceID != root.OTelTraceID {
		t.Errorf("OTel traceID 应在同一 trace 内保持一致: root=%q child=%q", root.OTelTraceID, child.OTelTraceID)
	}
	if child.OTelSpanID == root.OTelSpanID {
		t.Error("不同 span 的 OTelSpanID 不该相同")
	}
	if child.OTelSpanID == child.SpanID {
		t.Error("自研 spanID 与 OTel spanID 不该相同")
	}
}

// TestFlushTraceOTelTraceIDMatchesExportedSpan 是这组测试里最关键的一条：
// 它不只断言「DB 里的 otel_trace_id 等于内部记录的某个字符串」，而是断言
// **这个 ID 真的能在 OTLP 接收端收到的 span 里找到** —— 也就是「能跳到三方平台」这件事本身。
func TestFlushTraceOTelTraceIDMatchesExportedSpan(t *testing.T) {
	// 用内存 exporter 代替 OTLP 接收端：自研 OTLP 出口已随「改用官方 eino →
	// Langfuse callback」一并移除，但本测试要守的语义与导出协议无关 ——
	// 「DB 里的 otel_trace_id 必须真的对应一个被导出的 span」，这样前端拿着它
	// 去三方平台（现在是 Langfuse）才跳得过去。
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSyncer(exp),
	)
	// tp.Shutdown 会连带关闭 exporter
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	prevTracer := globalTracer
	globalTracer = tp.Tracer("dual-track-e2e")
	t.Cleanup(func() { globalTracer = prevTracer })
	installOTelExportActive(t, true)

	sink := &capturingDBSink{}
	rec := newDualTrackRecorder(t, sink)

	// 按真实链路顺序走：HTTP 中间件先建根 span → chat 服务 WithTraceRoot 绑定业务信息
	// → 建 chat.quick 业务根 span → EndSpan → FlushTrace 落库。
	ctx, httpSpan := rec.StartSpan(context.Background(), "http.request", ComponentHTTPServer, Attrs{"route": "/api/v1/chat"})
	ctx = rec.WithTraceRoot(ctx, TraceRootAttrs{
		UserID:     "u-1",
		SessionID:  "s-1",
		MessageID:  "m-1",
		SearchMode: "quick",
		ModelID:    "deepseek-v4-flash",
	})
	ctx, chatSpan := rec.StartSpan(ctx, "chat.quick", ComponentServiceChat, Attrs{"model_id": "deepseek-v4-flash"})
	rec.EndSpan(ctx, chatSpan, SpanStatusOK, nil, nil)

	selfTraceID := rec.FlushTrace(ctx, "u-1", "s-1", "m-1")
	if selfTraceID == "" {
		t.Fatal("FlushTrace 返回了空的自研 traceID")
	}

	got := sink.onlyTrace(t)
	if got.ID != selfTraceID {
		t.Errorf("落库 trace 的自研 ID 不一致: got=%q want=%q", got.ID, selfTraceID)
	}
	if httpSpan.OTelTraceID == "" {
		t.Fatal("http.request span 没有记录 OTelTraceID，后续断言无意义")
	}
	if got.OTelTraceID != httpSpan.OTelTraceID {
		t.Errorf("落库的 OTelTraceID 与根 span 记录的不一致: got=%q want=%q",
			got.OTelTraceID, httpSpan.OTelTraceID)
	}
	if got.Root == nil {
		t.Fatal("落库 trace 的 Root 为 nil")
	}
	// 合成的 chat.request 根 span 没有 OTel span，但应带上这条 trace 的 OTel traceID，
	// 这样只读 span_tree JSON 的界面也能拿到跳转用的 ID。
	if got.Root.OTelTraceID != got.OTelTraceID {
		t.Errorf("span_tree 根 span 的 otel_trace_id 与顶层字段不一致: root=%q trace=%q",
			got.Root.OTelTraceID, got.OTelTraceID)
	}
	if !got.OTelExported {
		t.Error("挂了真实 exporter 且 AlwaysSample，OTelExported 应为 true")
	}

	// 关键一跳：验证 DB 里的 ID 真能在导出的 span 里找到。
	flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tp.ForceFlush(flushCtx); err != nil {
		t.Fatalf("ForceFlush 失败: %v", err)
	}
	found := false
	for _, s := range exp.GetSpans() {
		if s.SpanContext.TraceID().String() == got.OTelTraceID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("导出的 span 里没有 trace_id=%s —— 说明 DB 里的 otel_trace_id 对不上真实链路", got.OTelTraceID)
	}
}

// TestTraceOTelExportedFalseWithoutExporter 覆盖开发环境默认配置（OTelExporter=noop）：
// SpanContext 依然合法、OTelTraceID 依然有值（对日志串查有用），但三方平台什么都收不到，
// 因此 OTelExported 必须为 false。
//
// 回归价值：把 oTelExportedFor 里的 otelExportActive 判断删掉（只看 IsSampled），本测试变红。
func TestTraceOTelExportedFalseWithoutExporter(t *testing.T) {
	installSDKTracer(t)
	installOTelExportActive(t, false) // 模拟 OTelExporter=noop：没有 SpanProcessor 消费 span

	sink := &capturingDBSink{}
	rec := newDualTrackRecorder(t, sink)

	ctx, httpSpan := rec.StartSpan(context.Background(), "http.request", ComponentHTTPServer, nil)
	ctx = rec.WithTraceRoot(ctx, TraceRootAttrs{UserID: "u-1", SessionID: "s-1", MessageID: "m-1"})
	ctx, chatSpan := rec.StartSpan(ctx, "chat.quick", ComponentServiceChat, nil)
	rec.EndSpan(ctx, chatSpan, SpanStatusOK, nil, nil)
	rec.FlushTrace(ctx, "u-1", "s-1", "m-1")

	got := sink.onlyTrace(t)
	if got.OTelTraceID == "" {
		t.Error("noop TracerProvider 也会生成合法 SpanContext，OTelTraceID 不该为空")
	}
	if got.OTelTraceID != httpSpan.OTelTraceID {
		t.Errorf("OTelTraceID 应与根 span 记录一致: got=%q want=%q", got.OTelTraceID, httpSpan.OTelTraceID)
	}
	if got.OTelExported {
		t.Error("没有 exporter 消费时 OTelExported 必须为 false，否则前端会给出跳过去空白的入口")
	}
}
