package observability

import (
	"crypto/rand"
	"math/big"
	"sync"
	"time"
)

type Sampler interface {
	ShouldSample(ctx sampleContext, decision SampleDecision) bool
}

type sampleContext struct {
	TraceID     string
	UserID      string
	HasError    bool
	Duration    time.Duration
	SlowLimitMs int
	HasFeedback bool
	WhiteList   map[string]struct{}
	Rate        float64
}

type SampleDecision int

const (
	SampleDecisionDefault SampleDecision = iota
	SampleDecisionForceKeep
	SampleDecisionForceDrop
)

type DefaultSampler struct {
	Rate             float64
	ErrorAlways      bool
	FeedbackAlways   bool
	SlowThresholdMs  int
	WhiteListUserIDs map[string]struct{}
}

func NewDefaultSampler(rate float64, errorAlways, feedbackAlways bool, slowMs int, whiteList []string) *DefaultSampler {
	wm := make(map[string]struct{}, len(whiteList))
	for _, u := range whiteList {
		if u != "" {
			wm[u] = struct{}{}
		}
	}
	if rate <= 0 {
		rate = 0
	}
	if rate > 1 {
		rate = 1
	}
	return &DefaultSampler{
		Rate:             rate,
		ErrorAlways:      errorAlways,
		FeedbackAlways:   feedbackAlways,
		SlowThresholdMs:  slowMs,
		WhiteListUserIDs: wm,
	}
}

var (
	slowMu            sync.Mutex
	slowWindowStart   time.Time
	slowBucketTotal   int64
	slowBucketOver    int64
	slowWindowSeconds = 60
)

func ObserveSlow(sampleOver bool) {
	slowMu.Lock()
	defer slowMu.Unlock()
	now := time.Now()
	if slowWindowStart.IsZero() || now.Sub(slowWindowStart) > time.Duration(slowWindowSeconds)*time.Second {
		slowWindowStart = now
		slowBucketTotal = 0
		slowBucketOver = 0
	}
	slowBucketTotal++
	if sampleOver {
		slowBucketOver++
	}
}

func EstimateP99OverSlow() bool {
	slowMu.Lock()
	defer slowMu.Unlock()
	if slowBucketTotal < 30 {
		return false
	}
	ratio := float64(slowBucketOver) / float64(slowBucketTotal)
	return ratio > 0.01
}

func (s *DefaultSampler) ShouldSample(traceID, userID string, hasErr bool, dur time.Duration, hasFeedback bool, decision SampleDecision) bool {
	switch decision {
	case SampleDecisionForceKeep:
		return true
	case SampleDecisionForceDrop:
		return false
	}
	if _, ok := s.WhiteListUserIDs[userID]; ok && userID != "" {
		return true
	}
	if s.ErrorAlways && hasErr {
		return true
	}
	if s.FeedbackAlways && hasFeedback {
		return true
	}
	if s.SlowThresholdMs > 0 && dur.Milliseconds() >= int64(s.SlowThresholdMs) {
		ObserveSlow(true)
		return true
	}
	ObserveSlow(false)
	if s.Rate >= 1 {
		return true
	}
	if s.Rate <= 0 {
		return false
	}
	return rollSample(s.Rate)
}

func rollSample(rate float64) bool {
	n := int64(10000)
	threshold := int64(rate * float64(n))
	if threshold <= 0 {
		return false
	}
	if threshold >= n {
		return true
	}
	r, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		return false
	}
	return r.Int64() < threshold
}
