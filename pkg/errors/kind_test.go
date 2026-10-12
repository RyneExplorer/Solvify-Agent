package errors

import (
	stderrors "errors"
	"fmt"
	"testing"
)

// ─── 失败种类判定 Classify 的回归测试 ──────────────────────────────────────
//
// 这一层的存在理由就是「判定只有一处」：此前判定靠两处互不相干的字符串黑名单，
// 同一个失败在两处可以得出不同结论。下面先钉住结构化优先，再钉住文本兜底。

// 注册的哨兵必须压过文本特征。
//
// 退化形态（正是本次要防的）：某条宽泛的特征串先命中，把一个有哨兵的精确错误
// 判成另一种失败 —— 实测就是 "exceeds" 把 eino 的迭代超限判成了上下文超长。
func TestClassify_SentinelBeatsTextFeature(t *testing.T) {
	// 故意让哨兵的文本里含有一个**真实存在**的特征串（"context length"）：
	// 这样「哨兵优先」才是被真正验证的 —— 一旦优先级反了，就会落到 KindContextLength。
	text := "context length budget exhausted: exceeds max iterations"
	sentinel := stderrors.New(text)
	RegisterKindSentinel(KindMaxIterations, sentinel)

	err := fmt.Errorf("run node fail: %w", sentinel)
	if got := Classify(err); got != KindMaxIterations {
		t.Fatalf("有哨兵的错误被判成 %q，期望 %q ⇒ 文本特征抢在结构化判定前面了", got, KindMaxIterations)
	}

	// 对照组：同样的文本去掉哨兵后落到文本兜底。
	// 没有这一条，上面那条断言在退化实现下也可能为真（根本没测到优先级）。
	if got := Classify(stderrors.New(text)); got != KindContextLength {
		t.Fatalf("无哨兵时文本兜底失效：got %q，期望 %q", got, KindContextLength)
	}
}

// 判定必须确定：同一输入重复调用结果完全一致（旧实现里 map 遍历顺序会让结果抖动）。
func TestClassify_IsDeterministic(t *testing.T) {
	cases := []error{
		stderrors.New("LLM 调用失败: 429 Too Many Requests"),
		stderrors.New("联网搜索超时"),
		stderrors.New("context deadline exceeded"),
		stderrors.New("dial tcp 127.0.0.1:11434: connect: connection refused"),
	}
	for _, err := range cases {
		first := Classify(err)
		for i := 0; i < 200; i++ {
			if got := Classify(err); got != first {
				t.Fatalf("第 %d 次调用与首次不一致（判定抖动）: err=%v 首次=%q 本次=%q", i+1, err, first, got)
			}
		}
	}
}

// 特征串按「具体 → 通用」查找：更长的串必须胜出，否则具体文案会被通用文案吃掉。
func TestClassify_LongerFeatureWins(t *testing.T) {
	cases := []struct {
		text string
		want Kind
	}{
		// "联网搜索超时" 与 "超时" 都命中，前者更具体
		{"联网搜索超时", KindWebSearchTimeout},
		// "联网搜索认证失败" 比 "联网搜索失败" 更具体吗？不是子串关系，但要确认没被判歪
		{"联网搜索认证失败", KindWebSearchAuthFailed},
		// 仅 "超时" 时落到通用超时
		{"请求超时了", KindTimeout},
		// 上下文超长
		{"this model's maximum context length is 8192 tokens", KindContextLength},
		// 限流：包装文案也带着底层的 429
		{"LLM 调用失败: 429 Too Many Requests", KindRateLimited},
	}
	for _, c := range cases {
		if got := Classify(stderrors.New(c.text)); got != c.want {
			t.Errorf("Classify(%q)=%q，期望 %q", c.text, got, c.want)
		}
	}
}

// nil 错误不参与判定，也绝不能误命中某个特征串。
func TestClassify_NilIsUnknown(t *testing.T) {
	if got := Classify(nil); got != KindUnknown {
		t.Fatalf("Classify(nil)=%q，期望 %q", got, KindUnknown)
	}
}

// 认不出来的错误回落 KindUnknown，由调用方给兜底文案。
func TestClassify_UnknownFallsBack(t *testing.T) {
	if got := Classify(stderrors.New("dial tcp 127.0.0.1:11434: connect: connection refused")); got != KindUnknown {
		t.Fatalf("未登记的错误应回落到 %q，实际 %q", KindUnknown, got)
	}
}

// 特征表里不得出现裸词：裸词会跨种类误伤。
//
// 判据是「该特征串必须能让自己的种类胜出」——不满足说明它被更长的串遮蔽，
// 或者它本身太通用、必然抢走别的种类的报错。
func TestKindTextFeatures_NoShadowedOrBareNeedle(t *testing.T) {
	bare := map[string]bool{"exceeds": true, "exceed": true, "error": true, "失败": true}
	for _, feature := range kindTextFeatures {
		if bare[feature.needle] {
			t.Errorf("特征串 %q 过于宽泛，会跨种类误伤 —— 能用哨兵判定的请走 RegisterKindSentinel", feature.needle)
		}
	}

	// 每个特征串都必须真实可用：拿它本身去判定，必须回到它自己的种类。
	for _, feature := range kindTextFeatures {
		if got := Classify(stderrors.New(feature.needle)); got != feature.kind {
			t.Errorf("特征串 %q 被遮蔽或失效：判定成 %q，期望 %q", feature.needle, got, feature.kind)
		}
	}
}
