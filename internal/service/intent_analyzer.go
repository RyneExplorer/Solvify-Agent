package service

import (
	"strings"
	"unicode/utf8"
)

// Intent 表示识别出的用户意图类型
type Intent string

const (
	// IntentQuestion 知识库问答类（默认）
	IntentQuestion Intent = "question"
	// IntentGreeting 问候类
	IntentGreeting Intent = "greeting"
	// IntentIdentity 询问助手身份/模型
	IntentIdentity Intent = "identity"
	// IntentMeta 询问能力、模式、帮助等元问题
	IntentMeta Intent = "meta"
	// IntentChitchat 闲聊/结束语/致谢等
	IntentChitchat Intent = "chitchat"
	// IntentListQuery 列表/元数据查询（快速模式无法完成）
	IntentListQuery Intent = "list_query"
)

// IntentResult 轻量意图分析结果
type IntentResult struct {
	Intent        Intent
	Confidence    float64
	SkipRetrieval bool
	Reason        string
}

// AnalyzeIntent 基于规则的轻量意图分析
// 不调用 LLM，仅通过关键词和句式识别常见意图
func AnalyzeIntent(query string) IntentResult {
	q := strings.TrimSpace(query)
	if q == "" {
		return IntentResult{
			Intent:        IntentQuestion,
			Confidence:    1.0,
			SkipRetrieval: false,
			Reason:        "空问题按问答处理",
		}
	}

	lower := strings.ToLower(q)

	// 1. 问候
	if isGreeting(lower) {
		return IntentResult{
			Intent:        IntentGreeting,
			Confidence:    0.9,
			SkipRetrieval: true,
			Reason:        "命中问候关键词",
		}
	}

	// 2. 身份/模型类
	if isIdentity(lower) {
		return IntentResult{
			Intent:        IntentIdentity,
			Confidence:    0.95,
			SkipRetrieval: true,
			Reason:        "询问助手身份或模型",
		}
	}

	// 3. 元问题（能力/帮助/模式）
	if isMeta(lower) {
		return IntentResult{
			Intent:        IntentMeta,
			Confidence:    0.85,
			SkipRetrieval: true,
			Reason:        "询问能力、帮助或模式",
		}
	}

	// 4. 闲聊/结束语/致谢
	if isChitchat(lower) {
		return IntentResult{
			Intent:        IntentChitchat,
			Confidence:    0.85,
			SkipRetrieval: true,
			Reason:        "闲聊、致谢或结束语",
		}
	}

	// 5. 列表/元数据查询（快速模式建议切深度模式）
	if isListQuery(lower) {
		return IntentResult{
			Intent:        IntentListQuery,
			Confidence:    0.8,
			SkipRetrieval: true,
			Reason:        "查询列表/元数据，快速模式无法完成",
		}
	}

	// 默认按问答处理
	return IntentResult{
		Intent:        IntentQuestion,
		Confidence:    0.7,
		SkipRetrieval: false,
		Reason:        "未命中特殊意图，按知识库问答处理",
	}
}

func isGreeting(q string) bool {
	greetings := []string{
		"你好", "您好", "嗨", "hello", "hi", "hey",
		"早上好", "上午好", "中午好", "下午好", "晚上好",
		"好久不见", "在吗", "在不在", "在么",
	}
	for _, g := range greetings {
		if q == g || strings.HasPrefix(q, g) {
			return true
		}
	}
	return false
}

func isIdentity(q string) bool {
	patterns := []string{
		"你是谁", "你是什么", "你叫", "你是哪个",
		"什么模型", "哪个模型", "你是gpt", "你是chatgpt",
		"你是claude", "你是通义", "你是千问", "你是deepseek",
		"你是kimi", "你是豆包", "who are you", "what are you",
		"what model", "which model", "your name",
	}
	for _, p := range patterns {
		if strings.Contains(q, p) {
			return true
		}
	}
	return false
}

func isMeta(q string) bool {
	patterns := []string{
		"你能做什么", "你能干嘛", "你会什么", "你有什么用",
		"你能帮我", "你可以帮我", "你可以做什么", "你有什么能力",
		"怎么使用", "如何使用", "使用说明", "使用帮助",
		"帮助", "help", "怎么切换", "如何切换",
		"深度模式", "快速模式", "切换模式", "smart-reasoning",
	}
	for _, p := range patterns {
		if strings.Contains(q, p) {
			return true
		}
	}
	return false
}

func isChitchat(q string) bool {
	// 纯短句结束语/致谢
	phrases := []string{
		"谢谢", "感谢", "多谢", "谢了",
		"再见", "拜拜", "拜", "bye", "goodbye",
		"好的", "OK", "ok", "知道了", "明白了", "清楚了",
		"辛苦了", "牛", "赞", "厉害",
	}
	for _, p := range phrases {
		if q == p || strings.HasPrefix(q, p+" ") || strings.HasPrefix(q, p+"。") {
			return true
		}
	}
	// 只有一个表情或极短无意义内容
	if utf8.RuneCountInString(q) <= 2 && (strings.ContainsAny(q, "👍😊😀🙏🎉👋") || q == "。" || q == "。。") {
		return true
	}
	return false
}

func isListQuery(q string) bool {
	patterns := []string{
		"有哪些知识库", "知识库有哪些", "有什么知识库",
		"有哪些文档", "文档有哪些", "有什么文档",
		"有哪些文件", "文件有哪些", "上传了哪些",
		"列出", "列举", "列表", "清单",
	}
	for _, p := range patterns {
		if strings.Contains(q, p) {
			return true
		}
	}
	return false
}
