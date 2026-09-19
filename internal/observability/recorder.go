package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// contextKey 用于在 context 中绑定 recorder / traceID / rootAttrs。
type contextKey string

const (
	traceIDKey   contextKey = "obs_trace_id"
	recorderKey  contextKey = "obs_recorder"
	rootAttrsKey contextKey = "obs_root_attrs"
)

type rootAttrs struct {
	mu         sync.Mutex
	attrs      Attrs
	beginAt    time.Time
	rootDone   bool
	endErr     error
	endStatus  SpanStatus
	endAt      time.Time
	messageID  string
	userID     string
	sessionID  string
	requestID  string
	searchMode string
	modelID    string
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// 发布后保活窗口与重发防抖，见 traceState 的注释。
const (
	// tracePublishGraceWindow 是 trace 落库后仍为其保活的时长。
	//
	// 为什么需要：会话摘要 / 记忆抽取这类后台任务的 span 在 HTTP 响应返回、主 trace
	// 已经落库之后才 End。旧实现「发布即 LoadAndDelete 状态」，迟到 span 找不到所属 trace，
	// 只能按无父根 span 另起一行写入 chat_traces —— 前端就多出一条 user/session=unknown 的
	// 「孤儿 trace」，与真正的会话彻底割裂。保活期内这类 span 会被并回同一行
	// （WriteTraces 是 upsert，重复写同一主键只会覆盖，不会新增行）。
	//
	// 取 3 分钟：后台任务最长链路 ≈ 3 次重试 × (45s + 15s·attempt) ≈ 135s，留约 2 倍余量。
	tracePublishGraceWindow = 3 * time.Minute

	// traceRepublishDebounce 把同一 trace 内连续 End 的多个迟到 span 合并成一次重发。
	// 摘要与记忆抽取两个后台 span 通常前后脚结束，不防抖会对同一行 upsert 两次。
	traceRepublishDebounce = 800 * time.Millisecond
)

// traceState 是一条 trace 在内存中的活跃状态，也是「迟到 span 合并」的载体。
//
// 生命周期：StartSpan 登记 → 首发落库（publishTrace / finalizeTrace）→
// 保活窗口内迟到 span 触发防抖重发（upsert 覆盖同一行）→ 窗口到期 GC 释放。
type traceState struct {
	// mu 保护下面的标量生命周期字段。
	mu sync.Mutex
	// traceMu 保护 Trace.Root 这棵树的 Children / Events 结构。
	//
	// 为什么必须单独一把锁：后台 span（DetachedTraceContext 派生）会在请求已经落库之后
	// 才 End 并往树上挂节点，而重发要克隆整棵树 —— 不加锁就是 slice 的并发读写。
	traceMu sync.Mutex
	// flushMu 串行化首发与重发，保证不会出现「后发起的重发先落库、
	// 用更旧的树覆盖更新的树」这种乱序写入。
	flushMu sync.Mutex

	Trace        *Trace
	decision     SampleDecision
	PendingForce bool

	// —— 发布后保活（迟到 span 合并）——
	// published 表示这条 trace 已进入「落库 + 保活」阶段。置位时机**早于**首次写库，
	// 这样写库期间（最长 10s）结束的后台 span 也能被感知并触发重发，不会静默漏掉。
	published bool
	// synthetic 区分两种发布形态：
	//   true  —— publishTrace 路径（chat 场景）：发布根是合成的 chat.request，Trace.Root 挂在其下
	//   false —— finalizeTrace 路径（非 chat 根 span）：Trace.Root 本身即发布根
	synthetic bool
	// sampled 是首发算出的采样决定。重发一律复用它 —— rollSample 用的是随机数，
	// 重算会让同一条 trace 在两次写入之间采样结果翻转，甚至把已落库的行「重发成不写」。
	sampled bool
	// otelTraceID / otelExported 是首发时从真实 OTel span 上抄下来的「双轨对齐」结果，
	// 与 sampled 同理首发即冻结。重发必须复用 —— 重发发生在请求结束之后，且 cloneSpan
	// 刻意不搬运 otelSpan，现算只会得到空 traceID / false；而落库是 upsert 整行覆盖，
	// 等于用「算不出来的假值」把首发写下的真值抹掉（同一 trace 运行中 true、事后 false）。
	otelTraceID  string
	otelExported bool
	// snap 是首发时对 rootAttrs 的快照。重发发生在请求结束之后，那时的 ctx 早已回收，
	// 只能靠快照拿 user / session / message 归属。
	snap *rootAttrsSnapshot
	// graceUntil 是状态回收时刻；每次迟到 span 到达都会把它往后推（滑动窗口续期）。
	graceUntil time.Time
	// treeGen 是树结构的代次，每次挂上新子 span 自增。重发写完后再对比一次：
	// 若写库期间又有 span 挂上，就再排一轮，保证不漏。内容为 0 表示树未变。
	treeGen uint64
	// republish 是防抖计时器，非空表示已有一轮重发在排队。
	republish *time.Timer
}

// rootAttrsSnapshot 是 rootAttrs 的只读快照，供 trace 重发时复用。
// 字段与 publishTrace 需要的一致，避免重发路径再去碰那个早已无人持有的 rootAttrs。
type rootAttrsSnapshot struct {
	userID     string
	sessionID  string
	requestID  string
	messageID  string
	searchMode string
	modelID    string
	beginAt    time.Time
	endAt      time.Time
	endErr     error
	endStatus  SpanStatus
	attrs      Attrs
}

// snapshotRootAttrs 拷贝 rootAttrs 的当前值。
// 顺带把 rootDone 置位，语义与旧实现一致：请求侧的根已经收口，后续不再被业务改写。
func snapshotRootAttrs(ra *rootAttrs) *rootAttrsSnapshot {
	snap := &rootAttrsSnapshot{}
	if ra == nil {
		return snap
	}
	ra.mu.Lock()
	defer ra.mu.Unlock()
	snap.userID = ra.userID
	snap.sessionID = ra.sessionID
	snap.requestID = ra.requestID
	snap.messageID = ra.messageID
	snap.searchMode = ra.searchMode
	snap.modelID = ra.modelID
	snap.beginAt = ra.beginAt
	snap.endAt = ra.endAt
	snap.endErr = ra.endErr
	snap.endStatus = ra.endStatus
	if len(ra.attrs) > 0 {
		snap.attrs = make(Attrs, len(ra.attrs))
		for k, v := range ra.attrs {
			snap.attrs[k] = v
		}
	}
	ra.rootDone = true
	return snap
}

// cloneSpan 深拷贝一棵 span 树，用于「快照式落库」。
//
// 为什么必须拷贝：后台 span 会在主 trace 已经落库之后继续往活树上挂，而写库路径
// （observabilityRepository.WriteTraces）会对这棵树做 stripInternalSpanAttrs 改写 + JSON marshal。
// 把活树直接交出去，等于让序列化过程和并发挂树赛跑 —— 轻则 marshal 出半棵树，
// 重则 slice 并发写。树规模只有几十个节点，拷贝成本可以忽略。
//
// otelSpan / parent 刻意不拷：两者都是 json:"-" 的运行期引用，序列化用不到，
// 拷过去反而埋下「对同一个 OTel span 调两次 End」的隐患。
func cloneSpan(s *Span) *Span {
	if s == nil {
		return nil
	}
	out := &Span{
		TraceID:     s.TraceID,
		SpanID:      s.SpanID,
		ParentID:    s.ParentID,
		Name:        s.Name,
		Component:   s.Component,
		StartAt:     s.StartAt,
		EndAt:       s.EndAt,
		DurationMs:  s.DurationMs,
		Status:      s.Status,
		Error:       s.Error,
		OTelTraceID: s.OTelTraceID,
		OTelSpanID:  s.OTelSpanID,
	}
	if len(s.Attrs) > 0 {
		out.Attrs = make(Attrs, len(s.Attrs))
		for k, v := range s.Attrs {
			out.Attrs[k] = v
		}
	}
	if len(s.Events) > 0 {
		out.Events = make([]*SpanEvent, 0, len(s.Events))
		for _, e := range s.Events {
			if e == nil {
				continue
			}
			ce := &SpanEvent{Name: e.Name, Timestamp: e.Timestamp}
			if len(e.Attrs) > 0 {
				ce.Attrs = make(Attrs, len(e.Attrs))
				for k, v := range e.Attrs {
					ce.Attrs[k] = v
				}
			}
			out.Events = append(out.Events, ce)
		}
	}
	if len(s.Children) > 0 {
		out.Children = make([]*Span, 0, len(s.Children))
		for _, c := range s.Children {
			if cloned := cloneSpan(c); cloned != nil {
				out.Children = append(out.Children, cloned)
			}
		}
	}
	return out
}

// mergeChildRoot 把自研轨登记的根 span 挂到即将落库的发布根下。
//
// 同名（都是 chat.request）说明自研轨里已经有显式的 chat.request span，
// 这时合并其子树 / 事件 / 属性，避免树里出现两个同名节点。
func mergeChildRoot(root, prev *Span) {
	if root == nil || prev == nil {
		return
	}
	if prev.Name != root.Name {
		root.Children = append(root.Children, prev)
		return
	}
	root.Children = append(root.Children, prev.Children...)
	root.Events = append(root.Events, prev.Events...)
	if root.Attrs == nil {
		root.Attrs = Attrs{}
	}
	for k, v := range prev.Attrs {
		if _, exists := root.Attrs[k]; !exists {
			root.Attrs[k] = v
		}
	}
}

// stampParentIDs 把每个节点的 ParentID 从「它在树里的位置」重新派生，根一律不写。
//
// 为什么不能沿用 Span 创建时记下的 parent：创建时还不知道自己最终会被挂到哪。chat 场景的
// 发布根是落库时才合成的 chat.request，而真实 HTTP 根 span 创建时根本没有父 —— 于是出现
// 「http.request 出现在 chat.request.children 里、自己的 parent_id 却是空」：按 parent_id
// 重建会散成两棵树，按 children 重建又和 parent_id 对不上。同名合并（两个 chat.request）
// 时 prev 的子节点被整体上提一层，旧 parent_id 同样失效。
// 所以父子关系只认树这一个来源，parent_id 是它的派生结果。
func stampParentIDs(root *Span) {
	if root == nil {
		return
	}
	root.ParentID = "" // 根没有父，前端靠「没有 parent_id」识别树的入口
	// 下面用内层闭包而不是递归调用自己：本函数的首件事是把入参的 ParentID 清空，
	// 递归时每层都会再清一次 —— 刚写好的父 id 会被自己抹掉，而且看起来还「全绿」。
	var walk func(n *Span)
	walk = func(n *Span) {
		for _, c := range n.Children {
			if c == nil {
				continue
			}
			c.ParentID = n.SpanID
			walk(c)
		}
	}
	walk(root)
}

// envelopeTreeIntervals 让每个节点的区间都包住它的整棵子树。
//
// 为什么不直接用测量值：父 span 的 EndAt 是「父自己那一刻结束」的时刻，有两种情况会让它
// 比子 span 更早，而在数据上它们只表现为同一个形状 —— 父比子短：
//
//  1. Eino 图节点在「返回流对象」时就 OnEnd（如 ChatModelGenerate 是 InvokableLambda），
//     真正的生成发生在后续流消费阶段：父 82ms / 子 eino.chat_model 4296ms，
//     耗时占比图会得出「生成几乎不花时间」这个完全相反的结论；
//  2. 会话摘要 / 记忆抽取这类后置任务在响应返回之后才跑完，根 span 比后代早结束 4.9s，
//     于是 trace.duration_ms 严重低估端到端耗时。
//
// 而「父比子短」本身就是自相矛盾的：子 span 还在跑，说明父当时并没有真正结束。
// 所以这里按树把父的区间扩到包住子树 —— 一条规则、无例外，前端占比图也才成立
// （子永远不会超过根，不需要靠 clamp 掩盖）。
//
// 口径：duration_ms = 该 span 及其子树的总跨度，不是「独占耗时」。
// 只在落库视图上做（改的是 flushTraceState 克隆出来的树）：OTel 轨的结束时刻是 SDK 按
// 真实回调记下的，不动它，否则两条轨道会对同一个 span 给出两个不同的耗时。
func envelopeTreeIntervals(n *Span) {
	if n == nil {
		return
	}
	start, end := n.StartAt, n.EndAt
	for _, c := range n.Children {
		if c == nil {
			continue
		}
		// 后序：先把子树自己撑开，再把结果并进来，一层扩到底
		envelopeTreeIntervals(c)
		if !c.StartAt.IsZero() && (start.IsZero() || c.StartAt.Before(start)) {
			start = c.StartAt
		}
		if c.EndAt.After(end) {
			end = c.EndAt
		}
	}
	n.StartAt = start
	n.EndAt = end
	// 从未结束过的 span（EndAt 为零值）保持原样，别算出一个负耗时
	if !start.IsZero() && !end.IsZero() {
		n.DurationMs = end.Sub(start).Milliseconds()
	}
}

// defaultRecorder 内部用 OTel Tracer 管运行时 span，用 promMetrics 管指标。
// Span 结构体仍保留，用于 DBSink 写 chat_traces 表（前端可视化数据源）。
type defaultRecorder struct {
	enabled     bool
	cfg         config.ObservabilityConfig
	sampler     *DefaultSampler
	sanitizer   *PIISanitizer
	sinks       Sink
	dbSink      DBSink
	metrics     *promMetrics
	tracer      trace.Tracer
	traceStates sync.Map
	traceDecide sync.Map
}

// NewRecorder 初始化 Recorder。需要在 InitTracerProvider / InitPrometheusRegistry 之后调用。
func NewRecorder(cfg config.ObservabilityConfig, extraSinks ...Sink) Recorder {
	sanitizer := NewPIISanitizer(cfg.PIIContentMaxChars, cfg.PIIMaskSecret)
	sampler := NewDefaultSampler(cfg.SamplingRate, cfg.ErrorAlwaysSample, cfg.FeedbackAlwaysSample, cfg.SlowThresholdMs, cfg.WhiteListUserIDs)
	logSink := NewLogSink(cfg.ExportLogEnabled, sanitizer, sampler)
	sinks := []Sink{logSink}
	sinks = append(sinks, extraSinks...)
	bs := NewBatchSink(sinks, cfg.SinkBufferSize, cfg.SinkBatchSize, cfg.SinkFlushIntervalMs)
	return &defaultRecorder{
		enabled:   cfg.Enabled,
		cfg:       cfg,
		sampler:   sampler,
		sanitizer: sanitizer,
		sinks:     bs,
		metrics:   GlobalMetrics(),
		tracer:    GlobalTracer(),
	}
}

// NewRecorderWithDBSink 在 NewRecorder 基础上挂 DBSink（写 chat_traces / chat_feedbacks / chat_agent_steps）。
func NewRecorderWithDBSink(cfg config.ObservabilityConfig, db DBSink) Recorder {
	r := NewRecorder(cfg).(*defaultRecorder)
	r.dbSink = db
	return r
}

// TraceIDFromContext 从 context 取 trace_id（由 WithTraceRoot 或 HTTP 中间件写入）。
// OTel 的 SpanContext 不直接暴露 TraceID 字符串，所以保留自研 traceIDKey 用于业务字段关联。
func TraceIDFromContext(ctx context.Context) string {
	v := ctx.Value(traceIDKey)
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// oTelTraceIDFor 取自研 span / ctx 对应的 OTel traceID（双轨 traceID 对齐用）。
//
// 查找顺序：
//  1. 自研 Span 上记录的 OTelTraceID —— 最可靠，创建时直接从 SDK 抄下来
//  2. ctx 里的 OTel span —— 用于手工构造、没有 otelSpan 的 span
//     （典型是 publishTrace 合成的 chat.request 根 span）
//
// 两者都没有时返回空串，不做任何伪造。
func oTelTraceIDFor(ctx context.Context, s *Span) string {
	if s != nil && s.OTelTraceID != "" {
		return s.OTelTraceID
	}
	if sc := trace.SpanFromContext(ctx).SpanContext(); sc.IsValid() {
		return sc.TraceID().String()
	}
	return ""
}

// oTelExportedFor 判断这条 trace 是否真的会出现在三方追踪平台上。
//
// 三个条件缺一不可 —— 只看 SpanContext 合法与否会误判（noop provider 也有合法 SpanContext）：
//  1. 挂了真实 exporter（otelExportActive）：OTelExporter=noop 时没有任何 SpanProcessor 消费 span
//  2. OTel 头采样命中（SpanContext.IsSampled）：采样率 < 1 时整条 trace 可能被丢弃
//  3. span / ctx 里确实存在 OTel span
func oTelExportedFor(ctx context.Context, s *Span) bool {
	if !otelExportActive.Load() {
		return false
	}
	if s != nil && s.otelSpan != nil {
		return s.otelSpan.SpanContext().IsSampled()
	}
	return trace.SpanFromContext(ctx).SpanContext().IsSampled()
}

// RecorderFromContext 从 context 取出绑定的 Recorder。
func RecorderFromContext(ctx context.Context) Recorder {
	v := ctx.Value(recorderKey)
	if v == nil {
		return nil
	}
	r, _ := v.(Recorder)
	return r
}

// currentSpanKey 用于 ctx 直接携带当前自研 *Span 引用。
// StartSpan 写入返回的 ctx，子 span 挂树和 CurrentSpanFromContext 都优先读它，
// 不依赖 OTel span 的 IsRecording 状态（eino 流式组件的 OnEnd 会提前 End 父 span）。
type currentSpanKey struct{}

// CurrentSpanFromContext 定位当前正在运行的 span。
// 返回项目自研 *Span（包含 otelSpan 字段），找不到时返回 nil，调用方静默降级。
func CurrentSpanFromContext(ctx context.Context) *Span {
	// 优先走 currentSpanKey：与 End 状态解耦，流式场景父 span 可能已被回调提前 End
	if s, ok := ctx.Value(currentSpanKey{}).(*Span); ok && s != nil {
		return s
	}
	// 兜底：OTel span 指针反查（不经 StartSpan 返回 ctx 的旧调用路径）
	otelSpan := trace.SpanFromContext(ctx)
	if otelSpan == nil {
		return nil
	}
	if !otelSpan.IsRecording() {
		return nil
	}
	if s, ok := spanByOtel.Load(spanKeyOf(otelSpan)); ok {
		return s.(*Span)
	}
	return nil
}

// otelSpanKey 是 spanByOtel 的键：OTel 的 traceID + spanID。
//
// 两个字段都是定长数组（[16]byte / [8]byte），所以本结构体可比较、可哈希 ——
// 这正是它存在的理由，详见 spanByOtel 的注释。
type otelSpanKey struct {
	traceID trace.TraceID
	spanID  trace.SpanID
}

// spanKeyOf 从任意 OTel span 算出它的键。
// 对 nonRecordingSpan 同样安全：SpanContext 一直可读，本函数也只做值拷贝。
func spanKeyOf(s trace.Span) otelSpanKey {
	sc := s.SpanContext()
	return otelSpanKey{traceID: sc.TraceID(), spanID: sc.SpanID()}
}

// spanByOtel 把 OTel span 关联到项目自研 Span，供 CurrentSpanFromContext 的兜底路径反查。
// StartSpan 写入，EndSpan 删除（避免内存泄漏）。
//
// key 是 otelSpanKey，不是 trace.Span 接口值本身。为什么不能用后者：
// 采样决定为「丢弃」时，SDK 返回的是 sdk/trace.nonRecordingSpan —— 一个值类型，
// 它内嵌的 SpanContext 里的 TraceState 是 slice，属于「类型上不可哈希」。
// 拿它调 sync.Map 的 Store / Delete 会直接 panic: hash of unhashable type，
// 而 Delete 同样要算哈希，所以只给 Store 加守卫是挡不住的（漏了 Delete 照样崩）。
// 换成两个定长数组做键，可哈希就由类型系统保证，不再依赖调用方记得加守卫。
//
// 为什么用 TraceID+SpanID 而不是 span 指针：SpanContext 在 span 结束后保持不变，
// 于是 EndSpan 侧能算出与 StartSpan 完全相同的键；数组键也不需要解引用或担心复用。
//
// 注意：OTelExporter=noop 时所有 noop span 的键都是零值、会互相覆盖 ——
// 但兜底路径先判 IsRecording()，noop span 恒为 false，永远不会读到这个键。
var spanByOtel sync.Map

// SetSpanAttrs 在 ctx 对应的当前 span 上直接追加/覆盖 attrs。
//
// 典型场景：RAG Retriever、LLM Client 等底层 adapter 在 Graph OnStart 开好 span 之后、
// EndSpan 关闭 span 之前，从内部补全细粒度业务字段（top_k/hit_n/avg_score/token 用量等）。
//
// 实现细节：
//   - 找不到当前 span 时静默返回（不影响业务流程）
//   - OTel span 的 SetAttributes 是幂等的，相同 key 会覆盖
//   - 自动走 PII Sanitizer，避免敏感信息落到 attrs
func SetSpanAttrs(ctx context.Context, attrs Attrs) {
	span := CurrentSpanFromContext(ctx)
	if span == nil || len(attrs) == 0 {
		return
	}
	sanitized := attrs
	if rec := RecorderFromContext(ctx); rec != nil {
		if dr, ok := rec.(*defaultRecorder); ok && dr != nil && dr.sanitizer != nil {
			sanitized = dr.sanitizer.SanitizeAttrs(attrs)
		}
	}
	// 写 OTel span（运行时追踪用）
	if span.otelSpan != nil {
		span.otelSpan.SetAttributes(attrsToOTel(sanitized)...)
	}
	// 写项目自研 Span（落库用）
	if span.Attrs == nil {
		span.Attrs = Attrs{}
	}
	for k, v := range sanitized {
		span.Attrs[k] = v
	}
}

// MergeSpanAttrs 同 SetSpanAttrs，语义别名。
func MergeSpanAttrs(ctx context.Context, attrs Attrs) { SetSpanAttrs(ctx, attrs) }

// AppendSpanAttrs 给指定 span 追加/覆盖 attrs。
//
// 典型场景（ChatModelGenerate Lambda）：流式输出读完最后一个 chunk 后，
// span.EndAt 已被设置，但 root span 还没 finalizeFlush，用此函数补 reply_preview 等字段。
//
// 实现细节：
//   - s == nil 或 attrs == nil 时静默降级
//   - rec 传 RecorderFromContext(ctx)（可选，传 nil 时跳过 PII sanitize 但仍会写入 attrs）
func AppendSpanAttrs(s *Span, attrs Attrs, rec Recorder) {
	if s == nil || len(attrs) == 0 {
		return
	}
	sanitized := attrs
	if dr, ok := rec.(*defaultRecorder); ok && dr != nil && dr.sanitizer != nil {
		sanitized = dr.sanitizer.SanitizeAttrs(attrs)
	}
	// 写 OTel span
	if s.otelSpan != nil {
		s.otelSpan.SetAttributes(attrsToOTel(sanitized)...)
	}
	// 写落库 Span
	if s.Attrs == nil {
		s.Attrs = Attrs{}
	}
	for k, v := range sanitized {
		s.Attrs[k] = v
	}
}

// attrsToOTel 把项目自研 Attrs 转成 OTel attribute.KeyValue 列表。
// OTel 的 SetAttributes 只接受 KeyValue，所以需要做类型转换。
func attrsToOTel(attrs Attrs) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		kv, ok := toKeyValue(k, v)
		if !ok {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func toKeyValue(k string, v any) (attribute.KeyValue, bool) {
	switch val := v.(type) {
	case string:
		return attribute.String(k, val), true
	case int:
		return attribute.Int(k, val), true
	case int64:
		return attribute.Int64(k, val), true
	case float64:
		return attribute.Float64(k, val), true
	case float32:
		// eino 的 model.Config.Temperature / TopP 是 float32，不能落到下面的字符串兜底，
		// 否则 gen_ai.request.temperature 这类数值属性会以字符串写进 OTel，三方平台的数值解析会失败。
		return attribute.Float64(k, float64(val)), true
	case bool:
		return attribute.Bool(k, val), true
	case []string:
		return attribute.StringSlice(k, val), true
	case []int:
		return attribute.IntSlice(k, val), true
	case []int64:
		return attribute.Int64Slice(k, val), true
	case []float64:
		return attribute.Float64Slice(k, val), true
	case []bool:
		return attribute.BoolSlice(k, val), true
	default:
		// 其他类型（map / struct / nil）转字符串兜底
		return attribute.String(k, fmt.Sprintf("%v", v)), true
	}
}

// spanKindFor 把自研 Component 映射为 OTel SpanKind。
//
// 为什么必须显式指定：不指定时 SpanKind 恒为 Internal，而三方追踪平台靠它区分
// 「我作为服务端处理的请求」和「我发出去的调用」。全是 Internal 时，平台的上下游
// 关系图会把服务端入口和外部依赖混成一类，依赖箭头画不出来。
//
// 只映射语义没有歧义的两类，其余一律保持 Internal —— 宁可不分类，也不要给平台
// 一个错误的分类。注意 rag.reranker / embedding 等真实的出站调用不需要在这里映射，
// 它们由 HTTPTransport 自己建 SpanKindClient 的 span，本函数产出的是上层的逻辑 span。
func spanKindFor(component Component) trace.SpanKind {
	switch component {
	case ComponentHTTPServer:
		return trace.SpanKindServer
	case ComponentLLMClient:
		return trace.SpanKindClient
	default:
		return trace.SpanKindInternal
	}
}

func (r *defaultRecorder) StartSpan(ctx context.Context, name string, component Component, attrs Attrs) (context.Context, *Span) {
	if !r.enabled {
		s := &Span{Name: name, Component: component, StartAt: time.Now()}
		return context.WithValue(ctx, traceIDKey, ""), s
	}

	traceID := TraceIDFromContext(ctx)
	if traceID == "" {
		traceID = randomHex(16)
		ctx = context.WithValue(ctx, traceIDKey, traceID)
	}

	// 落库 parent-child：优先从入参 ctx 的 currentSpanKey 取 parent 自研 Span 引用。
	// 不能用 trace.SpanFromContext(ctx).IsRecording() 判断：eino 流式组件（如 adk Agent）的
	// OnEnd 会在输出流刚返回时就 End 掉 span，而该 span 仍作为 ctx 的 current 传给后续子组件，
	// IsRecording()=false 会让整棵子树找不到 parent 变成孤儿（深度模式 trace 断裂的根因）。
	// ctx 引用与 End 状态解耦后，已 End 的 span 仍是合法 parent。
	var parentSpan *Span
	if ps, ok := ctx.Value(currentSpanKey{}).(*Span); ok && ps != nil {
		parentSpan = ps
	}

	// OTel 侧属性单独算一份：自研轨用的下划线 user_id / session_id 只落在自研根 span 上，
	// 而自研合成的 chat.request 根 span 在 OTel 侧并不存在；三方平台（如 Langfuse）又是按
	// DOT 记法 user.id / session.id 做用户与会话分组的。所以在这里补一份别名。
	// 只补在根 span（无父）上：子 span 会从 ctx 继承 trace 级属性，无需重复携带。
	otelAttrs := r.sanitizer.SanitizeAttrs(attrs)
	if parentSpan == nil {
		if ra, ok := ctx.Value(rootAttrsKey).(*rootAttrs); ok && ra != nil {
			ra.mu.Lock()
			if ra.userID != "" {
				otelAttrs["user.id"] = ra.userID
			}
			if ra.sessionID != "" {
				otelAttrs["session.id"] = ra.sessionID
			}
			ra.mu.Unlock()
		}
	}

	// 用 OTel tracer.Start 创建运行时 span，OTel 自动管理 parent-child 关系。
	// 关键收益：运行时追踪的 parent-child 完全交给 OTel SDK，不管 Eino callback / compose.Graph /
	// InitCallbacks 包多少层 context.WithValue，OTel 的 trace.SpanFromContext(ctx) 永远能拿回当前 span。
	ctxWithSpan, otelSpan := r.tracer.Start(ctx, name,
		trace.WithSpanKind(spanKindFor(component)),
		trace.WithAttributes(attrsToOTel(otelAttrs)...))

	s := &Span{
		TraceID:   traceID,
		SpanID:    randomHex(8),
		Name:      name,
		Component: component,
		StartAt:   time.Now(),
		Status:    SpanStatusOK,
		Attrs:     r.sanitizer.SanitizeAttrs(attrs),
		otelSpan:  otelSpan,
		parent:    parentSpan,
	}

	// 双轨 traceID 对齐：把 OTel SDK 为这个 span 生成的 traceID / spanID 记到自研 Span 上。
	// 同一请求内所有 span 由 OTel 从 ctx 继承，共享同一个 OTel traceID；自研轨道则共享
	// 上面生成的随机 traceID。两者都记下来，落库后一条 trace 就能映射回三方平台。
	// SpanContext 无效（noop TracerProvider）时留空，不做任何伪造。
	if sc := otelSpan.SpanContext(); sc.IsValid() {
		s.OTelTraceID = sc.TraceID().String()
		s.OTelSpanID = sc.SpanID().String()
	}

	// parent_id 用于落库：从 parent Span 拿 SpanID（如果有的话）
	if parentSpan != nil {
		s.ParentID = parentSpan.SpanID
	}

	// 关联 OTel span 到自研 Span，CurrentSpanFromContext 兜底路径用
	spanByOtel.Store(spanKeyOf(otelSpan), s)

	// ctx 携带自研 Span 引用：子 span 的 parent 查找与 CurrentSpanFromContext 走这里，
	// 与 span 的 End 状态解耦（见上方 parent 查找注释）。
	ctxWithSpan = context.WithValue(ctxWithSpan, currentSpanKey{}, s)

	// 登记 traceState 的根 span。用 LoadOrStore 防止后到的孤儿 span 覆盖已登记的树。
	isChatRoot := ctx.Value(rootAttrsKey) != nil
	if parentSpan == nil {
		if isChatRoot {
			// chat 场景：Root 是 chat.deep/chat.quick 等中间根，publishTrace 时合并到合成 chat.request 下
			if s.Attrs == nil {
				s.Attrs = Attrs{}
			}
			s.Attrs["__chat_intermediate_root"] = true
		}
		r.traceStates.LoadOrStore(traceID, &traceState{Trace: &Trace{ID: traceID, Root: s, SampleRate: r.cfg.SamplingRate}})
	}

	r.metrics.obsSpanStartTotal.WithLabelValues(string(component)).Inc()
	return ctxWithSpan, s
}

func (r *defaultRecorder) AddEvent(ctx context.Context, span *Span, name string, attrs Attrs) {
	if span == nil {
		return
	}
	e := &SpanEvent{Name: name, Timestamp: time.Now(), Attrs: r.sanitizer.SanitizeAttrs(attrs)}
	span.Events = append(span.Events, e)
	// 同步给 OTel span（运行时追踪用）
	if span.otelSpan != nil {
		span.otelSpan.AddEvent(name, trace.WithAttributes(attrsToOTel(e.Attrs)...))
	}
}

// containsSpan 判断 children 里是否已经挂过同一个 span。
// 用指针相等判断：span 在树里是唯一节点，同一个指针的第二次挂载必然是重复 EndSpan，
// 而不是「两个同名兄弟」（那种情况指针不同，仍会被正常挂上去）。
func containsSpan(children []*Span, target *Span) bool {
	for _, c := range children {
		if c == target {
			return true
		}
	}
	return false
}

func (r *defaultRecorder) EndSpan(ctx context.Context, span *Span, status SpanStatus, err error, attrs Attrs) {
	if span == nil {
		return
	}
	span.EndAt = time.Now()
	span.DurationMs = span.EndAt.Sub(span.StartAt).Milliseconds()
	span.Status = status
	if err != nil {
		span.Error = r.sanitizer.SanitizeString(err.Error())
	}
	if len(attrs) > 0 {
		sanitized := r.sanitizer.SanitizeAttrs(attrs)
		if span.Attrs == nil {
			span.Attrs = Attrs{}
		}
		for k, v := range sanitized {
			span.Attrs[k] = v
		}
		// 同步写 OTel span
		if span.otelSpan != nil {
			span.otelSpan.SetAttributes(attrsToOTel(sanitized)...)
		}
	}

	// 关闭 OTel span：设置 status + 记录 error + End
	if span.otelSpan != nil {
		switch status {
		case SpanStatusError:
			span.otelSpan.SetStatus(codes.Error, span.Error)
			if err != nil {
				span.otelSpan.RecordError(err)
			}
		case SpanStatusCanceled:
			span.otelSpan.SetStatus(codes.Error, "canceled")
		case SpanStatusOK:
			span.otelSpan.SetStatus(codes.Ok, "")
		}
		span.otelSpan.End(trace.WithTimestamp(span.EndAt))
		// 清理 spanByOtel 关联，避免内存泄漏
		spanByOtel.Delete(spanKeyOf(span.otelSpan))
	}

	// 落库 parent-child：用 StartSpan 时存的 parent 引用挂接 children。
	// 不依赖 trace.SpanFromContext(ctx)：ctx 可能是 eino_callback 透传的 ctxWithSpan，
	// SpanFromContext 拿回的是当前 span 自己，parent != span 永远失败。
	//
	// 挂树全程持 traceState.traceMu：后台 span（DetachedTraceContext 派生）会在主 trace
	// 已经落库之后才 End，与「重发时克隆树」并发，不加锁就是对 Children slice 的并发写。
	st := r.loadTraceState(span.TraceID)
	if st != nil {
		st.traceMu.Lock()
	}
	if span.parent != nil {
		if span.parent.Children == nil {
			span.parent.Children = []*Span{}
		}
		// 幂等守卫：一个 span 在树里只能是「一个」节点。
		// EndSpan 不是「只会被调用一次」的：业务侧普遍是「显式 End + defer 兜底 End」，
		// eino 侧同一个 state.span 也可能被 OnEnd / OnError / OnEndWithStreamOutput 多条路径命中。
		// 直接 append 会把同一个 *Span 指针挂进 Children 多次 —— 落库后 span_tree 里同一个
		// span_id 在同一个父节点下出现多份（深度模式实测 ×3），前端 trace 详情重复显示节点、
		// 节点总数与 OTel 侧对不上，且各处「按 span 去重」的分析/统计都会被算重。
		if !containsSpan(span.parent.Children, span) {
			span.parent.Children = append(span.parent.Children, span)
			if st != nil {
				// 只有树真的变了才记代次：重发写完靠它判断有没有漏
				st.treeGen++
			}
		}
	}
	if st != nil {
		st.traceMu.Unlock()
	}

	// 根 span 结束时触发 finalizeTrace；chat 中间 root span 跳过
	if span.TraceID != "" {
		isIntermediate := false
		if span.Attrs != nil {
			if v, ok := span.Attrs["__chat_intermediate_root"].(bool); ok && v {
				isIntermediate = true
			}
		}
		if !isIntermediate && st != nil && st.Trace != nil {
			switch {
			case st.Trace.Root == span:
				// 已发布过就不再走 finalizeTrace：那会用「直接根」形态把同一行再写一遍，
				// 把 publishTrace 合成的 chat.request 树和 rootAttrs 里的归属信息覆盖掉
				// （WriteTraces 是 upsert + UpdateAll）。真实链路里这正是 gin 中间件的
				// http.request 根 span —— 它在 SSE 收尾、FlushTrace 之后才 End。
				// 它带的新子树早已由各自的 EndSpan 触发过重发，这里无需再做任何事。
				if !r.traceAlreadyPublished(st) {
					r.finalizeTrace(ctx, span, err)
				}
			case r.traceAlreadyPublished(st):
				// 迟到 span：所属 trace 已经落库（典型是 DetachedTraceContext 派生的后台任务）。
				// 不再为它新开一行 chat_traces，而是防抖重发、并回同一行。
				r.scheduleRepublish(span.TraceID, st)
			}
		}
	}

	r.metrics.obsSpanDurationSec.WithLabelValues(string(span.Component), string(span.Status)).Observe(float64(span.DurationMs) / 1000.0)
}

// finalizeTrace 在「非 chat 的自研根 span」结束时落库（chat 场景走 publishTrace）。
//
// 与 publishTrace 的唯一区别是发布根的形态：这里 root 自己就是发布根，
// 而 chat 场景要在合成 chat.request 下面再挂一层中间根（见 traceState.synthetic）。
//
// 落库后**不销毁** traceState，改为进入保活窗口：请求结束后才 End 的后台 span
// 依然能把子树并回同一行（见 tracePublishGraceWindow）。
func (r *defaultRecorder) finalizeTrace(ctx context.Context, root *Span, endErr error) {
	if root == nil || root.TraceID == "" {
		return
	}
	traceID := root.TraceID
	snap := &rootAttrsSnapshot{
		beginAt: root.StartAt,
		endAt:   root.EndAt,
		endErr:  endErr,
	}
	if root.Status == SpanStatusError || root.Status == SpanStatusCanceled {
		snap.endStatus = root.Status
	} else {
		snap.endStatus = SpanStatusOK
	}
	// 兜底到 attrs：非 chat 路径没有 rootAttrs，归属信息只能从根 span 的 attrs 上取
	if root.Attrs != nil {
		if v, ok := root.Attrs["user_id"].(string); ok {
			snap.userID = v
		}
		if v, ok := root.Attrs["session_id"].(string); ok {
			snap.sessionID = v
		}
		if v, ok := root.Attrs["request_id"].(string); ok {
			snap.requestID = v
		}
	}

	st := r.ensureTraceState(traceID)
	st.mu.Lock()
	st.snap = snap
	st.synthetic = false
	st.published = true
	st.graceUntil = time.Now().Add(tracePublishGraceWindow)
	st.mu.Unlock()

	r.flushTraceState(ctx, traceID, st, false)
	r.armTraceGC(traceID)
}

// ensureTraceState 取（必要时新建）traceID 对应的 traceState。
//
// 允许存在「没有登记过根 span 的空状态」：FlushTrace 可能对一条没有自研根 span 的 trace
// 调用（例如只有 HTTP 层 span 的请求），这种情况同样要能落库。
func (r *defaultRecorder) ensureTraceState(traceID string) *traceState {
	if v, ok := r.traceStates.Load(traceID); ok {
		if st, ok := v.(*traceState); ok && st != nil {
			return st
		}
	}
	fresh := &traceState{Trace: &Trace{ID: traceID, SampleRate: r.cfg.SamplingRate}}
	actual, _ := r.traceStates.LoadOrStore(traceID, fresh)
	if st, ok := actual.(*traceState); ok && st != nil {
		return st
	}
	return fresh
}

// loadTraceState 取 traceID 的状态，没有则返回 nil（调用方静默降级）。
func (r *defaultRecorder) loadTraceState(traceID string) *traceState {
	if traceID == "" {
		return nil
	}
	v, ok := r.traceStates.Load(traceID)
	if !ok {
		return nil
	}
	st, _ := v.(*traceState)
	return st
}

// traceAlreadyPublished 表示这条 trace 已经进入落库 + 保活阶段。
//
// 判据用 snap != nil 而不是 sampled：首发置位 published 早于写库完成，
// 写库期间结束的后台 span 也必须被判为「迟到」并触发重发，否则会被静默丢掉。
func (r *defaultRecorder) traceAlreadyPublished(st *traceState) bool {
	if st == nil {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.published && st.snap != nil
}

// scheduleRepublish 排一次防抖重发：把 trace 的当前树重新 upsert 到同一行。
//
// 防抖的意义：会话摘要 + 记忆抽取两个后台 span 通常前后脚结束，不防抖会对同一行写两次。
// 每次调用同时把保活窗口往后推，避免后台任务还在跑、状态就被 GC 回收。
func (r *defaultRecorder) scheduleRepublish(traceID string, st *traceState) {
	if st == nil {
		return
	}
	st.mu.Lock()
	if !st.published || st.snap == nil {
		// 还没发布过（快照都没建），首发自会带上最新的树，无需重发
		st.mu.Unlock()
		return
	}
	st.graceUntil = time.Now().Add(tracePublishGraceWindow)
	if st.republish != nil {
		st.republish.Stop()
	}
	st.republish = time.AfterFunc(traceRepublishDebounce, func() {
		st.mu.Lock()
		st.republish = nil
		st.mu.Unlock()
		r.republishTrace(traceID)
	})
	st.mu.Unlock()
	r.armTraceGC(traceID)
}

// republishTrace 把保活中的 trace 再写一次（upsert 覆盖同一行，不会新增行）。
//
// ctx 用 Background：请求 ctx 早已被回收。OTel traceID 不依赖 ctx ——
// 它保存在保活的自研根 span 上（见 oTelTraceIDFor 的第一条查找路径）。
func (r *defaultRecorder) republishTrace(traceID string) {
	st := r.loadTraceState(traceID)
	if st == nil {
		return
	}
	st.mu.Lock()
	ready := st.published && st.snap != nil
	st.mu.Unlock()
	if !ready {
		return
	}
	r.flushTraceState(context.Background(), traceID, st, true)
	r.armTraceGC(traceID)
}

// armTraceGC 安排一次状态回收。每次发布与每个迟到 span 都会续期，
// 所以实际回收时刻跟随 graceUntil（滑动窗口），而不是「首次发布 + 固定时长」。
func (r *defaultRecorder) armTraceGC(traceID string) {
	st := r.loadTraceState(traceID)
	if st == nil {
		return
	}
	st.mu.Lock()
	until := st.graceUntil
	st.mu.Unlock()
	if until.IsZero() {
		// 没设过保活窗口就不排 GC：否则 time.Until(零值) 是极大负数，会立刻误删状态
		return
	}
	d := time.Until(until)
	if d < 0 {
		d = 0
	}
	time.AfterFunc(d, func() { r.releaseTraceState(traceID) })
}

// releaseTraceState 保活到期后释放 traceState 与采样决定，避免内存无界增长。
//
// 必须先确认 graceUntil 没有被迟到 span 续期：抢先回收会把还在用的状态删掉。
// 被续期时直接返回即可 —— 续期那次调用已经排了新的 GC。
func (r *defaultRecorder) releaseTraceState(traceID string) {
	st := r.loadTraceState(traceID)
	if st == nil {
		return
	}
	st.mu.Lock()
	if time.Now().Before(st.graceUntil) {
		st.mu.Unlock()
		return
	}
	if st.republish != nil {
		st.republish.Stop()
		st.republish = nil
	}
	st.mu.Unlock()

	r.traceStates.Delete(traceID)
	r.traceDecide.Delete(traceID)
}

// buildSyntheticRoot 构造落库用的 chat.request 根 span。
//
// 自研轨登记的是 chat.deep / chat.quick 这类中间根（真实走过 StartSpan、带 OTel span），
// 而前端列表 / 详情需要一个统一的会话级根节点，所以落库时合成一个 chat.request，
// 再把中间根挂成它的子节点。
//
// SpanID 直接复用 traceID：合成的根没有真实 OTel span，用 traceID 占位既保证 SpanID 非空
// （前端树渲染需要），又天然与行主键一致，排查时一眼能对上。OTelSpanID 保持为空，
// 不伪造一个并不存在的 span。
func (r *defaultRecorder) buildSyntheticRoot(traceID string, snap *rootAttrsSnapshot) *Span {
	beginAt := snap.beginAt
	endAt := snap.endAt
	if beginAt.IsZero() {
		beginAt = time.Now()
	}
	if endAt.IsZero() {
		endAt = time.Now()
	}
	status := snap.endStatus
	if status == "" {
		if snap.endErr != nil {
			status = SpanStatusError
		} else {
			status = SpanStatusOK
		}
	}
	attrs := Attrs{}
	for k, v := range snap.attrs {
		attrs[k] = v
	}
	if snap.userID != "" {
		attrs["user_id"] = snap.userID
	}
	if snap.sessionID != "" {
		attrs["session_id"] = snap.sessionID
	}
	if snap.messageID != "" {
		attrs["message_id"] = snap.messageID
	}
	if snap.requestID != "" {
		attrs["request_id"] = snap.requestID
	}
	if snap.searchMode != "" {
		attrs["search_mode"] = snap.searchMode
	}
	if snap.modelID != "" {
		attrs["model_id"] = snap.modelID
	}
	root := &Span{
		TraceID:    traceID,
		SpanID:     traceID,
		Name:       "chat.request",
		Component:  ComponentServiceChat,
		StartAt:    beginAt,
		EndAt:      endAt,
		DurationMs: endAt.Sub(beginAt).Milliseconds(),
		Status:     status,
		Attrs:      r.sanitizer.SanitizeAttrs(attrs),
	}
	if snap.endErr != nil {
		root.Error = r.sanitizer.SanitizeString(snap.endErr.Error())
	}
	return root
}

// Incr 是业务代码统一 metric 入口，内部代理到 Prometheus CounterVec。
func (r *defaultRecorder) Incr(ctx context.Context, metric string, labels map[string]string, delta int64) {
	if !r.enabled {
		return
	}
	incrProm(r.metrics, metric, labels, delta)
}

// Observe 是业务代码统一 histogram 入口，内部代理到 Prometheus HistogramVec。
func (r *defaultRecorder) Observe(ctx context.Context, metric string, labels map[string]string, value float64) {
	if !r.enabled {
		return
	}
	observeProm(r.metrics, metric, labels, value)
}

func (r *defaultRecorder) RecordTrace(trace *Trace) {
	if trace == nil || !r.enabled {
		return
	}
	rec := &SinkRecord{Kind: "trace", Timestamp: time.Now(), Trace: trace}
	if err := r.sinks.Write(context.Background(), rec); err != nil {
		logger.Warnf("RecordTrace: %v", err)
	}
	if r.dbSink != nil && r.cfg.TraceTableEnabled && trace.Sampled {
		// 落库前统一脱敏：DB 出口与 LogSink 是两条独立路径，DB 不会经过 BatchSink 扇出，
		// 只能在这里自己清洁。sanitizeTrace 同时深拷贝整棵树 —— 落库仓储会就地把树改写成
		// 可落库形态（stripInternalSpanAttrs），与日志侧的并发序列化共享同一棵树是隐患。
		if err := r.dbSink.WriteTraces(context.Background(), []*Trace{sanitizeTrace(trace, r.sanitizer)}); err != nil {
			r.metrics.obsDBSinkErrorsTotal.WithLabelValues("trace").Inc()
		}
	}
}

func (r *defaultRecorder) RecordFeedback(fb *Feedback) {
	if fb == nil || !r.enabled {
		return
	}
	if fb.CreatedAt.IsZero() {
		fb.CreatedAt = time.Now()
	}
	if fb.TraceID != "" {
		r.traceDecide.Store(fb.TraceID, SampleDecisionForceKeep)
	}
	rec := &SinkRecord{Kind: "feedback", Timestamp: fb.CreatedAt, Feedback: fb}
	if err := r.sinks.Write(context.Background(), rec); err != nil {
		logger.Warnf("RecordFeedback: %v", err)
	}
	if r.dbSink != nil {
		// 用户填写的反馈评论是自由文本，最可能带邮箱 / 手机号；DB 出口必须自己脱敏。
		if err := r.dbSink.WriteFeedbacks(context.Background(), []*Feedback{sanitizeFeedback(fb, r.sanitizer)}); err != nil {
			r.metrics.obsDBSinkErrorsTotal.WithLabelValues("feedback").Inc()
		}
	}
}

func (r *defaultRecorder) RecordAgentStep(step *AgentStep) {
	if step == nil || !r.enabled {
		return
	}
	rec := &SinkRecord{Kind: "agent_step", Timestamp: time.Now(), AgentStep: step}
	if err := r.sinks.Write(context.Background(), rec); err != nil {
		logger.Warnf("RecordAgentStep: %v", err)
	}
	if r.dbSink != nil {
		// 思考摘要 / 工具入参 / 工具结果 / 工具报错都可能回显用户原文或凭证，DB 出口必须自己脱敏。
		if err := r.dbSink.WriteAgentSteps(context.Background(), []*AgentStep{sanitizeAgentStep(step, r.sanitizer)}); err != nil {
			r.metrics.obsDBSinkErrorsTotal.WithLabelValues("agent_step").Inc()
		}
	}
}

// MetricsSnapshot 返回 Prometheus 指标快照（JSON 格式）。
// 用于 GET /api/v1/chat/metrics 管理端接口（非 /metrics 标准抓取）。
func (r *defaultRecorder) MetricsSnapshot() (map[string]any, error) {
	if !r.enabled {
		return nil, errors.New("observability disabled")
	}
	return prometheusSnapshot(GlobalPromRegistry()), nil
}

func (r *defaultRecorder) Config() config.ObservabilityConfig { return r.cfg }

func (r *defaultRecorder) SamplingRate() float64 { return r.cfg.SamplingRate }

func (r *defaultRecorder) Shutdown(ctx context.Context) error {
	if r.sinks != nil {
		return r.sinks.Shutdown(ctx)
	}
	return nil
}

func (r *defaultRecorder) WithTraceRoot(ctx context.Context, attrs TraceRootAttrs) context.Context {
	ctx = context.WithValue(ctx, recorderKey, r)
	traceID := TraceIDFromContext(ctx)
	if traceID == "" {
		traceID = randomHex(16)
		ctx = context.WithValue(ctx, traceIDKey, traceID)
	}
	ra := &rootAttrs{
		attrs: Attrs{
			"user_id":     attrs.UserID,
			"session_id":  attrs.SessionID,
			"message_id":  attrs.MessageID,
			"request_id":  attrs.RequestID,
			"search_mode": attrs.SearchMode,
			"model_id":    attrs.ModelID,
		},
		beginAt:    time.Now(),
		userID:     attrs.UserID,
		sessionID:  attrs.SessionID,
		messageID:  attrs.MessageID,
		requestID:  attrs.RequestID,
		searchMode: attrs.SearchMode,
		modelID:    attrs.ModelID,
	}

	// 三方平台（如 Langfuse）按 DOT 记法 user.id / session.id 做用户与会话归组，
	// 而自研轨用的是下划线 user_id / session_id，会被平台当「未映射属性」丢进 metadata。
	// 这里是「用户 + 会话同时可知」的唯一位置：HTTP 中间件建根 span 时只拿得到 user_id，
	// 还没有 session_id（session 是本次请求体的参数）。
	// 此刻 ctx 里的当前 OTel span 就是整条 trace 的根（http.request），把 trace 级属性
	// 打在根 span 上最符合平台的归组语义。没有 OTel span（noop provider）时静默跳过，
	// 不产生任何伪造数据。
	if otelSpan := trace.SpanFromContext(ctx); otelSpan.SpanContext().IsValid() {
		kv := make([]attribute.KeyValue, 0, 2)
		if attrs.UserID != "" {
			kv = append(kv, attribute.String("user.id", attrs.UserID))
		}
		if attrs.SessionID != "" {
			kv = append(kv, attribute.String("session.id", attrs.SessionID))
		}
		if len(kv) > 0 {
			otelSpan.SetAttributes(kv...)
		}
	}
	return context.WithValue(ctx, rootAttrsKey, ra)
}

func (r *defaultRecorder) AddRootAttrs(ctx context.Context, attrs Attrs) {
	if v := ctx.Value(rootAttrsKey); v != nil {
		if ra, ok := v.(*rootAttrs); ok {
			ra.mu.Lock()
			if ra.attrs == nil {
				ra.attrs = Attrs{}
			}
			for k, val := range r.sanitizer.SanitizeAttrs(attrs) {
				ra.attrs[k] = val
			}
			ra.mu.Unlock()
		}
	}
}

// MarkTraceError 将当前 trace 的根 span 标记为错误状态。
// processMessage / processDeepMode 是 void 方法，内部错误通过事件流发送后无法传递给 FlushTrace。
// 在错误路径调用此方法，写入 rootAttrs.endErr + endStatus，publishTrace 时会据此设置根 span 状态。
func (r *defaultRecorder) MarkTraceError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	v := ctx.Value(rootAttrsKey)
	if v == nil {
		return
	}
	ra, ok := v.(*rootAttrs)
	if !ok {
		return
	}
	ra.mu.Lock()
	ra.endErr = err
	ra.endStatus = SpanStatusError
	ra.mu.Unlock()
}

// PreviewAttr 暴露给 eino_callback 等业务方：先做 PII mask，再按 rune 数截断，
// 同时附带"被截断了多少字符"的尾标，方便前端一眼判断是否有更多内容。
func (r *defaultRecorder) PreviewAttr(text string, maxRunes int) string {
	if r == nil {
		return defaultSanitizerSingleton().TruncatePreview(text, maxRunes)
	}
	return r.sanitizer.TruncatePreview(text, maxRunes)
}

func defaultSanitizerSingleton() *PIISanitizer {
	return NewPIISanitizer(2000, true)
}

func (r *defaultRecorder) ForceSampling(ctx context.Context) {
	traceID := TraceIDFromContext(ctx)
	if traceID == "" {
		return
	}
	r.traceDecide.Store(traceID, SampleDecisionForceKeep)
}

func (r *defaultRecorder) FlushTrace(ctx context.Context, userID, sessionID, messageID string) string {
	if !r.enabled {
		return ""
	}
	traceID := TraceIDFromContext(ctx)
	if traceID == "" {
		return ""
	}
	var ra *rootAttrs
	if v := ctx.Value(rootAttrsKey); v != nil {
		ra, _ = v.(*rootAttrs)
	}
	if ra != nil {
		ra.mu.Lock()
		if ra.userID == "" {
			ra.userID = userID
		}
		if ra.sessionID == "" {
			ra.sessionID = sessionID
		}
		if ra.messageID == "" {
			ra.messageID = messageID
		}
		ra.endAt = time.Now()
		ra.mu.Unlock()
	} else {
		ra = &rootAttrs{
			beginAt:   time.Now(),
			endAt:     time.Now(),
			userID:    userID,
			sessionID: sessionID,
			messageID: messageID,
			endStatus: SpanStatusOK,
		}
	}
	r.publishTrace(ctx, traceID, ra)
	return traceID
}

// publishTrace 在请求收尾（FlushTrace）时把 chat trace 落库，并进入保活窗口。
//
// 保活的必要性：会话摘要 / 记忆抽取是响应返回之后才跑的后台任务，它们的 span 会在
// 本函数已经写完库之后才 End。旧实现在这里 LoadAndDelete 掉状态，迟到 span 无处可归，
// 只能另写一行 chat_traces（session=unknown 的孤儿 trace）。现在改为保活 + 迟到重发。
func (r *defaultRecorder) publishTrace(ctx context.Context, traceID string, ra *rootAttrs) {
	st := r.ensureTraceState(traceID)
	snap := snapshotRootAttrs(ra)
	st.mu.Lock()
	st.snap = snap
	st.synthetic = true
	// 早于写库置位：写库期间（最长 10s）结束的后台 span 也必须是「迟到」，
	// 否则它既没被本次克隆抓到、又不会触发重发，就被静默丢了。
	st.published = true
	st.graceUntil = time.Now().Add(tracePublishGraceWindow)
	st.mu.Unlock()

	r.flushTraceState(ctx, traceID, st, false)
	r.armTraceGC(traceID)
}

// flushTraceState 把 traceState 里当前的 span 树写成一条 chat_traces 记录。
//
//	republish=false —— 首发：算采样决定与双轨对齐结果、写 sink、记指标
//	republish=true  —— 迟到 span 触发的重发：只做 DB upsert（覆盖同一行），
//	                   不重复写 sink / 记指标，也不重掷采样、不重算双轨对齐结果
//	                   （见 traceState.sampled 与 traceState.otelExported）
//
// 三点并发约束：
//  1. flushMu 串行化首发与重发，避免乱序写入让更旧的树覆盖更新的树；
//  2. traceMu 下克隆整棵树，之后序列化只碰私有副本，后台 span 继续挂树不受影响；
//  3. 写完再对比 treeGen，写库期间有新节点挂上就再排一轮重发，保证不漏。
func (r *defaultRecorder) flushTraceState(ctx context.Context, traceID string, st *traceState, republish bool) {
	if st == nil {
		return
	}
	st.flushMu.Lock()
	defer st.flushMu.Unlock()

	st.mu.Lock()
	snap := st.snap
	synthetic := st.synthetic
	prevSampled := st.sampled
	prevOTelTraceID := st.otelTraceID
	prevOTelExported := st.otelExported
	st.mu.Unlock()
	if snap == nil {
		snap = &rootAttrsSnapshot{}
	}

	// hasOwner 判断这条 trace 是否承载了业务归属（挂在某轮问答的 session / message 上），
	// 有归属即必留（见 SampleRequest.Required）。
	//
	// 判据取自 snap（首发时对 rootAttrs 的快照），不能用下面那几个局部变量：
	// 局部 sessionID / userID 到这里很快会被兜底成 "unknown"，拿它们判断会让健康检查、
	// 列表轮询这类无归属 trace 也全部变成「必留」，采样率彻底失效。
	// 归属的写入点只有 WithTraceRoot / FlushTrace 的入参，不存在第二个来源。
	hasOwner := snap.sessionID != "" || snap.messageID != ""

	st.traceMu.Lock()
	var registeredRoot *Span
	if st.Trace != nil {
		registeredRoot = cloneSpan(st.Trace.Root)
	}
	treeGen := st.treeGen
	st.traceMu.Unlock()

	var root *Span
	if synthetic {
		// chat 场景：合成 chat.request 作发布根，自研轨登记的中间根挂到它下面
		root = r.buildSyntheticRoot(traceID, snap)
		mergeChildRoot(root, registeredRoot)
	} else {
		// 非 chat 场景：登记根自己就是发布根，无需再套一层
		if registeredRoot == nil {
			return
		}
		root = registeredRoot
	}

	// 树是唯一真相：父子关系与发布根的时间区间都从树形状派生，不再依赖各 span 创建时
	// 各自记下的字段 —— 那两个来源在 chat 场景必然打架（见 stampParentIDs 注释）。
	stampParentIDs(root)
	envelopeTreeIntervals(root)

	// 双轨对齐：OTel traceID 从保活的自研根上回捞（重发时 ctx 已经没有了），
	// 拿不到再退回 ctx 里的 OTel span。
	// 双轨对齐：OTel traceID 从保活的自研根上回捞（重发时 ctx 已经没有了），
	// 拿不到再退回 ctx 里的 OTel span。
	//
	// 只首发计算、重发复用（见 traceState.otelExported）：重发的 ctx 是脱离请求的，
	// 现算必然得到空值 / false，而写库是 upsert，会把整行覆盖回去。
	var otelTraceID string
	var otelExported bool
	if republish {
		otelTraceID = prevOTelTraceID
		otelExported = prevOTelExported
	} else {
		otelTraceID = oTelTraceIDFor(ctx, registeredRoot)
		otelExported = oTelExportedFor(ctx, registeredRoot)
	}
	root.OTelTraceID = otelTraceID

	attrs := root.Attrs
	userID := snap.userID
	sessionID := snap.sessionID
	requestID := snap.requestID
	if userID == "" && attrs != nil {
		if v, ok := attrs["user_id"].(string); ok {
			userID = v
		}
	}
	if sessionID == "" && attrs != nil {
		if v, ok := attrs["session_id"].(string); ok {
			sessionID = v
		}
	}
	// 兜底：空值统一填 unknown，避免数据库字段为空导致前端列表筛选 / 详情查询失败
	if userID == "" {
		userID = "unknown"
	}
	if sessionID == "" {
		sessionID = "unknown"
	}

	dur := time.Duration(root.DurationMs) * time.Millisecond
	hasErr := snap.endErr != nil || snap.endStatus == SpanStatusError || snap.endStatus == SpanStatusCanceled ||
		root.Status == SpanStatusError || root.Status == SpanStatusCanceled

	var sampled bool
	if republish {
		// 复用首发决定。rollSample 是随机的，重算可能把已落库的行「重发成不写」。
		sampled = prevSampled
	} else {
		rawDecision, _ := r.traceDecide.Load(traceID)
		var decision SampleDecision
		if rawDecision != nil {
			decision, _ = rawDecision.(SampleDecision)
		}
		// 这里刻意用 Load 而不是 LoadAndDelete：保活期内可能还有迟到 span 触发重发，
		// 删掉会让「用户点赞强制保留」（RecordFeedback / ForceSampling）在重发时失效。
		// 真正的清理交给保活到期后的 releaseTraceState。
		sampled = r.sampler.ShouldSample(SampleRequest{
			TraceID:  traceID,
			UserID:   userID,
			HasErr:   hasErr,
			Duration: dur,
			// 反馈必然发生在响应落库之后，首发时不可能有；用户的点赞走 traceDecide 里的
			// ForceKeep，经上面的 decision 生效。
			HasFeedback: false,
			Decision:    decision,
			Required:    hasOwner,
		})
	}
	st.mu.Lock()
	st.sampled = sampled
	st.otelTraceID = otelTraceID
	st.otelExported = otelExported
	st.published = true
	st.mu.Unlock()

	t := &Trace{
		ID:           traceID,
		RequestID:    requestID,
		UserID:       userID,
		SessionID:    sessionID,
		Root:         root,
		SampleRate:   r.cfg.SamplingRate,
		Sampled:      sampled,
		OTelTraceID:  otelTraceID,
		OTelExported: otelExported,
	}

	// 写库用独立 context，避免 HTTP 请求结束后 ctx 被取消导致写库失败
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer writeCancel()

	if !republish {
		rec := &SinkRecord{Kind: "trace", Timestamp: root.EndAt, Trace: t}
		if e := r.sinks.Write(writeCtx, rec); e != nil {
			logger.Warnf("trace 写入 sink 失败: %v", e)
		}
	}
	if sampled && r.dbSink != nil && r.cfg.TraceTableEnabled {
		// 与 RecordTrace 同理：DB 出口不经 BatchSink，需自行脱敏 + 深拷贝，
		// 避免落库仓储的就地改写（stripInternalSpanAttrs）与日志侧的序列化互相干扰。
		if err := r.dbSink.WriteTraces(writeCtx, []*Trace{sanitizeTrace(t, r.sanitizer)}); err != nil {
			if republish {
				logger.Warnf("迟到 span 重发写库失败: %v", err)
			} else {
				logger.Warnf("trace 写库失败: %v", err)
			}
			r.metrics.obsDBSinkErrorsTotal.WithLabelValues("trace").Inc()
		}
	} else if !republish && !sampled {
		r.metrics.obsTraceNotSampled.Inc()
	}

	if !republish {
		searchMode := snap.searchMode
		r.metrics.obsTraceFlushTotal.WithLabelValues(
			boolLabelO(sampled),
			searchModeOrDefault(searchMode),
		).Inc()
		r.metrics.obsTraceDurationSec.WithLabelValues(
			searchModeOrDefault(searchMode),
			string(root.Status),
		).Observe(dur.Seconds())
	}

	// 写库期间树又长了（后台 span 恰好在这几百毫秒里 End）→ 再排一轮重发。
	// 首发也一样要排：否则这个 span 既没进本次克隆，也没人再写第二次。
	st.traceMu.Lock()
	changed := st.treeGen != treeGen
	st.traceMu.Unlock()
	if changed {
		r.scheduleRepublish(traceID, st)
	}
}

func boolLabelO(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func searchModeOrDefault(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

var (
	_ = context.Background
	_ = errors.New
)

// 保证编译期 defaultRecorder 实现 Recorder 接口
var _ Recorder = (*defaultRecorder)(nil)

// 兼容：metric_dispatcher.go 内部调用，从 metric name 找到对应的 prometheus 指标。
// 如果 metric 名不在已注册列表里，静默丢弃（避免 panic）。
func incrProm(m *promMetrics, name string, labels map[string]string, delta int64) {
	if m == nil {
		return
	}
	labelVals := labelMapToValues(labels)
	switch name {
	case "obs_span_start_total":
		m.obsSpanStartTotal.WithLabelValues(labelVals["component"]).Add(float64(delta))
	case "obs_db_sink_errors_total":
		m.obsDBSinkErrorsTotal.WithLabelValues(labelVals["type"]).Add(float64(delta))
	case "obs_trace_not_sampled_total":
		m.obsTraceNotSampled.Add(float64(delta))
	case "obs_trace_flush_total":
		m.obsTraceFlushTotal.WithLabelValues(labelVals["sampled"], labelVals["search_mode"]).Inc()
	case "http_request_total":
		m.HTTPRequestTotal.WithLabelValues(labelVals["method"], labelVals["route"], labelVals["status_group"]).Inc()
	case "http_error_total":
		m.HTTPErrorTotal.WithLabelValues(labelVals["method"], labelVals["route"], labelVals["status_group"]).Inc()
	case "http_panic_total":
		m.HTTPanicTotal.WithLabelValues(labelVals["method"], labelVals["route"], labelVals["type"]).Inc()
	case "eino_llm_requests_total":
		m.EinoLLMRequestsTotal.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Inc()
	case "eino_llm_stream_requests_total":
		m.EinoLLMStreamRequestsTotal.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Inc()
	case "eino_llm_errors_total":
		m.EinoLLMErrorsTotal.WithLabelValues(labelVals["component"], labelVals["name"]).Inc()
	case "eino_retriever_requests_total":
		m.EinoRetrieverRequestsTotal.WithLabelValues(labelVals["component"], labelVals["name"]).Inc()
	case "eino_retriever_errors_total":
		m.EinoRetrieverErrorsTotal.WithLabelValues(labelVals["component"], labelVals["name"]).Inc()
	case "eino_tool_calls_total":
		m.EinoToolCallsTotal.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["tool_name"]).Inc()
	case "eino_tool_errors_total":
		m.EinoToolErrorsTotal.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["tool_name"]).Inc()
	case "agent_tool_calls_total":
		m.AgentToolCallsTotal.WithLabelValues(labelVals["status"], labelVals["tool"]).Inc()
	case "eino_embed_requests_total":
		m.EinoEmbedRequestsTotal.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Inc()
	case "eino_embed_errors_total":
		m.EinoEmbedErrorsTotal.WithLabelValues(labelVals["component"], labelVals["name"]).Inc()
	case "eino_agent_runs_total":
		m.EinoAgentRunsTotal.WithLabelValues(labelVals["component"], labelVals["name"]).Inc()
	case "eino_agent_errors_total":
		m.EinoAgentErrorsTotal.WithLabelValues(labelVals["component"], labelVals["name"]).Inc()
	case "eino_stream_end_total":
		m.EinoStreamEndTotal.WithLabelValues(labelVals["component"], labelVals["name"]).Inc()
	case "chat_feedback_total":
		m.ChatFeedbackTotal.WithLabelValues(labelVals["rating"], labelVals["reason_tag"]).Inc()
	case "chat_deep_requests_total":
		m.ChatDeepRequestsTotal.WithLabelValues(labelVals["model_id"]).Inc()
	case "chat_deep_errors_total":
		m.ChatDeepErrorsTotal.WithLabelValues(labelVals["stage"]).Inc()
	case "ctx_summary_errors_total":
		m.CtxSummaryErrorsTotal.Add(float64(delta))
	case "ctx_summary_updates_total":
		m.CtxSummaryUpdatesTotal.Add(float64(delta))
	case "ctx_memory_errors_total":
		m.CtxMemoryErrorsTotal.Add(float64(delta))
	case "ctx_memory_extracted_total":
		m.CtxMemoryExtractedTotal.Add(float64(delta))
	case "agent_engine_runs_total":
		m.AgentEngineRunsTotal.Add(float64(delta))
	default:
		// 未注册的 metric 名，静默丢弃（避免 panic 影响业务流程）
	}
}

func observeProm(m *promMetrics, name string, labels map[string]string, value float64) {
	if m == nil {
		return
	}
	labelVals := labelMapToValues(labels)
	switch name {
	case "obs_span_duration_seconds":
		m.obsSpanDurationSec.WithLabelValues(labelVals["component"], labelVals["status"]).Observe(value)
	case "obs_trace_duration_seconds":
		m.obsTraceDurationSec.WithLabelValues(labelVals["search_mode"], labelVals["status"]).Observe(value)
	case "http_request_duration_seconds":
		m.HTTPRequestDuration.WithLabelValues(labelVals["method"], labelVals["route"]).Observe(value)
	case "eino_llm_duration_seconds":
		m.EinoLLMDurationSeconds.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Observe(value)
	case "eino_llm_total_tokens":
		m.EinoLLMTotalTokens.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Observe(value)
	case "eino_llm_prompt_tokens":
		m.EinoLLMPromptTokens.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Observe(value)
	case "eino_llm_completion_tokens":
		m.EinoLLMCompletionTokens.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Observe(value)
	case "eino_retriever_duration_seconds":
		m.EinoRetrieverDurationSeconds.WithLabelValues(labelVals["component"], labelVals["name"]).Observe(value)
	case "eino_retriever_hit_count":
		m.EinoRetrieverHitCount.WithLabelValues(labelVals["component"], labelVals["name"]).Observe(value)
	case "eino_tool_duration_seconds":
		m.EinoToolDurationSeconds.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["tool_name"]).Observe(value)
	case "eino_embed_duration_seconds":
		m.EinoEmbedDurationSeconds.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Observe(value)
	case "eino_embed_total_tokens":
		m.EinoEmbedTotalTokens.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Observe(value)
	case "eino_embed_prompt_tokens":
		m.EinoEmbedPromptTokens.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Observe(value)
	case "eino_agent_duration_seconds":
		m.EinoAgentDurationSeconds.WithLabelValues(labelVals["component"], labelVals["name"]).Observe(value)
	case "eino_stream_end_seconds":
		m.EinoStreamEndSeconds.WithLabelValues(labelVals["component"], labelVals["name"]).Observe(value)
	case "chat_deep_init_ctx_seconds":
		m.ChatDeepInitCtxSeconds.WithLabelValues(labelVals["model_id"]).Observe(value)
	case "ctx_prompt_tokens_by_block":
		m.CtxPromptTokensByBlock.WithLabelValues(labelVals["model_id"], labelVals["block"]).Observe(value)
	default:
		// 未注册的 metric 名，静默丢弃
	}
}

// labelMapToValues 把 map[string]string 转成 map 用于 WithLabelValues 查找。
// 不存在的 key 返回空字符串（Prometheus 允许空标签值）。
func labelMapToValues(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		out[k] = v
	}
	return out
}

// prometheusSnapshot 从 Registry gather 指标并转成 JSON 快照。
// 展开每个 Metric 的 labels / value / count / sum / buckets，供 service 层 groupByMetric 消费。
func prometheusSnapshot(reg *prometheus.Registry) map[string]any {
	mfs, err := reg.Gather()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	out := map[string]any{
		"generated_at_seconds": time.Now().Unix(),
	}
	counters := []any{}
	gauges := []any{}
	histos := []any{}
	for _, mf := range mfs {
		typ := mf.GetType().String()
		name := mf.GetName()
		help := mf.GetHelp()
		for _, m := range mf.GetMetric() {
			labels := []any{}
			for _, lp := range m.GetLabel() {
				labels = append(labels, map[string]any{"name": lp.GetName(), "value": lp.GetValue()})
			}
			switch typ {
			case "COUNTER":
				counters = append(counters, map[string]any{
					"name":   name,
					"help":   help,
					"labels": labels,
					"value":  m.GetCounter().GetValue(),
				})
			case "GAUGE":
				gauges = append(gauges, map[string]any{
					"name":   name,
					"help":   help,
					"labels": labels,
					"value":  m.GetGauge().GetValue(),
				})
			case "HISTOGRAM":
				h := m.GetHistogram()
				buckets := []any{}
				for _, b := range h.GetBucket() {
					var le any = b.GetUpperBound()
					if math.IsInf(le.(float64), 1) {
						le = "+Inf"
					}
					buckets = append(buckets, map[string]any{
						"le":          le,
						"delta_count": int64(b.GetCumulativeCount()),
					})
				}
				histos = append(histos, map[string]any{
					"name":    name,
					"help":    help,
					"labels":  labels,
					"count":   int64(h.GetSampleCount()),
					"sum":     h.GetSampleSum(),
					"buckets": buckets,
				})
			case "SUMMARY":
				s := m.GetSummary()
				histos = append(histos, map[string]any{
					"name":   name,
					"help":   help,
					"labels": labels,
					"count":  int64(s.GetSampleCount()),
					"sum":    s.GetSampleSum(),
				})
			}
		}
	}
	out["counters"] = counters
	out["gauges"] = gauges
	out["histograms"] = histos
	out["enabled"] = true
	return out
}
