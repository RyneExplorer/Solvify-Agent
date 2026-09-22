package observability

import (
	"context"
	"math"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	collectortracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"solvify-agent/pkg/config"
)

// 这一组测试是真正的端到端：进程内起一个 OTLP gRPC 接收端，让 exporter 通过真实 gRPC
// 连接把 span 发出去，再对「接收端实际拿到的东西」下断言。
//
// 为什么必须补这层：provider_test.go 里的 TestBuildOTelExporter 只断言
// "otlptracegrpc.New 没报错"。而 grpc.NewClient 是惰性建连 —— 构造成功不代表 TLS 配对了、
// 鉴权头发出去了。也就是说 WithInsecure / WithHeaders 写错时，那个冒烟测试照样通过。
// 本文件把这两件事变成可证伪的断言：
//   - 明文接收端 + insecure=true  → 必须送达，且 authorization metadata 必须收到；
//   - 明文接收端 + insecure=false → 必须失败（证明 TLS 真的"开"了，而不是被静默降级成明文）。
//
// 全部在进程内完成，不依赖任何外部 collector / 账号，可以直接进 CI。

// otlpTestReceiver 是一个最小的 OTLP trace 接收端：只把收到的请求与 gRPC metadata
// 存下来供断言，不做任何处理。
type otlpTestReceiver struct {
	collectortracev1.UnimplementedTraceServiceServer

	mu       sync.Mutex
	requests []*collectortracev1.ExportTraceServiceRequest
	metas    []metadata.MD
}

func (r *otlpTestReceiver) Export(ctx context.Context, req *collectortracev1.ExportTraceServiceRequest) (*collectortracev1.ExportTraceServiceResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.metas = append(r.metas, md)
	r.mu.Unlock()
	return &collectortracev1.ExportTraceServiceResponse{}, nil
}

// snapshot 返回已收到的请求与对应 metadata 的副本，避免断言时读到并发写入的切片。
func (r *otlpTestReceiver) snapshot() ([]*collectortracev1.ExportTraceServiceRequest, []metadata.MD) {
	r.mu.Lock()
	defer r.mu.Unlock()
	reqs := make([]*collectortracev1.ExportTraceServiceRequest, len(r.requests))
	copy(reqs, r.requests)
	mds := make([]metadata.MD, len(r.metas))
	copy(mds, r.metas)
	return reqs, mds
}

// startOTLPReceiver 在 127.0.0.1 的随机端口上起一个**明文** gRPC OTLP 接收端，
// 返回监听地址（host:port）与接收端本身。
func startOTLPReceiver(t *testing.T) (string, *otlpTestReceiver) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听随机端口失败: %v", err)
	}
	recv := &otlpTestReceiver{}
	gs := grpc.NewServer()
	collectortracev1.RegisterTraceServiceServer(gs, recv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return lis.Addr().String(), recv
}

// newProbeSpans 造一条已结束的 SDK span，用于直接调 Exporter.ExportSpans。
func newProbeSpans(t *testing.T, name string) []sdktrace.ReadOnlySpan {
	t.Helper()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	_, sp := tp.Tracer("otlp-export-probe").Start(context.Background(), name)
	sp.End()
	ro, ok := sp.(sdktrace.ReadOnlySpan)
	if !ok {
		t.Fatal("SDK span 未实现 ReadOnlySpan，无法直接导出")
	}
	return []sdktrace.ReadOnlySpan{ro}
}

// spanNames 摊平所有 ResourceSpans/ScopeSpans，返回接收端收到的全部 span 名。
func spanNames(reqs []*collectortracev1.ExportTraceServiceRequest) []string {
	var out []string
	for _, req := range reqs {
		for _, rs := range req.GetResourceSpans() {
			for _, ss := range rs.GetScopeSpans() {
				for _, sp := range ss.GetSpans() {
					out = append(out, sp.GetName())
				}
			}
		}
	}
	return out
}

// TestOTLPExporterDeliversSpansWithAuthHeader 证明「span 真的发出去了、鉴权头真的带上了」。
//
// 回归价值：把 buildOTelExporter 里的 WithHeaders 分支删掉，或把 WithInsecure 改成无条件调用，
// 本测试会失败 —— 这两点正是 provider_test.go 冒烟测试抓不到的。
func TestOTLPExporterDeliversSpansWithAuthHeader(t *testing.T) {
	addr, recv := startOTLPReceiver(t)

	exp, err := buildOTelExporter(context.Background(), config.ObservabilityConfig{
		OTelExporter:     "otlp",
		OTelOTLPEndpoint: addr,
		OTelInsecure:     true, // 接收端是明文 gRPC
		OTelHeaders:      map[string]string{"Authorization": "Bearer test-token"},
	})
	if err != nil {
		t.Fatalf("创建 OTLP exporter 失败: %v", err)
	}
	defer func() { _ = exp.Shutdown(context.Background()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exp.ExportSpans(ctx, newProbeSpans(t, "probe.export")); err != nil {
		t.Fatalf("ExportSpans 失败（明文接收端 + insecure=true 应当成功）: %v", err)
	}

	reqs, metas := recv.snapshot()
	if len(reqs) == 0 {
		t.Fatal("接收端没有收到任何 OTLP 请求")
	}
	if names := spanNames(reqs); len(names) == 0 || names[0] != "probe.export" {
		t.Errorf("接收端收到的 span 名不符: %v", names)
	}

	// 关键断言：鉴权头必须原样出现在 gRPC metadata 里（键按 gRPC 规范被规范化成小写）
	got := metas[0].Get("authorization")
	if len(got) == 0 {
		t.Fatalf("接收端没有收到 authorization metadata，实际 metadata=%v", metas[0])
	}
	if got[0] != "Bearer test-token" {
		t.Errorf("authorization = %q，期望 %q", got[0], "Bearer test-token")
	}
}

// TestOTLPExporterEngagesTLSWhenNotInsecure 证明 TLS 开关真的有效。
//
// 手法是"差分"：同一接收端、同一条 span，只改 OTelInsecure 一个开关。
//   - insecure=true  → 上一个测试里必须送达；
//   - insecure=false → 这里必须失败，且接收端一个请求都收不到。
//
// 底层原因是客户端真发了 TLS ClientHello、而明文 gRPC 服务端解析不了
// （实测报错：`tls: first record does not look like a TLS handshake`，
// 见下 t.Logf）。但 gRPC 会把这种连接错误当瞬时故障重试，要等 exporter 自带的
// 10s 导出超时才返回，所以这里给一个更短的 ctx 把测试时间压下来 ——
// 断言仍然是有效的差分：若哪天实现漏了判断、无条件 WithInsecure()，
// 导出会"成功"送达，exportErr == nil 这一步就会直接失败。
func TestOTLPExporterEngagesTLSWhenNotInsecure(t *testing.T) {
	addr, recv := startOTLPReceiver(t)

	exp, err := buildOTelExporter(context.Background(), config.ObservabilityConfig{
		OTelExporter:     "otlp",
		OTelOTLPEndpoint: addr,
		OTelInsecure:     false, // 不传 WithInsecure → 应当走 TLS
	})
	if err != nil {
		t.Fatalf("创建 OTLP exporter 失败: %v", err)
	}
	defer func() { _ = exp.Shutdown(context.Background()) }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	exportErr := exp.ExportSpans(ctx, newProbeSpans(t, "probe.tls"))

	if exportErr == nil {
		t.Fatal("insecure=false 指向明文接收端竟然导出成功：TLS 没有真正启用（WithInsecure 被无条件应用了？）")
	}
	// 明文服务端解析不了 TLS 握手，不应留下任何"成功送达"的请求
	if reqs, _ := recv.snapshot(); len(reqs) != 0 {
		t.Errorf("TLS 握手失败却仍有 %d 个请求到达接收端", len(reqs))
	}
	// 报错措辞随 grpc 版本变化，不做强断言，只在日志留证
	t.Logf("insecure=false 导出按预期失败: %v", exportErr)
}

// TestGenAIAttrsOnExportedOTelSpan 走完整链路验证 gen_ai 属性真的落到了**导出的 OTel span** 上：
//
//	eino callback → Recorder → OTel SDK → OTLP gRPC → 接收端
//
// 补的是 eino_callback_test.go 的盲区：那里断言的是自研 `Span.Attrs`（落 chat_traces 那份）
// 以及 attrsToOTel 的转换函数，从 SetAttributes 到真正导出的那一跳没有覆盖。
func TestGenAIAttrsOnExportedOTelSpan(t *testing.T) {
	addr, recv := startOTLPReceiver(t)

	exp, err := buildOTelExporter(context.Background(), config.ObservabilityConfig{
		OTelExporter:     "otlp",
		OTelOTLPEndpoint: addr,
		OTelInsecure:     true,
	})
	if err != nil {
		t.Fatalf("创建 OTLP exporter 失败: %v", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(exp),
	)
	// tp.Shutdown 会连带关闭 exporter，所以这里不再单独 defer exp.Shutdown
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	// 把全局 tracer 换成真的 SDK tracer；测试结束必须还原，
	// 否则同包其他测试会以为"没有 tracer 可用"而拿到这个已关闭的 provider。
	prevTracer := globalTracer
	globalTracer = tp.Tracer("genai-e2e")
	t.Cleanup(func() { globalTracer = prevTracer })

	RegisterGenAIProvider("deepseek-v4-flash", "deepseek")

	rec := newCaptureRecorder(t)
	info := &callbacks.RunInfo{
		Name:      "ChatModelGenerate",
		Type:      "OpenAI",
		Component: components.ComponentOfChatModel,
	}
	input := &model.CallbackInput{
		Messages: []*schema.Message{
			{Role: schema.System, Content: "你是知识助理"},
			{Role: schema.User, Content: "什么是 Go"},
		},
		Config: &model.Config{
			Model:       "deepseek-v4-flash",
			Temperature: 0.3,
			MaxTokens:   1024,
		},
	}
	ctx, _ := startEinoSpan(t, rec, info, input)

	// 流式 chunk 按真实形态造：内容 chunk 带 Message，收尾 chunk 只有 TokenUsage，
	// finish_reason 在第三个 chunk 的 ResponseMeta 上。
	chunks := []callbacks.CallbackOutput{
		&model.CallbackOutput{
			Message: &schema.Message{Role: schema.Assistant, Content: "Go 是"},
			Config:  &model.Config{Model: "deepseek-v4-flash"},
		},
		&model.CallbackOutput{
			Config: &model.Config{Model: "deepseek-v4-flash"},
			TokenUsage: &model.TokenUsage{
				PromptTokens:     12,
				CompletionTokens: 34,
				TotalTokens:      46,
			},
			Message: nil,
		},
		&model.CallbackOutput{
			Message: &schema.Message{
				Role:         schema.Assistant,
				ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"},
			},
			Config: &model.Config{Model: "deepseek-v4-flash"},
		},
	}
	NewEinoCallbackHandler(rec).OnEndWithStreamOutput(ctx, info, schema.StreamReaderFromArray(chunks))
	waitSpan(t, rec) // 等到异步 EndSpan 真的完成，属性才算写完

	// 把 BatchSpanProcessor 缓冲的 span 刷到接收端
	flushCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tp.ForceFlush(flushCtx); err != nil {
		t.Fatalf("ForceFlush 失败: %v", err)
	}

	reqs, _ := recv.snapshot()
	exported := findExportedSpan(reqs, "ChatModelGenerate")
	if exported == nil {
		t.Fatalf("接收端没有找到名为 ChatModelGenerate 的 span，实际收到: %v", spanNames(reqs))
	}
	if len(exported.GetTraceId()) == 0 {
		t.Error("导出的 span 没有 trace id")
	}

	attrs := attrMap(exported.GetAttributes())

	// 字符串类：身份 / 模型 / finish_reason
	for key, want := range map[string]string{
		AttrGenAIOperationName: "chat",
		AttrGenAIProviderName:  "deepseek",
		AttrGenAIRequestModel:  "deepseek-v4-flash",
		AttrGenAIResponseModel: "deepseek-v4-flash",
	} {
		if got := attrs[key].GetStringValue(); got != want {
			t.Errorf("导出 span 的 %s = %q，期望 %q", key, got, want)
		}
	}
	// 数值类：token 用量必须以 INT 落地，不能退化成字符串
	for key, want := range map[string]int64{
		AttrGenAIUsageInputTokens:  12,
		AttrGenAIUsageOutputTokens: 34,
		AttrGenAIRequestMaxTokens:  1024,
	} {
		if got := attrs[key].GetIntValue(); got != want {
			t.Errorf("导出 span 的 %s = %d（AnyValue=%+v），期望 int %d", key, got, attrs[key], want)
		}
	}
	// 浮点类：temperature 必须是 DOUBLE（float32 落到字符串兜底的老 bug 由这条兜住）
	if got := attrs[AttrGenAIRequestTemperature].GetDoubleValue(); math.Abs(got-0.3) > 1e-6 {
		t.Errorf("导出 span 的 %s = %v，期望约 0.3（double 类型）", AttrGenAIRequestTemperature, got)
	}
	// 数组类：finish_reasons
	vals := attrs[AttrGenAIResponseFinishReasons].GetArrayValue().GetValues()
	if len(vals) != 1 || vals[0].GetStringValue() != "stop" {
		t.Errorf("导出 span 的 %s = %+v，期望 [\"stop\"]", AttrGenAIResponseFinishReasons, vals)
	}
	// 布尔类：流式标记
	if !attrs[AttrGenAIRequestStream].GetBoolValue() {
		t.Errorf("导出 span 的 %s 不是 true", AttrGenAIRequestStream)
	}
	// 自研 attrs 仍在（两套并存，前端还靠它）
	if attrs["model_id"].GetStringValue() != "deepseek-v4-flash" {
		t.Errorf("导出 span 丢了自研 model_id: %+v", attrs["model_id"])
	}
}

// findExportedSpan 在接收端已收到的请求里按名字找 span。
func findExportedSpan(reqs []*collectortracev1.ExportTraceServiceRequest, name string) *tracev1.Span {
	for _, req := range reqs {
		for _, rs := range req.GetResourceSpans() {
			for _, ss := range rs.GetScopeSpans() {
				for _, sp := range ss.GetSpans() {
					if sp.GetName() == name {
						return sp
					}
				}
			}
		}
	}
	return nil
}

// attrMap 把 OTLP 属性列表摊平成 key → AnyValue。
func attrMap(kvs []*commonv1.KeyValue) map[string]*commonv1.AnyValue {
	out := make(map[string]*commonv1.AnyValue, len(kvs))
	for _, kv := range kvs {
		out[kv.GetKey()] = kv.GetValue()
	}
	return out
}
