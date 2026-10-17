package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	aclOpenai "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// modelStreamTimeout 保留 deadline 语义，供 Agent 区分超时与用户取消。
type modelStreamTimeout string

func (e modelStreamTimeout) Error() string   { return string(e) }
func (e modelStreamTimeout) Unwrap() error   { return context.DeadlineExceeded }
func (e modelStreamTimeout) Timeout() bool   { return true }
func (e modelStreamTimeout) Temporary() bool { return true }

type streamWatchKey struct{}

type streamWatch struct {
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelCauseFunc
	cancelTotal context.CancelFunc
	timer       *time.Timer
	idle        time.Duration
	deadline    time.Time
	started     bool
	finished    bool
	cause       error
}

func newStreamWatch(parent context.Context, total, first, idle time.Duration) (context.Context, *streamWatch) {
	var ctx context.Context
	var cancelTotal context.CancelFunc
	if total > 0 {
		ctx, cancelTotal = context.WithTimeout(parent, total)
	} else {
		ctx, cancelTotal = context.WithCancel(parent)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	w := &streamWatch{ctx: ctx, cancel: cancel, cancelTotal: cancelTotal, idle: idle}
	w.mu.Lock()
	if first > 0 {
		w.deadline = time.Now().Add(first)
		w.timer = time.AfterFunc(first, w.expire)
	}
	w.mu.Unlock()
	context.AfterFunc(ctx, func() { w.finish() })
	return context.WithValue(ctx, streamWatchKey{}, w), w
}

func (w *streamWatch) expire() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finished {
		return
	}
	// Reset 与已开始的 AfterFunc 可能交错，必须重新比较最近的截止时间。
	if remaining := time.Until(w.deadline); remaining > 0 {
		w.timer.Reset(remaining)
		return
	}
	phase := "首个有效输出"
	if w.started {
		phase = "连续有效输出"
	}
	w.cancel(modelStreamTimeout(fmt.Sprintf("模型流等待%s超时", phase)))
}

func (w *streamWatch) activity(msg *schema.Message) {
	if !hasModelOutput(msg) {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finished || w.ctx.Err() != nil {
		return
	}
	w.started = true
	if w.idle > 0 {
		w.deadline = time.Now().Add(w.idle)
		if w.timer == nil {
			w.timer = time.AfterFunc(w.idle, w.expire)
		} else {
			w.timer.Reset(w.idle)
		}
	}
}

func hasModelOutput(msg *schema.Message) bool {
	if msg == nil {
		return false
	}
	if msg.Content != "" || msg.ReasoningContent != "" {
		return true
	}
	if reasoning, _ := aclOpenai.GetReasoningContent(msg); reasoning != "" {
		return true
	}
	for _, call := range msg.ToolCalls {
		if call.ID != "" || call.Function.Name != "" || call.Function.Arguments != "" {
			return true
		}
	}
	return false
}

func (w *streamWatch) finish() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finished {
		return
	}
	w.finished = true
	w.cause = context.Cause(w.ctx)
	if w.timer != nil {
		w.timer.Stop()
	}
	w.cancel(nil)
	w.cancelTotal()
}

func (w *streamWatch) result(err error) error {
	w.finish()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cause != nil {
		return w.cause
	}
	return err
}

func (m *contextModel) streamWithTimeout(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	ctx, watch := newStreamWatch(ctx, m.timeout, m.firstTimeout, m.idleTimeout)
	options := m.callOptions(opts)
	// 在 Eino 解析有效上游片段时计时，避免前端消费速度影响判断。
	options = append(options, aclOpenai.WithResponseChunkMessageModifier(func(_ context.Context, msg *schema.Message, _ []byte, _ bool) (*schema.Message, error) {
		watch.activity(msg)
		return msg, nil
	}))
	sr, err := m.inner.Stream(ctx, msgs, options...)
	if err != nil {
		return nil, watch.result(err)
	}
	return schema.StreamReaderWithConvert(sr, func(msg *schema.Message) (*schema.Message, error) { return msg, nil },
		schema.WithErrWrapper(watch.result),
		schema.WithOnEOF(func() (any, error) { return nil, watch.result(io.EOF) })), nil
}

// timedModelTransport 只包装请求生命周期；连接仍由共享 Transport 管理。
type timedModelTransport struct{ base http.RoundTripper }

func (t timedModelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if watch, ok := req.Context().Value(streamWatchKey{}).(*streamWatch); ok {
		if err != nil {
			return nil, watch.result(err)
		}
		resp.Body = &watchedModelBody{ReadCloser: resp.Body, watch: watch}
	}
	return resp, err
}

type watchedModelBody struct {
	io.ReadCloser
	watch *streamWatch
}

func (b *watchedModelBody) Close() error {
	err := b.ReadCloser.Close()
	b.watch.finish()
	return err
}
