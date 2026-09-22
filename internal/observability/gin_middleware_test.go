package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"solvify-agent/pkg/config"
)

// newTestRecorder 构造一个启用状态的真实 Recorder。
// OTel Tracer 未初始化时 GlobalTracer() 会兜底返回 noop tracer，
// 因此这里不需要起 TracerProvider，也不依赖任何外部服务。
func newTestRecorder(t *testing.T) Recorder {
	t.Helper()
	rec := NewRecorder(config.ObservabilityConfig{
		Enabled:             true,
		SamplingRate:        1.0,
		PIIContentMaxChars:  2000,
		PIIMaskSecret:       true,
		SinkBufferSize:      16,
		SinkBatchSize:       4,
		SinkFlushIntervalMs: 20,
		ExportLogEnabled:    false,
	})
	t.Cleanup(func() { _ = rec.Shutdown(context.Background()) })
	return rec
}

// TestTraceMiddlewarePropagatesSpanContext 锁定 HTTP 根 span 的 context 传播。
//
// 回归背景：中间件曾写成 `_, span = StartSpan(...)`，把返回的 ctx 丢掉，
// 导致下游 chat / eino 组件在请求 context 里找不到父 span，各自新建根 span ——
// 同一个 HTTP 请求在 OTel 侧被拆成两棵互不相关的 trace，三方追踪平台看不到父子关系，
// 响应头 X-Trace-ID 也是空值。
func TestTraceMiddlewarePropagatesSpanContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := newTestRecorder(t)

	var (
		handlerTraceID string
		handlerSpan    *Span
		childTraceID   string
		childParentID  string
	)

	engine := gin.New()
	engine.Use(NewTraceMiddleware(rec).Handler())
	engine.POST("/api/v1/chat/sessions/:sessionID/messages", func(c *gin.Context) {
		ctx := c.Request.Context()
		handlerTraceID = TraceIDFromContext(ctx)
		handlerSpan = CurrentSpanFromContext(ctx)

		// 模拟 chat service 的调用姿势：先 WithTraceRoot，再开自己的根 span
		rootCtx := rec.WithTraceRoot(ctx, TraceRootAttrs{
			UserID:    "u1",
			SessionID: "s1",
			RequestID: RequestID(ctx),
		})
		_, child := rec.StartSpan(rootCtx, "chat.quick", ComponentAgentEngine, Attrs{})
		childTraceID = child.TraceID
		childParentID = child.ParentID
		rec.EndSpan(rootCtx, child, SpanStatusOK, nil, nil)

		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/sessions/s1/messages", nil)
	rec1 := httptest.NewRecorder()
	engine.ServeHTTP(rec1, req)

	if handlerSpan == nil {
		t.Fatal("请求 context 里拿不到 HTTP span：StartSpan 返回的 ctx 没有写回 Request")
	}
	if handlerSpan.Component != ComponentHTTPServer {
		t.Fatalf("HTTP span component 不符: got=%q want=%q", handlerSpan.Component, ComponentHTTPServer)
	}
	if handlerTraceID == "" {
		t.Fatal("请求 context 里的 traceID 为空，下游会另生成一条 trace")
	}
	// 关键断言：下游复用同一个 traceID，而不是各起一条
	if childTraceID != handlerTraceID {
		t.Fatalf("下游未复用 HTTP trace: child=%q handler=%q", childTraceID, handlerTraceID)
	}
	// 关键断言：下游根 span 的 parent 就是 HTTP span
	if childParentID != handlerSpan.SpanID {
		t.Fatalf("下游根 span 未挂在 HTTP span 下: childParentID=%q httpSpanID=%q", childParentID, handlerSpan.SpanID)
	}
	if got := rec1.Header().Get("X-Trace-ID"); got != handlerTraceID {
		t.Fatalf("X-Trace-ID 响应头不符: got=%q want=%q", got, handlerTraceID)
	}
	if rec1.Header().Get("X-Request-ID") == "" {
		t.Fatal("X-Request-ID 响应头缺失")
	}
}

// TestTraceMiddlewareExtractsInboundTraceparent 断言中间件真的做了入站 trace 上下文提取，
// http.request span 复用上游 traceID。
//
// 为什么必须有这一条：ExtractRemoteContext 本身测得再细，也证明不了「中间件里调了它」。
// 删掉中间件里的那一行调用，其它 propagation 测试依然全绿，但生产行为已经退化成
// 「每个服务各自一条 trace」—— 这正是本测试要守住的接线点。
func TestTraceMiddlewareExtractsInboundTraceparent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	installGlobalPropagator(t)
	// 装真实 SDK tracer，让自研 Span 能记下 OTel 轨道的 traceID（必须早于 NewRecorder）。
	installSDKTracer(t)
	rec := newTestRecorder(t)

	var (
		gotOTelTraceID  string
		gotInboundTrace bool
	)
	engine := gin.New()
	engine.Use(NewTraceMiddleware(rec).Handler())
	engine.POST("/api/v1/chat", func(c *gin.Context) {
		if s := CurrentSpanFromContext(c.Request.Context()); s != nil {
			gotOTelTraceID = s.OTelTraceID
			_, gotInboundTrace = s.Attrs["otel.inbound_trace_id"]
		}
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", nil)
	req.Header.Set("traceparent", "00-"+upstreamTraceID+"-"+upstreamSpanID+"-01")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if gotOTelTraceID != upstreamTraceID {
		t.Fatalf("http.request span 的 OTel traceID 应为上游的 %s，实际 %q —— 中间件没有提取入站 traceparent，跨服务链路会断",
			upstreamTraceID, gotOTelTraceID)
	}
	if !gotInboundTrace {
		t.Error("http.request span 缺少 otel.inbound_trace_id 属性，入站提取的信号没有暴露出来")
	}
}

// TestTraceMiddlewareWithoutRecorder 确认不传 Recorder 时中间件仍能放行请求。
func TestTraceMiddlewareWithoutRecorder(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reached := false
	engine := gin.New()
	engine.Use(NewTraceMiddleware(nil).Handler())
	engine.GET("/health", func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	if !reached {
		t.Fatal("Recorder 为 nil 时请求未到达 handler")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("状态码不符: got=%d want=%d", w.Code, http.StatusOK)
	}
}

// TestTraceMiddlewareExemptsProbePaths 锁定探针路径不建 trace。
//
// 为什么必须有这一条：健康检查会被集群探针高频拉取（10s 间隔约 1.7 万次/天）。
// 一旦被埋点，三方平台的计费单位（trace + observation）数天内就会打满免费额度，
// trace 列表也会被单 span 的健康检查刷屏、失去排障价值。
// 这类问题功能上完全看不出来（请求照常 200），所以必须由测试守住，不能只靠注释提醒。
func TestTraceMiddlewareExemptsProbePaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := newTestRecorder(t)

	var (
		probeSpanNil   bool
		probeTraceID   string
		bizSpanCreated bool
	)
	engine := gin.New()
	engine.Use(NewTraceMiddleware(rec).Handler())
	engine.GET("/health", func(c *gin.Context) {
		probeSpanNil = CurrentSpanFromContext(c.Request.Context()) == nil
		probeTraceID = TraceIDFromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})
	engine.GET("/api/v1/chat/sessions", func(c *gin.Context) {
		bizSpanCreated = CurrentSpanFromContext(c.Request.Context()) != nil
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("/health 未被正常放行: status=%d", w.Code)
	}
	if !probeSpanNil {
		t.Error("/health 不该建 span —— 探针高频拉取会打满三方平台额度并刷屏 trace 列表")
	}
	if probeTraceID != "" {
		t.Errorf("/health 不该分配 traceID，实际 %q", probeTraceID)
	}
	// 豁免的只是 span：请求标识与放行行为必须保持不变
	if w.Header().Get("X-Request-ID") == "" {
		t.Error("/health 仍应保留 X-Request-ID 响应头")
	}
	if got := w.Header().Get("X-Trace-ID"); got != "" {
		t.Errorf("/health 不该回 X-Trace-ID，实际 %q", got)
	}

	// 对照组：普通业务路径必须照旧建 span，
	// 防止「豁免名单写错」把正常接口一起豁免掉。
	w2 := httptest.NewRecorder()
	engine.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/api/v1/chat/sessions", nil))

	if !bizSpanCreated {
		t.Error("业务路径未建 span —— 豁免名单误伤了正常接口")
	}
	if w2.Header().Get("X-Trace-ID") == "" {
		t.Error("业务路径仍应回 X-Trace-ID")
	}
}
