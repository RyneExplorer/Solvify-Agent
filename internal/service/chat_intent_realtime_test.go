package service

import (
	"strings"
	"testing"
)

// ─── realtime 意图：本地短路 + 派生规则 的回归 ───────────────────────────────
//
// 背景（三个缺陷叠在一起，收敛到同一个概念）：
//   1. 天气类问法曾被硬编码进 reChitchat ⇒ 判成闲聊、不检索，模型只能编一个天气出来；
//   2. 从 reChitchat 摘掉之后落回 question ⇒ 白跑一次知识库检索，再回一句「知识库没有」；
//   3. 快速模式没有工具链，实时数据本来就拿不到。
// 三者收敛成 realtime：本地直接短路、跳过检索，由 prompt 的「实时信息处理」段明确告知用户，
// 并指出「去深度模式调工具」这条路。

// TestRealtimeQueryRoutesToRealtimeIntent 本地短路回归：实时类问法必须命中 realtime，
// 既不能落进 chitchat（编造数值），也不能落进 question（白跑一次检索）。
func TestRealtimeQueryRoutesToRealtimeIntent(t *testing.T) {
	cases := []string{
		"今天天气怎么样",
		"天气怎么样",
		"北京现在气温多少",
		"明天会下雨吗",
		"最近的新闻有哪些",
		"今日新闻有什么",
		"今天股价怎么样",
		"美元汇率是多少",
	}
	for _, q := range cases {
		intent, ok := matchLocalIntent(q)
		if !ok {
			t.Errorf("matchLocalIntent(%q) 未被本地规则命中：实时类问法必须本地短路，不该交给 LLM 判", q)
			continue
		}
		if intent != intentRealtime {
			t.Errorf("matchLocalIntent(%q) = %q，期望 %q", q, intent, intentRealtime)
		}
	}
}

// TestRealtimeIntentSkipsRetrieve 钉住派生规则：realtime 必须跳过检索。
func TestRealtimeIntentSkipsRetrieve(t *testing.T) {
	if !deriveSkipRetrieve(intentRealtime, false) {
		t.Errorf("deriveSkipRetrieve(%q, false) = false，期望 true：realtime 不该白跑检索", intentRealtime)
	}
}

// TestRealtimeIntentIsValidEnum 钉住枚举：漏了它，LLM 返回的 realtime 会被静默改写成
// question，于是又回到「白跑检索再回一句知识库没有」那条路。
func TestRealtimeIntentIsValidEnum(t *testing.T) {
	if !isValidIntent(intentRealtime) {
		t.Errorf("isValidIntent(%q) = false：LLM 返回 realtime 会被改写成 %q", intentRealtime, intentQuestion)
	}
}

// TestChitchatStillMatchesPureChitchat 是对照组：从 reChitchat 摘掉天气字面量时，
// 它原本覆盖的纯闲聊问法不能被一起改坏（否则这轮改动只是把缺陷挪了个位置）。
func TestChitchatStillMatchesPureChitchat(t *testing.T) {
	for _, q := range []string{"讲个笑话", "随便聊聊", "夸夸我"} {
		intent, ok := matchLocalIntent(q)
		if !ok || intent != intentChitchat {
			t.Errorf("matchLocalIntent(%q) = (%q, %v)，期望 (%q, true)", q, intent, ok, intentChitchat)
		}
	}
}

// TestKnowledgeQuestionStillRetrieves 是另一侧对照组：知识问答必须照旧走检索，
// 不能被 realtime 这条新规则误伤成「跳过检索」。
func TestKnowledgeQuestionStillRetrieves(t *testing.T) {
	if deriveSkipRetrieve(intentQuestion, false) {
		t.Errorf("deriveSkipRetrieve(%q, false) = true：知识问答必须走检索", intentQuestion)
	}
	if intent, ok := matchLocalIntent("怎么配置模型路由"); ok {
		t.Errorf("matchLocalIntent(知识问题) 命中本地规则 %q：知识问答应交给 LLM 与检索链路", intent)
	}
}

// TestQuickPromptCarriesRealtimeAndGeneralKnowledgeRules 钉住快速模式 prompt 的两条口径：
// ① 实时类要「明确告知 + 指向深度模式」（此前缺失 ⇒ 模型转去编一个天气）；
// ② 没有参考资料时必须允许用通用知识并显式标注（此前是「禁止靠常识补全」，把常识回答堵死）。
// 断言的是生产代码真正喂给 LLM 的那个常量本身，不是在测试里复刻一份 prompt。
func TestQuickPromptCarriesRealtimeAndGeneralKnowledgeRules(t *testing.T) {
	for _, sub := range []string{
		"## 实时信息处理",
		"深度模式",
		"以下内容基于通用知识，未来自知识库",
	} {
		if !strings.Contains(quickModeAgentSystemPrompt, sub) {
			t.Errorf("quickModeAgentSystemPrompt 缺少 %q", sub)
		}
	}
	if strings.Contains(quickModeAgentSystemPrompt, "禁止靠常识补全") {
		t.Error("quickModeAgentSystemPrompt 仍含「禁止靠常识补全」：这条会堵死通用知识回答")
	}
}
