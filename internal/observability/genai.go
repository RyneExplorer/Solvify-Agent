package observability

import "sync"

// OpenTelemetry gen_ai 语义约定属性名。
//
// 为什么用字符串字面量而不是 semconv 常量：OTel Go 从 v1.42.0 起把 gen_ai 从核心
// semconv 移到了独立仓库（该仓库还没有 tag），本项目 OTel v1.45.0 的 semconv 包里
// 已经没有 gen_ai.* 常量，写常量会编译不过。
//
// 命名按当前（Development 阶段）的定义，几个已废弃/移除的旧名没有使用：
//   - gen_ai.system              废弃 → gen_ai.provider.name
//   - gen_ai.usage.prompt_tokens 改名 → gen_ai.usage.input_tokens
//   - gen_ai.usage.completion_tokens → gen_ai.usage.output_tokens
//   - gen_ai.prompt / gen_ai.completion 移除 → gen_ai.input.messages / gen_ai.output.messages
//
// 这套属性是给三方追踪平台（Jaeger / Tempo / Langfuse / Phoenix 等）认 span 用的：
//   - 有 gen_ai.operation.name → 平台知道这是 LLM / 工具 / 检索调用
//   - 有 gen_ai.provider.name + request.model → 平台能归类和选图标
//   - 有 gen_ai.usage.* → 平台能出 token 成本统计
//
// 项目自有的 attrs（model_id / prompt_tokens / reply_preview 等）保持不变，
// 那套是前端 chat_traces.span_tree 可视化用的，两套并存、互不影响。
const (
	// Required —— 缺失时三方平台不会把 span 识别成 LLM/工具/检索卡片
	AttrGenAIOperationName = "gen_ai.operation.name"
	AttrGenAIProviderName  = "gen_ai.provider.name"
	AttrGenAIRequestModel  = "gen_ai.request.model"

	// 请求参数
	AttrGenAIRequestTemperature   = "gen_ai.request.temperature"
	AttrGenAIRequestMaxTokens     = "gen_ai.request.max_tokens"
	AttrGenAIRequestTopP          = "gen_ai.request.top_p"
	AttrGenAIRequestStopSequences = "gen_ai.request.stop_sequences"
	AttrGenAIRequestStream        = "gen_ai.request.stream"

	// 响应
	AttrGenAIResponseModel         = "gen_ai.response.model"
	AttrGenAIResponseFinishReasons = "gen_ai.response.finish_reasons"

	// token 用量
	AttrGenAIUsageInputTokens     = "gen_ai.usage.input_tokens"
	AttrGenAIUsageOutputTokens    = "gen_ai.usage.output_tokens"
	AttrGenAIUsageCacheReadTokens = "gen_ai.usage.cache_read.input_tokens"
	AttrGenAIUsageReasoningTokens = "gen_ai.usage.reasoning.output_tokens"

	// 工具
	AttrGenAIToolName          = "gen_ai.tool.name"
	AttrGenAIToolType          = "gen_ai.tool.type"
	AttrGenAIToolCallArguments = "gen_ai.tool.call.arguments"
	AttrGenAIToolCallResult    = "gen_ai.tool.call.result"

	// 检索
	AttrGenAIRetrievalQueryText = "gen_ai.retrieval.query.text"

	// Embedding
	AttrGenAIEmbeddingsDimensionCount = "gen_ai.embeddings.dimension.count"
)

// gen_ai.operation.name 的取值（semconv 允许的枚举）。
const (
	genAIOpChat        = "chat"
	genAIOpEmbeddings  = "embeddings"
	genAIOpExecuteTool = "execute_tool"
	genAIOpRetrieval   = "retrieval"
	genAIOpInvokeAgent = "invoke_agent"
	genAIOpInvokeFlow  = "invoke_workflow"
)

// genAIProviderRegistry 是 modelID → 供应商名 的登记表。
//
// 为什么需要它：eino 的 model.CallbackInput.Config 只带 Model / Temperature / MaxTokens
// 这类生成参数，不带 provider；而 gen_ai.provider.name 是 semconv 的 Required 属性。
// 所以由构造 LLM 客户端的业务方（chat service 的 resolveClient）登记一次，
// eino 回调再按 model_id 反查。
var genAIProviderRegistry sync.Map

// RegisterGenAIProvider 登记 modelID 对应的供应商名，例如 openai / deepseek / zhipu / tongyi。
//
// 重复登记以最后一次为准（同 modelID 换供应商时会覆盖）；modelID 或 provider 为空时忽略。
func RegisterGenAIProvider(modelID, provider string) {
	if modelID == "" || provider == "" {
		return
	}
	genAIProviderRegistry.Store(modelID, provider)
}

// genAIProviderFor 反查 modelID 对应的供应商名，没登记过时返回空字符串（调用方跳过该属性）。
func genAIProviderFor(modelID string) string {
	if modelID == "" {
		return ""
	}
	if v, ok := genAIProviderRegistry.Load(modelID); ok {
		s, _ := v.(string)
		return s
	}
	return ""
}
