package observability

import "testing"

// TestSamplerKeepsTraceWithBusinessOwner 钉住采样契约里最容易退化的那条规则：
// 「有业务归属的 trace 必留」。
//
// 三条规则各钉一条，同时把优先级写死在测试里：
//  1. Required（有 session / message 归属）—— 它的 trace_id 已经对外可见，采掉就是悬空 ID
//  2. 无归属 + 采样率 0 —— 不落库，采样率仍然对 HTTP 噪声生效
//  3. 显式 ForceKeep —— 压过采样率（用户点赞「强制保留」走这条路）
func TestSamplerKeepsTraceWithBusinessOwner(t *testing.T) {
	s := NewDefaultSampler(0, false, false, 0, nil)

	if !s.ShouldSample(SampleRequest{TraceID: "t-owned", Required: true}) {
		t.Error("有业务归属的 trace 必须落库，否则对外返回的 trace_id 会查不到详情")
	}
	if s.ShouldSample(SampleRequest{TraceID: "t-noise"}) {
		t.Error("无归属 + 采样率 0 的 HTTP 噪声不该落库")
	}
	if !s.ShouldSample(SampleRequest{TraceID: "t-like", Decision: SampleDecisionForceKeep}) {
		t.Error("显式 ForceKeep（用户点赞强制保留）应当压过采样率")
	}
}
