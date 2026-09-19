package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// 两个路由样本刻意取自生产真实路由（internal/api/v1/chat/routes.go），
// 而不是临时编一个字符串：配置默认值一旦写成 :sessionID 之类的错拼，
// 生产里就是一条静默失效的死配置，只有这样才守得住。
const (
	bizRouteSample   = "/api/v1/chat/sessions/:id/messages"
	noiseRouteSample = "/api/v1/chat/sessions/:id/traces"
	// parentRouteSample 是 /api/v1/chat/sessions —— 一个真实存在、且是上面两条路由
	// 共同前缀的模板。只有拿它做样本，才能把「精确匹配」和「前缀匹配」区分开。
	parentRouteSample = "/api/v1/chat/sessions"
)

// decideOnRoot 模拟「根 span 创建时采样器被问一次」。
//
// ParentContext 传空白 context：ParentBased 只在父 span 无效时才回调 root sampler，
// 传空才等价于真实的「本地根 span」。
func decideOnRoot(s sdktrace.Sampler, route string) sdktrace.SamplingDecision {
	var attrs []attribute.KeyValue
	if route != "" {
		attrs = append(attrs, attribute.String("route", route))
	}
	res := s.ShouldSample(sdktrace.SamplingParameters{
		ParentContext: context.Background(),
		TraceID: trace.TraceID{
			0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88,
			0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x01,
		},
		Name:       "http.request",
		Kind:       trace.SpanKindServer,
		Attributes: attrs,
	})
	return res.Decision
}

// TestSamplerForKeepsBizRouteRegardlessOfRate 锁定「业务路由必留」与采样率无关。
//
// 用 rate=0 这组最强取值：非业务路由一律丢、业务路由必须留。
// 这样断言的是「走的是独立分支」，而不是「概率上碰巧命中」——
// 后者在 rate=0 时给出的也是 Drop，测不出新增逻辑有没有真的接上。
func TestSamplerForKeepsBizRouteRegardlessOfRate(t *testing.T) {
	s := samplerFor(0, []string{bizRouteSample})

	if got := decideOnRoot(s, bizRouteSample); got != sdktrace.RecordAndSample {
		t.Fatalf("业务路由在 rate=0 时仍必须保留: got=%v want=%v", got, sdktrace.RecordAndSample)
	}
	if got := decideOnRoot(s, noiseRouteSample); got != sdktrace.Drop {
		t.Fatalf("非业务路由应遵循采样率（rate=0 即丢弃）: got=%v want=%v", got, sdktrace.Drop)
	}
}

// TestSamplerForDoesNotPrefixMatchBizRoute 守住「精确相等，不是前缀匹配」。
//
// 回归背景：噪声第一名 /api/v1/chat/sessions/:id/traces（追踪页自身轮询）占全部 HTTP
// 入口 span 的 29%，而它正挂在 /api/v1/chat/sessions 这条真实路由下面。
// 运维很自然会写成「/api/v1/chat/sessions，覆盖所有会话接口」——
// 实现一旦按前缀匹配，这一条配置就把最大的噪声源整个放回来，且平台上完全看不出来。
//
// 样本必须用「短模板」而不是完整模板：拿
// /api/v1/chat/sessions/:id/messages 去比 /api/v1/chat/sessions/:id/traces，
// 两者在倒数第二段就分叉了，精确匹配和前缀匹配都会判不命中 ——
// 那样写出来的测试区分不出实现，等于没写（本条测试的第一版就踩了这个坑，
// 用 -overlay 做故障注入时全绿才发现）。
func TestSamplerForDoesNotPrefixMatchBizRoute(t *testing.T) {
	s := samplerFor(0, []string{parentRouteSample})

	// 模板自己必须命中，否则说明匹配根本没工作
	if got := decideOnRoot(s, parentRouteSample); got != sdktrace.RecordAndSample {
		t.Fatalf("精确相等的路由未命中: got=%v want=%v", got, sdktrace.RecordAndSample)
	}
	// 挂在它下面的子路由不得被牵连
	for _, sub := range []string{bizRouteSample, noiseRouteSample} {
		if got := decideOnRoot(s, sub); got != sdktrace.Drop {
			t.Fatalf("前缀相同但不相等的路由 %s 被误判为业务路由（实现退化成前缀匹配了）: got=%v want=%v",
				sub, got, sdktrace.Drop)
		}
	}
}

// TestSamplerForAppliesRateToNonHTTPRootSpan 确认没有 route 属性的根 span 不受影响。
//
// 后台任务（会话摘要 / 记忆抽取 / 迟到 span 重发）派生的根 span 没有 HTTP 路由，
// 必须继续按采样率走，不能被业务白名单逻辑吞掉。
func TestSamplerForAppliesRateToNonHTTPRootSpan(t *testing.T) {
	if got := decideOnRoot(samplerFor(1, []string{bizRouteSample}), ""); got != sdktrace.RecordAndSample {
		t.Fatalf("rate=1 时无路由根 span 应保留: got=%v want=%v", got, sdktrace.RecordAndSample)
	}
	if got := decideOnRoot(samplerFor(0, []string{bizRouteSample}), ""); got != sdktrace.Drop {
		t.Fatalf("rate=0 时无路由根 span 应丢弃: got=%v want=%v", got, sdktrace.Drop)
	}
}

// TestSamplerForFollowsRemoteParentEvenOnBizRoute 确认跨服务一致性优先于业务白名单。
//
// 上游明确标记不采样时，业务路由同样留不下来 —— ParentBased 在「有父」时不会回调
// root sampler。这是刻意设计：跨服务链路要么整条留、要么整条丢，
// 半截链路（只有本服务 span、上游全缺）比没有链路更容易误导人。
func TestSamplerForFollowsRemoteParentEvenOnBizRoute(t *testing.T) {
	remoteNotSampled := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{
			0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88,
			0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x01,
		},
		SpanID:     trace.SpanID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		TraceFlags: 0, // 未采样
		Remote:     true,
	})
	res := samplerFor(0, []string{bizRouteSample}).ShouldSample(sdktrace.SamplingParameters{
		ParentContext: trace.ContextWithSpanContext(context.Background(), remoteNotSampled),
		TraceID:       remoteNotSampled.TraceID(),
		Name:          "http.request",
		Kind:          trace.SpanKindServer,
		Attributes:    []attribute.KeyValue{attribute.String("route", bizRouteSample)},
	})
	if res.Decision == sdktrace.RecordAndSample {
		t.Fatal("上游标记不采样时不应单独保留 —— 会产生只有本服务半截 span 的孤儿 trace")
	}
}

// TestTraceMiddlewareRouteAttrReachesSampler 是「中间件写属性」到「采样器读属性」的端到端守卫。
//
// 为什么不能只测采样器：上面几条都是直接构造 SamplingParameters 喂给采样器，
// 它们证明不了「生产链路上真的送得到 route 属性」。中间件把 recAttrs 里的 route 改名、
// 或 StartSpan 不再用 trace.WithAttributes 交给 tracer.Start，都会让业务白名单静默失效
// （业务链路又退回按采样率抽样），而上面几条依然全绿。
func TestTraceMiddlewareRouteAttrReachesSampler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// rate=0 + 单条业务路由：IsSampled() 为 true 就只可能来自业务白名单命中
	installSDKTracerWithSampler(t, samplerFor(0, []string{bizRouteSample}))
	rec := newTestRecorder(t)

	sampledByRoute := map[string]bool{}
	capture := func(c *gin.Context) {
		if s := CurrentSpanFromContext(c.Request.Context()); s != nil && s.otelSpan != nil {
			sampledByRoute[c.FullPath()] = s.otelSpan.SpanContext().IsSampled()
		}
		c.Status(http.StatusOK)
	}

	engine := gin.New()
	engine.Use(NewTraceMiddleware(rec).Handler())
	engine.POST(bizRouteSample, capture)
	engine.GET(noiseRouteSample, capture)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/chat/sessions/s1/messages"},
		{http.MethodGet, "/api/v1/chat/sessions/s1/traces"},
	} {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s 未正常放行: status=%d", tc.method, tc.path, w.Code)
		}
	}

	if !sampledByRoute[bizRouteSample] {
		t.Fatalf("业务路由 %s 的 http.request span 未被采样 —— 中间件的 route 属性没送进采样器"+
			"（属性名不一致，或 StartSpan 没把 attrs 交给 tracer.Start）", bizRouteSample)
	}
	if sampledByRoute[noiseRouteSample] {
		t.Fatalf("噪声路由 %s 被采样 —— 采样器没读到 route 属性（属性名不一致），或判定过宽", noiseRouteSample)
	}
}

// installSDKTracerWithSampler 与 dual_track_traceid_test.go 的 installSDKTracer 同形，
// 区别是采样器可指定：本文件的端到端断言建立在「采样决定由传入的采样器产生」之上。
func installSDKTracerWithSampler(t *testing.T, s sdktrace.Sampler) {
	t.Helper()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(s))
	prev := globalTracer
	globalTracer = tp.Tracer("biz-route-sampling-test")
	t.Cleanup(func() {
		globalTracer = prev
		_ = tp.Shutdown(context.Background())
	})
}
