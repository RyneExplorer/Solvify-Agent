package observability

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"solvify-agent/pkg/logger"
)

type Sink interface {
	Write(ctx context.Context, rec *SinkRecord) error
	Shutdown(ctx context.Context) error
}

type NoopSink struct{}

func (n *NoopSink) Write(_ context.Context, _ *SinkRecord) error { return nil }
func (n *NoopSink) Shutdown(_ context.Context) error             { return nil }

type LogSink struct {
	Enabled    bool
	PII        *PIISanitizer
	Sampler    *DefaultSampler
	dropCount  atomic.Int64
	writeCount atomic.Int64
}

func NewLogSink(enabled bool, pii *PIISanitizer, sampler *DefaultSampler) *LogSink {
	return &LogSink{Enabled: enabled, PII: pii, Sampler: sampler}
}

func (l *LogSink) Write(ctx context.Context, rec *SinkRecord) error {
	if !l.Enabled || rec == nil {
		return nil
	}
	rec = l.clean(rec)
	if rec.Trace != nil {
		if l.Sampler != nil && !rec.Trace.Sampled {
			return nil
		}
	}
	b, err := json.Marshal(rec)
	if err != nil {
		l.dropCount.Add(1)
		return err
	}
	switch rec.Kind {
	case "trace":
		logger.Infof("[OBS] trace payload=%s", string(b))
	case "feedback":
		logger.Infof("[OBS] feedback payload=%s", string(b))
	case "agent_step":
		logger.Infof("[OBS] agent_step payload=%s", string(b))
	default:
		logger.Infof("[OBS] record kind=%s payload=%s", rec.Kind, string(b))
	}
	l.writeCount.Add(1)
	return nil
}

func (l *LogSink) Shutdown(context.Context) error {
	return nil
}

func (l *LogSink) clean(r *SinkRecord) *SinkRecord {
	cp := *r
	if l.PII == nil {
		return &cp
	}
	if cp.Trace != nil && cp.Trace.Root != nil {
		root := *cp.Trace.Root
		cleanSpan(root, l.PII)
		cp.Trace = &Trace{
			ID:         cp.Trace.ID,
			RequestID:  cp.Trace.RequestID,
			UserID:     cp.Trace.UserID,
			SessionID:  cp.Trace.SessionID,
			Root:       &root,
			SampleRate: cp.Trace.SampleRate,
			Sampled:    cp.Trace.Sampled,
		}
	}
	if cp.Feedback != nil {
		fb := *cp.Feedback
		fb.Comment = l.PII.SanitizeString(fb.Comment)
		cp.Feedback = &fb
	}
	if cp.AgentStep != nil {
		st := *cp.AgentStep
		st.ThinkingSummary = l.PII.SanitizeString(st.ThinkingSummary)
		st.ToolInputMasked = l.PII.SanitizeString(st.ToolInputMasked)
		st.ToolResultSummary = l.PII.SanitizeString(st.ToolResultSummary)
		st.ToolError = l.PII.SanitizeString(st.ToolError)
		cp.AgentStep = &st
	}
	return &cp
}

func cleanSpan(s Span, pii *PIISanitizer) {
	if len(s.Attrs) > 0 {
		s.Attrs = pii.SanitizeAttrs(s.Attrs)
	}
	if s.Error != "" {
		s.Error = pii.SanitizeString(s.Error)
	}
	for i := range s.Events {
		if len(s.Events[i].Attrs) > 0 {
			s.Events[i].Attrs = pii.SanitizeAttrs(s.Events[i].Attrs)
		}
	}
	for i := range s.Children {
		cp := *s.Children[i]
		cleanSpan(cp, pii)
		s.Children[i] = &cp
	}
}

type DBSink interface {
	Sink
	WriteTraces(ctx context.Context, traces []*Trace) error
	WriteFeedbacks(ctx context.Context, fs []*Feedback) error
	WriteAgentSteps(ctx context.Context, steps []*AgentStep) error
}

type BatchSink struct {
	mu         sync.Mutex
	sinks      []Sink
	buffer     chan *SinkRecord
	batchSize  int
	interval   time.Duration
	wg         sync.WaitGroup
	closed     chan struct{}
	closeOnce  sync.Once
	dropCount  atomic.Int64
	writeCount atomic.Int64
}

func NewBatchSink(sinks []Sink, bufferSize, batchSize, flushIntervalMs int) *BatchSink {
	if bufferSize <= 0 {
		bufferSize = 1024
	}
	if batchSize <= 0 {
		batchSize = 50
	}
	if flushIntervalMs <= 0 {
		flushIntervalMs = 200
	}
	b := &BatchSink{
		sinks:     sinks,
		buffer:    make(chan *SinkRecord, bufferSize),
		batchSize: batchSize,
		interval:  time.Duration(flushIntervalMs) * time.Millisecond,
		closed:    make(chan struct{}),
	}
	b.wg.Add(1)
	go b.run()
	return b
}

func (b *BatchSink) Write(_ context.Context, rec *SinkRecord) error {
	if rec == nil {
		return nil
	}
	select {
	case b.buffer <- rec:
		return nil
	default:
		b.dropCount.Add(1)
		return errors.New("observability sink buffer full, record dropped")
	}
}

func (b *BatchSink) run() {
	defer b.wg.Done()
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()
	batch := make([]*SinkRecord, 0, b.batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		b.flushBatch(batch)
		batch = batch[:0]
	}
	for {
		select {
		case <-b.closed:
			for {
				select {
				case r := <-b.buffer:
					batch = append(batch, r)
					if len(batch) >= b.batchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case r := <-b.buffer:
			batch = append(batch, r)
			if len(batch) >= b.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (b *BatchSink) flushBatch(batch []*SinkRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, s := range b.sinks {
		if s == nil {
			continue
		}
		for _, r := range batch {
			if err := s.Write(ctx, r); err != nil {
				logger.Warnf("Sink 写入失败: %v", err)
				b.dropCount.Add(1)
				continue
			}
			b.writeCount.Add(1)
		}
	}
}

func (b *BatchSink) Shutdown(ctx context.Context) error {
	b.closeOnce.Do(func() { close(b.closed) })
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var errs []string
	for _, s := range b.sinks {
		if s == nil {
			continue
		}
		if err := s.Shutdown(ctx); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New("sink shutdown errors: " + joinStr(errs, "; "))
	}
	return nil
}

func (b *BatchSink) Stats() (drops, writes int64) {
	return b.dropCount.Load(), b.writeCount.Load()
}

func joinStr(s []string, sep string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += sep
		}
		out += v
	}
	return out
}
