package observability

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"solvify-agent/pkg/logger"
)

type userIDKey struct{}
type sessionIDKey struct{}
type requestIDKey struct{}

// SetUserID 将 userID 写入 context。
func SetUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, userIDKey{}, userID)
}

// UserID 从 context 读取 userID，兼容 gin.Context。
func UserID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if c, ok := ctx.(*gin.Context); ok {
		if v, exists := c.Get("user_id"); exists {
			if s, ok := v.(string); ok {
				return s
			}
		}
		ctx = c.Request.Context()
	}
	v := ctx.Value(userIDKey{})
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// SetSessionID 将 sessionID 写入 context。
func SetSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, sessionID)
}

// SessionID 从 context 读取 sessionID，兼容 gin.Context。
func SessionID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if c, ok := ctx.(*gin.Context); ok {
		if v, exists := c.Get("session_id"); exists {
			if s, ok := v.(string); ok {
				return s
			}
		}
		ctx = c.Request.Context()
	}
	v := ctx.Value(sessionIDKey{})
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// SetRequestID 将 requestID 写入 context。
func SetRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

// RequestID 从 context 读取 requestID，兼容 gin.Context。
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if c, ok := ctx.(*gin.Context); ok {
		if v, exists := c.Get("request_id"); exists {
			if s, ok := v.(string); ok {
				return s
			}
		}
		ctx = c.Request.Context()
	}
	v := ctx.Value(requestIDKey{})
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

type responseRecorder struct {
	gin.ResponseWriter
	body   *bytes.Buffer
	status int
	size   int
}

func (w *responseRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *responseRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}

func (w *responseRecorder) Status() int {
	if w.status == 0 {
		return 200
	}
	return w.status
}

func (w *responseRecorder) Size() int {
	return w.size
}

// TraceMiddleware 给每个 HTTP 请求打 OTel 根 span + 记录 Prometheus HTTP 指标。
type TraceMiddleware struct {
	Recorder Recorder
}

// NewTraceMiddleware 构造 TraceMiddleware。
func NewTraceMiddleware(recorder Recorder) *TraceMiddleware {
	return &TraceMiddleware{Recorder: recorder}
}

func (m *TraceMiddleware) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if requestID == "" {
			requestID = randomHex(8)
		}
		c.Set("request_id", requestID)
		c.Writer.Header().Set("X-Request-ID", requestID)

		ctx := c.Request.Context()
		ctx = SetRequestID(ctx, requestID)
		if m.Recorder != nil {
			ctx = context.WithValue(ctx, recorderKey, m.Recorder)
		}
		if userID, exists := c.Get("user_id"); exists {
			if s, ok := userID.(string); ok {
				ctx = SetUserID(ctx, s)
			}
		}

		// 入站 trace 上下文提取：必须放在下面 StartSpan("http.request") 之前。
		// 提取到的远程 span 会成为 http.request 的父节点，本次请求于是复用上游的
		// traceID，跨服务链路才能串成同一条 trace。上游没接 OTel（不带 traceparent）时
		// 这里是空操作，行为与改动前完全一致：HTTP 入口依旧是根 span。
		ctx, inboundTrace := ExtractRemoteContext(ctx, c.Request.Header)

		c.Request = c.Request.WithContext(ctx)

		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		if len(route) > 256 {
			route = route[:256]
		}
		method := c.Request.Method

		// 在途请求 Gauge
		metrics := GlobalMetrics()
		if metrics != nil && metrics.HTTPRequestInflight != nil {
			metrics.HTTPRequestInflight.WithLabelValues(method, route).Inc()
			defer metrics.HTTPRequestInflight.WithLabelValues(method, route).Dec()
		}

		start := time.Now()
		var span *Span
		if m.Recorder != nil {
			recAttrs := Attrs{
				"method":     method,
				"path":       c.Request.URL.Path,
				"route":      route,
				"remote_ip":  c.ClientIP(),
				"request_id": requestID,
			}
			if userID, ok := c.Get("user_id"); ok {
				if s, ok := userID.(string); ok {
					recAttrs["user_id"] = s
				}
			}
			// 只在真有入站父 span 时才记这几个属性，避免「没有上游」被误读成
			// 「上游要求不采样」（那时 Sampled 也是 false，两者必须靠 Present 区分）。
			// 排查链路断裂 / 采样被上游掐掉时，先看这几个属性。
			if inboundTrace.Present {
				recAttrs["otel.inbound_trace_id"] = inboundTrace.TraceID
				recAttrs["otel.inbound_parent_span_id"] = inboundTrace.SpanID
				recAttrs["otel.inbound_parent_sampled"] = inboundTrace.Sampled
			}
			// 必须接收 StartSpan 返回的 ctx 并写回请求：它携带 traceID、当前 span 引用
			// 和 OTel span。丢掉的后果是下游（chat → eino 组件）找不到父 span，各自新建根
			// span —— OTel 侧同一个 HTTP 请求被拆成两棵互不相关的 trace，三方追踪平台
			// 看不到父子关系；响应头 X-Trace-ID 也会是空值。
			ctx, span = m.Recorder.StartSpan(ctx, "http.request", ComponentHTTPServer, recAttrs)
			c.Request = c.Request.WithContext(ctx)
		}

		rec := &responseRecorder{ResponseWriter: c.Writer, body: bytes.NewBuffer(nil)}
		c.Writer = rec
		defer func() {
			if err := recover(); err != nil {
				stack := make([]byte, 4<<10)
				n := runtime.Stack(stack, false)
				stackStr := string(stack[:n])
				logger.Errorf("Panic recovered: %v\n%s", err, stackStr)
				if span != nil && m.Recorder != nil {
					m.Recorder.AddEvent(ctx, span, "panic", Attrs{
						"panic_type":  fmt.Sprintf("%T", err),
						"panic_value": truncateForEvent(fmt.Sprintf("%v", err)),
						"stack":       truncateForEvent(stackStr),
					})
				}
				if m.Recorder != nil {
					m.Recorder.Incr(ctx, "http_panic_total", map[string]string{
						"method": method,
						"route":  route,
						"type":   panicTypeName(err),
					}, 1)
				}
				if span != nil && m.Recorder != nil {
					m.Recorder.EndSpan(ctx, span, SpanStatusError, fmt.Errorf("panic: %v", err), Attrs{
						"status": 500,
					})
				}
				c.Writer.Header().Set("Content-Type", "application/json")
				c.Writer.WriteHeader(500)
				_, _ = c.Writer.Write([]byte(`{"code":500,"message":"服务异常，请联系管理员"}`))
				c.Abort()
				return
			}
		}()

		c.Next()

		dur := time.Since(start)
		status := rec.Status()
		c.Writer.Header().Set("X-Trace-ID", TraceIDFromContext(c.Request.Context()))
		statusGrp := statusGroup(status)
		if span != nil && m.Recorder != nil {
			attrs := Attrs{
				"status":       status,
				"bytes":         rec.Size(),
				"errors":        len(c.Errors),
				"status_group":  statusGrp,
			}
			if len(c.Errors) > 0 {
				attrs["last_error"] = truncateForEvent(c.Errors.Last().Error())
			}
			endStatus := SpanStatusOK
			var recErr error
			if len(c.Errors) > 0 {
				recErr = c.Errors.Last()
				endStatus = SpanStatusError
			} else if status >= 500 {
				endStatus = SpanStatusError
				recErr = fmt.Errorf("http status %d", status)
			} else if status == 499 || (c.Request.Context().Err() != nil) {
				endStatus = SpanStatusCanceled
			}
			m.Recorder.EndSpan(c.Request.Context(), span, endStatus, recErr, attrs)
		}
		// HTTP 业务指标
		if m.Recorder != nil {
			m.Recorder.Incr(c.Request.Context(), "http_request_total", map[string]string{
				"method":       method,
				"route":        route,
				"status_group":  statusGrp,
			}, 1)
			m.Recorder.Observe(c.Request.Context(), "http_request_duration_seconds", map[string]string{
				"method": method,
				"route":  route,
			}, dur.Seconds())
			if status >= 400 {
				m.Recorder.Incr(c.Request.Context(), "http_error_total", map[string]string{
					"method":       method,
					"route":        route,
					"status_group": statusGrp,
				}, 1)
			}
		}
	}
}

func truncateForEvent(s string) string {
	const max = 512
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func statusGroup(status int) string {
	switch {
	case status < 200:
		return "1xx"
	case status < 300:
		return "2xx"
	case status < 400:
		return "3xx"
	case status < 500:
		return "4xx"
	default:
		return "5xx"
	}
}

func panicTypeName(err any) string {
	name := fmt.Sprintf("%T", err)
	name = strings.TrimPrefix(name, "*")
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	if name == "" {
		return "unknown"
	}
	return name
}

// StreamProgress 向事件通道非阻塞发送流式进度事件。
func StreamProgress(ctx context.Context, eventCh chan<- any, event string, payload any) {
	_ = ctx
	select {
	case eventCh <- map[string]any{"event": event, "payload": payload}:
	default:
	}
}

// ToInt64 将任意类型安全转换为 int64。
func ToInt64(v any) int64 {
	switch val := v.(type) {
	case int:
		return int64(val)
	case int32:
		return int64(val)
	case int64:
		return val
	case string:
		n, _ := strconv.ParseInt(val, 10, 64)
		return n
	default:
		return 0
	}
}
