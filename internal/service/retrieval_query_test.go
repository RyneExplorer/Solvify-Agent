package service

import (
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestPlanRetrievalQueries(t *testing.T) {
	tests := []struct {
		name          string
		history       []string
		raw           string
		base          string
		wantVector    []string // 必须全部包含
		unwantVector  []string // 必须全部不包含
		wantKeyword   []string
		unwantKeyword []string
		wantBackfill  bool
	}{
		{
			name:        "单轮自包含问题：两路都用原问题",
			raw:         "向量检索的原理是什么",
			base:        "向量检索的原理是什么",
			wantVector:  []string{"向量检索的原理是什么"},
			wantKeyword: []string{"向量检索的原理是什么"},
		},
		{
			name:         "有历史但无指代：向量侧拿到历史，关键字侧只拿原问题（不被污染）",
			history:      []string{"Redis 的超时时间怎么配置"},
			raw:          "支持哪些协议",
			base:         "支持哪些协议",
			wantVector:   []string{"Redis 的超时时间怎么配置", "支持哪些协议"},
			unwantVector: []string{},
			// 关键字侧不能把上一轮的 redis 前缀上来（话题可能已切换）
			wantKeyword:   []string{"支持哪些协议"},
			unwantKeyword: []string{"redis", "Redis"},
		},
		{
			name:          "有指代 + 有实体表：关键字侧回填为短 query",
			history:       []string{"Redis 的超时时间怎么配置"},
			raw:           "那它的超时时间是多少",
			base:          "那它的超时时间是多少",
			wantVector:    []string{"Redis 的超时时间怎么配置", "那它的超时时间是多少"},
			wantKeyword:   []string{"redis", "超时", "时间"},
			unwantKeyword: []string{"它"},
			wantBackfill:  true,
		},
		{
			name:          "指示代词 + 中心语整体回填，不产生残句",
			history:       []string{"Redis 的超时时间怎么配置"},
			raw:           "这个方案的成本是多少",
			base:          "这个方案的成本是多少",
			wantKeyword:   []string{"redis", "成本"},
			unwantKeyword: []string{"这个方案", "方案"},
			wantBackfill:  true,
		},
		{
			name:         "有指代但实体表为空：不猜，关键字侧保持原问题",
			raw:          "那个方案呢",
			base:         "那个方案呢",
			wantKeyword:  []string{"那个方案呢"},
			wantBackfill: false,
		},
		{
			name:         "有指代、实体表为空但 LLM 已消解：关键字侧借用 LLM 结果",
			raw:          "那它的超时时间是多少",
			base:         "Redis 的超时时间是多少",
			wantKeyword:  []string{"Redis 的超时时间是多少"},
			wantBackfill: false,
		},
		{
			name:         "「其他」里的「他」不是指代，不回填",
			history:      []string{"Redis 的超时时间怎么配置"},
			raw:          "其他方案有什么区别",
			base:         "其他方案有什么区别",
			wantKeyword:  []string{"其他方案有什么区别"},
			wantBackfill: false,
		},
		{
			name:         "「应该」里的「该」不触发回填",
			history:      []string{"Redis 的超时时间怎么配置"},
			raw:          "应该怎么配置",
			base:         "应该怎么配置",
			wantKeyword:  []string{"应该怎么配置"},
			wantBackfill: false,
		},
		{
			// 回归 test1/kw_strategy_probe.go：历史里的「网络安全」是文档级泛词（4 字，比 osi 长），
			// 按长度优先会选中它，关键字侧就会命中 DoS 噪声段；专名优先必须选中 osi。
			name:          "专名回填：文档级泛词不顶掉专名（OSI 场景）",
			history:       []string{"网络安全里 OSI 七层模型有哪些层"},
			raw:           "那它一共分了几层？",
			base:          "那它一共分了几层？",
			wantKeyword:   []string{"osi"},
			unwantKeyword: []string{"网络安全", "它"},
			wantBackfill:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := planRetrievalQueries(tt.history, tt.raw, tt.base)
			for _, w := range tt.wantVector {
				if !strings.Contains(got.Vector, w) {
					t.Errorf("Vector=%q 应包含 %q", got.Vector, w)
				}
			}
			for _, w := range tt.unwantVector {
				if strings.Contains(got.Vector, w) {
					t.Errorf("Vector=%q 不应包含 %q", got.Vector, w)
				}
			}
			for _, w := range tt.wantKeyword {
				if !strings.Contains(got.Keyword, w) {
					t.Errorf("Keyword=%q 应包含 %q", got.Keyword, w)
				}
			}
			for _, w := range tt.unwantKeyword {
				if strings.Contains(got.Keyword, w) {
					t.Errorf("Keyword=%q 不应包含 %q", got.Keyword, w)
				}
			}
			if got.Backfilled != tt.wantBackfill {
				t.Errorf("Backfilled=%v 期望 %v (entities=%v)", got.Backfilled, tt.wantBackfill, got.Entities)
			}
		})
	}
}

func TestPlanRetrievalQueriesTruncation(t *testing.T) {
	long := strings.Repeat("很", 500)
	got := planRetrievalQueries([]string{long, long}, "当前问题", "当前问题")

	if n := len([]rune(got.Vector)); n > retrievalVectorMaxRunes {
		t.Errorf("Vector 长度 %d 超过上限 %d", n, retrievalVectorMaxRunes)
	}
	// 截断保留尾部 → 当前问题必须还在
	if !strings.Contains(got.Vector, "当前问题") {
		t.Errorf("Vector=%q 截断后应保留当前问题", got.Vector)
	}
}

func TestBuildEntityTable(t *testing.T) {
	tests := []struct {
		name    string
		history []string
		wantTop string
	}{
		{name: "实体词优先于功能词", history: []string{"Redis 的超时时间怎么配置"}, wantTop: "redis"},
		{name: "更长的切分单元更具体", history: []string{"你们支持哪些数据库"}, wantTop: "数据库"},
		{name: "疑问词不进实体表", history: []string{"支持哪些协议"}, wantTop: "协议"},
		{name: "新轮次优先", history: []string{"Redis 的超时时间", "Kafka 的消费延迟"}, wantTop: "kafka"},
		{name: "无有效词时返回空", history: []string{"你好呀"}, wantTop: ""},
		{name: "带指代的历史词不当实体", history: []string{"那它一共分了几层？", "Redis 的超时时间"}, wantTop: "redis"},
		// 「网络安全」是文档级泛词（4 字，比 osi 长），拿它回填会把噪声 chunk 一起拉上来；
		// 专名优先必须把 osi 顶到首位。回归自 test1/kw_strategy_probe.go 的实测。
		{name: "专名优先于中文文档级泛词", history: []string{"网络安全里 OSI 七层模型有哪些层"}, wantTop: "osi"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildEntityTable(tt.history, retrievalEntityMaxCandidates)
			if tt.wantTop == "" {
				if len(got) != 0 {
					t.Fatalf("期望空实体表，实际 %v", got)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("实体表为空，期望首位 %q", tt.wantTop)
			}
			if got[0] != tt.wantTop {
				t.Errorf("实体表首位=%q 期望 %q (全量=%v)", got[0], tt.wantTop, got)
			}
		})
	}
}

func TestHasAnaphora(t *testing.T) {
	cases := map[string]bool{
		"那它的超时时间是多少":  true,
		"这个方案的成本":     true,
		"那个配置项在哪里":    true,
		"上述配置的作用":     true,
		"其他方案有什么区别":   false,
		"应该怎么配置":      false,
		"向量检索的原理是什么":  false,
		"Redis 的超时时间": false,
	}
	for q, want := range cases {
		if got := hasAnaphora(q); got != want {
			t.Errorf("hasAnaphora(%q)=%v 期望 %v", q, got, want)
		}
	}
}

func TestRecentUserQuestions(t *testing.T) {
	msgs := []*schema.Message{
		schema.SystemMessage("sys"),
		schema.UserMessage("第一轮问题"),
		schema.AssistantMessage("第一轮回答", nil),
		schema.UserMessage("第二轮问题"),
		schema.AssistantMessage("第二轮回答", nil),
		schema.UserMessage("第三轮问题"),
		schema.AssistantMessage("第三轮回答", nil),
		schema.UserMessage("当前问题"),
	}
	got := recentUserQuestions(msgs, len(msgs)-1, 2)
	want := []string{"第二轮问题", "第三轮问题"}
	if len(got) != len(want) {
		t.Fatalf("got=%v 期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q 期望 %q", i, got[i], want[i])
		}
	}
	if strings.Contains(strings.Join(got, "|"), "当前问题") {
		t.Error("不应把当前问题算进历史")
	}
	if strings.Contains(strings.Join(got, "|"), "回答") {
		t.Error("不应把助手回答算进历史")
	}
}

func TestRecentUserQuestionsStripsTruncationMarker(t *testing.T) {
	// 上下文预算裁剪会给用户消息追加标记，检索 query 必须拿到正文而不是标记
	truncated := "Redis 的超时时间怎么配置？" + truncationMarker
	msgs := []*schema.Message{
		schema.UserMessage(truncated),
		schema.AssistantMessage("上一轮回答", nil),
		schema.UserMessage("那它的重试次数呢？"),
	}
	got := recentUserQuestions(msgs, len(msgs)-1, 2)
	if len(got) != 1 {
		t.Fatalf("got=%v 期望 1 条", got)
	}
	if got[0] != "Redis 的超时时间怎么配置？" {
		t.Errorf("历史正文=%q 应剥掉截断标记", got[0])
	}

	// 端到端：标记不能作为词汇混进检索 query 与实体表
	q := planRetrievalQueries(got, "那它的重试次数呢？", "那它的重试次数呢？")
	for _, bad := range []string{"内容", "过长", "截断"} {
		if strings.Contains(q.Vector, bad) {
			t.Errorf("Vector=%q 混入了截断标记词汇 %q", q.Vector, bad)
		}
		if strings.Contains(q.Keyword, bad) {
			t.Errorf("Keyword=%q 混入了截断标记词汇 %q", q.Keyword, bad)
		}
		for _, e := range q.Entities {
			if e == bad {
				t.Errorf("实体表混入了截断标记词汇: %v", q.Entities)
			}
		}
	}
	if q.Entities[0] != "redis" {
		t.Errorf("实体表首位=%q 期望 redis (全量=%v)", q.Entities[0], q.Entities)
	}
}
