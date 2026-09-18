package observability

import (
	"crypto/rand"
	"math/big"
	"sync"
	"time"
)

// SampleRequest 是一次「这条 trace 该不该落库」的完整输入。
//
// 为什么用带名字段而不是位置参数：这里的字段几乎全是 bool / 时长，参数一旦错位编译器
// 不会报错，只会静默改变采样语义（例如把 HasFeedback 传成 HasErr，会让所有请求都被
// 当成出错而全量落库）。字段有名字，调用点就写不出「顺序对了、含义错了」。
type SampleRequest struct {
	TraceID     string
	UserID      string
	HasErr      bool
	Duration    time.Duration
	HasFeedback bool
	Decision    SampleDecision

	// Required 表示这条 trace 承载了业务归属（挂在某轮问答的 session / message 上），
	// 必须落库。
	//
	// 为什么这不是「少采一条日志」那么轻：chat 场景下 traceID 会先写进助手消息的
	// metadata.trace_id 返回给前端，采样丢弃等于对外承诺了一个在 chat_traces 里查不到
	// 详情的悬空 ID（前端点「追踪详情」必查空）。所以 sampling_rate 只作用于没有归属的
	// HTTP 噪声：健康检查、列表轮询、鉴权失败等。
	Required bool
}

// SampleDecision 是外部对单条 trace 的显式采样指令（如用户点赞强制保留）。
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

// ShouldSample 是「这条 trace 到底落不落库」的唯一决策点。
//
// 判定顺序即优先级，先命中先返回：
// 显式指令 > 业务归属必留 > 用户白名单 > 出错 > 用户反馈 > 慢请求 > 采样率。
func (s *DefaultSampler) ShouldSample(req SampleRequest) bool {
	switch req.Decision {
	case SampleDecisionForceKeep:
		return true
	case SampleDecisionForceDrop:
		return false
	}
	// 有业务归属的一律落库：它的 TraceID 已经对外可见了（见 SampleRequest.Required）。
	if req.Required {
		return true
	}
	if _, ok := s.WhiteListUserIDs[req.UserID]; ok && req.UserID != "" {
		return true
	}
	if s.ErrorAlways && req.HasErr {
		return true
	}
	if s.FeedbackAlways && req.HasFeedback {
		return true
	}
	if s.SlowThresholdMs > 0 && req.Duration.Milliseconds() >= int64(s.SlowThresholdMs) {
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
