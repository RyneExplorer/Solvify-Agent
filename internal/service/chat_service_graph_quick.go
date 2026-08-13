package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/retriever"
	einoCompose "github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	llmpkg "solvify-agent/internal/llm"
	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/observability"
	"solvify-agent/internal/rag"
	"solvify-agent/pkg/config"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/tokenutil"
)

// quickGraphInput 快速模式 Graph 入参，跨节点共享的上下文通过 Local State 传递。
type quickGraphInput struct {
	// OriginalQuery 用户原始问题
	OriginalQuery string
	// UserID 用户标识，注入到检索请求埋点
	UserID string
	// KnowledgeBaseIDs 限定检索范围
	KnowledgeBaseIDs []string
	// InputMsgs 已经组装好的 System + History + RewritePlaceholder
	InputMsgs []*schema.Message
	// UserQuestionIndex InputMsgs 里代表「用户问题」那条消息的下标（用于替换成改写后的 query）
	UserQuestionIndex int
	// ModelName 模型名，用于真 BPE token 截断
	ModelName string
	// RetrievalBudget 检索上下文 token 预算（真 BPE）
	RetrievalBudget int

	// PreRewritten* Graph 执行前已算好的 Rewrite 结果，避免 Graph 内重复调 LLM
	PreRewrittenQuery  string
	PreIntent          string
	PreKeywords        []string
	PreSkipRetrieve    bool
	PreNeedClarify     bool
	PreClarifyQuestion string
	PreClarifyOptions  []string
}

// quickGraphState Graph Local State，通过 ProcessState 读写。
type quickGraphState struct {
	Input          *quickGraphInput
	RewrittenQuery string   // 改写后的查询，供 Retrieve / BuildMsgs 使用
	Intent         string   // greeting / chitchat / question / identity / meta
	SkipRetrieve   bool     // Greeting/Chitchat 跳过知识库检索
	NeedClarify    bool     // 意图不明确,需要用户澄清
	ClarifyQuestion string  // 追问文本
	ClarifyOptions  []string // 追问选项(可选)
	Keywords       []string // 改写时提取的关键词，可用于日志/调试
	RetrievedDocs  []*schema.Document

	// RewriteDone 后台 goroutine 完成 LLM 改写后关闭，Retrieve 阶段可选等待
	RewriteDone chan struct{}
	RewriteErr  error
}

// 查询改写意图类型
const (
	intentGreeting = "greeting" // 问候语
	intentChitchat = "chitchat" // 闲聊
	intentQuestion = "question" // 知识查询（默认，最常见）
	intentIdentity = "identity" // 身份确认（你是谁、你能做什么）
	intentMeta     = "meta"     // 元问题（我的历史记录、你刚才说了什么）
)

// rewriteResult LLM 返回的 JSON 解析结果
type rewriteResult struct {
	Rewritten       string   `json:"rewritten"`
	Intent          string   `json:"intent"`
	Keywords        []string `json:"keywords"`
	NeedClarify     bool     `json:"need_clarify"`
	ClarifyQuestion string   `json:"clarify_question,omitempty"`
	ClarifyOptions  []string `json:"clarify_options,omitempty"`
}

// rewriteMaxHistoryRounds 改写时拼入历史的最大轮数（每轮=user+assistant）
const rewriteMaxHistoryRounds = 3

// rewriteSystemPrompt 改写专用的 System Prompt
const rewriteSystemPrompt = `你是一个查询改写助手。根据用户的原始问题和对话历史，对问题进行改写并识别意图。

## 改写规则
1. 消解指代：把"这个"、"那个方案"、"它"、"之前说的"等代词替换为对话历史中的具体名词
2. 扩展关键词：补充与问题相关的同义词、上下位词，方便知识库检索
3. 拆分复合问题：如果原问题包含多个子问题，改写为一个完整句子即可（不要拆成多行）
4. 保持原意：改写后的问题必须和原问题核心意图一致，不要引入新主题

## 意图识别
- greeting: 问候语（你好、hi、在吗、早上好）
- chitchat: 闲聊（今天天气怎么样、讲个笑话、随便聊聊）
- question: 知识查询（业务问题、技术问题、需要从知识库找答案）
- identity: 身份确认（你是谁、你能做什么、介绍一下你自己）
- meta: 元问题（我的历史记录、你刚才说了什么、回顾对话）

## 澄清追问判断
当用户问题过于模糊、存在多种理解且无法从历史对话推断真实意图时，设置 need_clarify=true：
- 没有历史上下文时，单个指代性问题（如"那个方案"、"它"）且知识库依赖强 → 追问
- 问题包含可能冲突的关键概念（如"怎么导出数据"未指明导出格式/导出范围）→ 追问
- 用户同时提及多个实体且未指明主体 → 追问
以下情况**不要**追问：
- 打招呼、闲聊、身份类意图（greeting/chitchat/identity）→ 直接返回原问题
- 有历史对话可以消解歧义 → 直接改写，need_clarify=false
- 即使问题有些宽泛，但可以给一个通用回答 → 直接回答，need_clarify=false

## 输出格式
严格使用 JSON，不要输出任何多余文字或 Markdown 代码块：
{"rewritten": "改写后的完整问题", "intent": "question", "keywords": ["关键词1", "关键词2"], "need_clarify": false, "clarify_question": "", "clarify_options": []}
需要追问时示例：
{"rewritten": "", "intent": "question", "keywords": [], "need_clarify": true, "clarify_question": "你是要导出哪些数据？是单个知识库还是全部知识库？", "clarify_options": ["单个知识库", "全部知识库", "指定文档范围"]}`

const (
	graphQuickNodeRewrite   = "query_rewrite"
	graphQuickNodeRetrieve  = "retrieve"
	graphQuickNodeBuildMsgs = "build_prompt_messages"
	graphQuickNodeGenerate  = "generate"
)

// buildQuickGraph 构建 START → rewrite → retrieve → build_msgs → generate → END 流水线。
// graphState 必须在 Invoke 前创建好并传入——它既是 eino compose 的 stateGenerator 返回值，
// 也是 Invoke 返回后外部读取 RetrievedDocs 的唯一入口。
func buildQuickGraph(
	graphState *quickGraphState,
	einoRetriever *rag.EinoRetrieverAdapter,
) (*einoCompose.Graph[*quickGraphInput, *schema.StreamReader[*schema.Message]], error) {
	// genState 是 buildQuickGraph 的局部闭包，始终返回同一个 graphState 实例。
	// eino compose 在 runCtx 里只是用 internalState 包装 graphState 指针，
	// 所以 StatePostHandler 写入的字段和外部 graphState 是同一对象——Invoke 返回后还能读到。
	genState := func(_ context.Context) *quickGraphState { return graphState }

	g := einoCompose.NewGraph[*quickGraphInput, *schema.StreamReader[*schema.Message]](
		einoCompose.WithGenLocalState(genState),
	)
	if err := addQuickRewriteNode(g); err != nil {
		return nil, wrapGraphErr("add rewrite node", err)
	}
	if err := addQuickRetrieveNode(g, einoRetriever); err != nil {
		return nil, wrapGraphErr("add retrieve node", err)
	}
	if err := addQuickBuildMsgsNode(g); err != nil {
		return nil, wrapGraphErr("add build msgs node", err)
	}
	if err := addQuickGenerateNode(g); err != nil {
		return nil, wrapGraphErr("add generate node", err)
	}
	if err := registerQuickGraphEdges(g); err != nil {
		return nil, wrapGraphErr("register edges", err)
	}
	return g, nil
}

// wrapGraphErr 统一包装"Graph 装配错误"为业务错误码，避免每个 AddXxx 节点都写一遍
func wrapGraphErr(stage string, err error) error {
	return apperrors.WrapDefault(apperrors.CodeInternalError, fmt.Errorf("%s: %w", stage, err))
}

// addQuickRewriteNode 节点 1：QueryRewrite，调 LLM 做查询改写 + 意图识别。
func addQuickRewriteNode(g *einoCompose.Graph[*quickGraphInput, *schema.StreamReader[*schema.Message]]) error {
	return g.AddLambdaNode(graphQuickNodeRewrite,
		einoCompose.InvokableLambda(quickRewriteFn),
		einoCompose.WithNodeName("QueryRewrite"),
	)
}

// quickRewriteFn 节点 1 实现：触发查询改写 + 意图识别，立即返回原始 query 让 Retrieve 先行。
// 并行策略：LLM 改写放到后台 goroutine 异步执行，Retrieve 用 OriginalQuery 先跑，
// 改写结果通过 State.RewriteDone channel 通知下游，Retrieve StatePostHandler 可选等待并补检索。
// 短路优化：如果 graphInput.PreRewrittenQuery 已填（Graph 执行前已算过），直接同步执行不再起 goroutine。
func quickRewriteFn(ctx context.Context, input *quickGraphInput) (string, error) {
	if input == nil {
		return "", apperrors.NewDefault(apperrors.CodeInvalidParam)
	}

	// 初始化 State：存 Input + 创建 RewriteDone channel
	if err := einoCompose.ProcessState(ctx, func(_ context.Context, state *quickGraphState) error {
		state.Input = input
		state.RewriteDone = make(chan struct{})
		return nil
	}); err != nil {
		return "", err
	}

	// 短路：Graph 执行前已算好 → 同步写 state，不走并行
	if input.PreRewrittenQuery != "" {
		rewritten, intent, keywords := input.PreRewrittenQuery, input.PreIntent, input.PreKeywords
		skipRetrieve, needClarify := input.PreSkipRetrieve, input.PreNeedClarify
		clarifyQ, clarifyO := input.PreClarifyQuestion, input.PreClarifyOptions
		_ = einoCompose.ProcessState(ctx, func(_ context.Context, state *quickGraphState) error {
			state.RewrittenQuery = rewritten
			state.Intent = intent
			state.Keywords = keywords
			state.SkipRetrieve = skipRetrieve
			state.NeedClarify = needClarify
			state.ClarifyQuestion = clarifyQ
			state.ClarifyOptions = clarifyO
			close(state.RewriteDone)
			return nil
		})
		observability.SetSpanAttrs(ctx, observability.Attrs{
			"original_query":  input.OriginalQuery,
			"rewritten_query": rewritten,
			"rewrite_mode":    "precomputed",
		})
		return rewritten, nil
	}

	// 正常路径：后台 goroutine 异步跑 LLM 改写，立即返回 OriginalQuery 让 Retrieve 并行
	go func() {
		startAt := time.Now()
		rewritten, intent, keywords, skipRetrieve, needClarify, clarifyQ, clarifyO := doRewriteWithLLM(ctx, input)
		_ = einoCompose.ProcessState(ctx, func(_ context.Context, state *quickGraphState) error {
			state.RewrittenQuery = rewritten
			state.Intent = intent
			state.Keywords = keywords
			state.SkipRetrieve = skipRetrieve
			state.NeedClarify = needClarify
			state.ClarifyQuestion = clarifyQ
			state.ClarifyOptions = clarifyO
			close(state.RewriteDone)
			return nil
		})
		observability.SetSpanAttrs(ctx, observability.Attrs{
			"rewrite_ms":      time.Since(startAt).Milliseconds(),
			"rewrite_mode":    "async",
			"original_query":  input.OriginalQuery,
			"rewritten_query": rewritten,
			"intent":          intent,
			"skip_retrieve":   fmt.Sprintf("%v", skipRetrieve),
			"need_clarify":    fmt.Sprintf("%v", needClarify),
		})
	}()

	observability.SetSpanAttrs(ctx, observability.Attrs{
		"original_query": input.OriginalQuery,
		"rewrite_mode":   "async_started",
	})
	return input.OriginalQuery, nil
}

// matchLocalIntent 本地快速意图匹配（纯正则 + 关键词，0ms）。
// 返回 (intent, matched) —— matched=false 表示交给 LLM 判定。
//
// 覆盖四类场景：
//   greeting: 你好 / hi / 早上好 / 在吗
//   identity: 你是谁 / 你能做什么 / 介绍一下自己
//   chitchat: 今天星期几 / 讲个笑话 / 随便聊聊（含"今天/现在+时间查询"）
//   meta:     我的历史 / 刚才说了什么
//
// 不命中时返回 ("", false)，交给 LLM 做更精细的意图判定。
func matchLocalIntent(raw string) (string, bool) {
	q := strings.TrimSpace(strings.ToLower(raw))
	if q == "" {
		return intentQuestion, true
	}

	// ── greeting ──
	greetingRegex := `^(你好|您好|hi+|hello+|嗨|哈喽|在吗|在不在|早|早上好|下午好|晚上好|晚安|早安|午安|晚安)$`
	if matchRegex(greetingRegex, q) {
		return intentGreeting, true
	}

	// ── identity ──
	identityRegex := `^(你是谁|你是谁呀|你叫什么|你叫什么名字|你能做什么|你能干什么|你是干什么的|介绍一下你自己|自我介绍|你是什么模型|你是什么)$`
	if matchRegex(identityRegex, q) {
		return intentIdentity, true
	}

	// ── chitchat（闲聊 + 系统信息查询，LLM 容易误识别成 question 的场景）──
	// 时间日期类
	timeRegex := `(今天|现在|当前|明天|后天)+(星期几|礼拜几|几号|多少号|日期|几号了|几点|几点钟|时间|日期是)`
	// 纯闲聊类
	chitchatRegex := `^(讲个笑话|来个笑话|随便聊聊|聊聊呗|聊聊天|说点什么|有什么好玩的|今天天气怎么样|天气怎么样|心情不好|我心情不好|安慰一下我|夸夸我)$`
	if matchRegex(timeRegex, q) || matchRegex(chitchatRegex, q) {
		return intentChitchat, true
	}

	// ── meta ──
	metaRegex := `(我的历史|聊天记录|你刚才说了什么|刚才说的什么|上一个问题|前一个问题|回顾对话|我们聊了什么|你还记得|之前说的)`
	if matchRegex(metaRegex, q) {
		return intentMeta, true
	}

	return "", false
}

// matchRegex 简单的正则匹配封装，避免每次都 re.Compile
var (
	reGreeting  = regexp.MustCompile(`^(你好|您好|hi+|hello+|嗨|哈喽|在吗|在不在|早|早上好|下午好|晚上好|晚安|早安|午安|晚安)$`)
	reIdentity  = regexp.MustCompile(`^(你是谁|你是谁呀|你叫什么|你叫什么名字|你能做什么|你能干什么|你是干什么的|介绍一下你自己|自我介绍|你是什么模型|你是什么)$`)
	reTimeInfo   = regexp.MustCompile(`(今天|现在|当前|明天|后天)+(星期几|礼拜几|几号|多少号|日期|几号了|几点|几点钟|时间|日期是)`)
	reChitchat   = regexp.MustCompile(`^(讲个笑话|来个笑话|随便聊聊|聊聊呗|聊聊天|说点什么|有什么好玩的|今天天气怎么样|天气怎么样|心情不好|我心情不好|安慰一下我|夸夸我)$`)
	reMeta       = regexp.MustCompile(`(我的历史|聊天记录|你刚才说了什么|刚才说的什么|上一个问题|前一个问题|回顾对话|我们聊了什么|你还记得|之前说的)`)
)

func matchRegex(pattern string, q string) bool {
	switch pattern {
	case `^(你好|您好|hi+|hello+|嗨|哈喽|在吗|在不在|早|早上好|下午好|晚上好|晚安|早安|午安|晚安)$`:
		return reGreeting.MatchString(q)
	case `^(你是谁|你是谁呀|你叫什么|你叫什么名字|你能做什么|你能干什么|你是干什么的|介绍一下你自己|自我介绍|你是什么模型|你是什么)$`:
		return reIdentity.MatchString(q)
	case `(今天|现在|当前|明天|后天)+(星期几|礼拜几|几号|多少号|日期|几号了|几点|几点钟|时间|日期是)`:
		return reTimeInfo.MatchString(q)
	case `^(讲个笑话|来个笑话|随便聊聊|聊聊呗|聊聊天|说点什么|有什么好玩的|今天天气怎么样|天气怎么样|心情不好|我心情不好|安慰一下我|夸夸我)$`:
		return reChitchat.MatchString(q)
	case `(我的历史|聊天记录|你刚才说了什么|刚才说的什么|上一个问题|前一个问题|回顾对话|我们聊了什么|你还记得|之前说的)`:
		return reMeta.MatchString(q)
	default:
		return false
	}
}

// doRewriteWithLLM 调 LLM 做改写，失败时 fallback 原始 query。
// 返回 (rewritten, intent, keywords, skipRetrieve, needClarify, clarifyQuestion, clarifyOptions)
//
// 优化：先本地快速意图匹配（0ms，覆盖问候/身份/闲聊/系统查询等常见场景），
// 命中后直接返回，省掉 LLM 调用。只有本地判定为 question 或不确定时才调 LLM。
func doRewriteWithLLM(ctx context.Context, input *quickGraphInput) (string, string, []string, bool, bool, string, []string) {
	// ── Step 0: 本地快速意图匹配（0ms） ──
	if localIntent, ok := matchLocalIntent(input.OriginalQuery); ok {
		skip := localIntent == intentGreeting || localIntent == intentChitchat || localIntent == intentIdentity || localIntent == intentMeta
		logger.Infof("[意图识别-本地] original=%q → intent=%s, skipRetrieve=%v, cost=0ms",
			input.OriginalQuery, localIntent, skip)
		return input.OriginalQuery, localIntent, nil, skip, false, "", nil
	}

	// ── Step 1: 本地没命中 → 调 LLM ──
	cm, ok := graphChatModelFromContext(ctx)
	if !ok || cm == nil {
		logger.Warnf("quickRewriteFn: context 中没有 ChatModel，跳过改写")
		return input.OriginalQuery, intentQuestion, nil, false, false, "", nil
	}

	// 2. 从 InputMsgs 提取最近几轮用户-助手历史（排除 system 和当前问题）
	historyStr := buildRewriteHistory(input.InputMsgs, input.UserQuestionIndex, rewriteMaxHistoryRounds)

	// 3. 构造改写请求消息
	var userContent strings.Builder
	userContent.WriteString("原始问题：")
	userContent.WriteString(input.OriginalQuery)
	if historyStr != "" {
		userContent.WriteString("\n\n对话历史：\n")
		userContent.WriteString(historyStr)
	}

	msgs := []*schema.Message{
		schema.SystemMessage(rewriteSystemPrompt),
		schema.UserMessage(userContent.String()),
	}

	// 4. 同步调 Generate（改写不需要流式）
	msg, err := cm.Generate(ctx, msgs)
	if err != nil || msg == nil || msg.Content == "" {
		logger.Warnf("quickRewriteFn: LLM 改写失败，fallback 原始 query: err=%v", err)
		return input.OriginalQuery, intentQuestion, nil, false, false, "", nil
	}

	// 5. 解析 JSON 返回
	var result rewriteResult
	content := strings.TrimSpace(msg.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	if err := json.Unmarshal([]byte(content), &result); err != nil {
		logger.Warnf("quickRewriteFn: LLM 改写返回 JSON 解析失败，fallback 原始 query: err=%v, content=%s", err, content)
		return input.OriginalQuery, intentQuestion, nil, false, false, "", nil
	}

	// 6. 清洗 + 验证
	if strings.TrimSpace(result.Rewritten) == "" {
		result.Rewritten = input.OriginalQuery
	}
	if !isValidIntent(result.Intent) {
		result.Intent = intentQuestion
	}

	// 7. 判定是否跳过检索
	// greeting/chitchat → 无需知识库，直接闲聊
	// identity → "你是谁/你能做什么"，System Prompt 里已定义，不需要检索
	// meta → "我的历史记录/你刚才说了什么"，属于会话层，不走知识检索
	skipRetrieve := result.Intent == intentGreeting ||
		result.Intent == intentChitchat ||
		result.Intent == intentIdentity ||
		result.Intent == intentMeta

	// 8. 澄清检查: need_clarify=true 且有 question 才生效
	needClarify := result.NeedClarify && strings.TrimSpace(result.ClarifyQuestion) != ""
	if needClarify {
		skipRetrieve = true // 需要澄清时也跳过检索
	}

	return result.Rewritten, result.Intent, result.Keywords, skipRetrieve, needClarify, result.ClarifyQuestion, result.ClarifyOptions
}

// isValidIntent 检查 LLM 返回的意图是否在合法枚举内
func isValidIntent(intent string) bool {
	switch intent {
	case intentGreeting, intentChitchat, intentQuestion, intentIdentity, intentMeta:
		return true
	}
	return false
}

// buildRewriteHistory 从 InputMsgs 提取最近 N 轮 user-assistant 对话（排除 system 和当前问题）。
// maxRounds 控制最大轮数，避免改写 prompt 太长。
func buildRewriteHistory(msgs []*schema.Message, currentUserMsgIdx, maxRounds int) string {
	if len(msgs) == 0 {
		return ""
	}
	// 从 currentUserMsgIdx 往前找，跳过 system，收集 user+assistant 对
	// 简化实现：找最近 maxRounds*2 条非 system 消息，倒序输出
	var pairs []string
	for i := currentUserMsgIdx - 1; i >= 0 && len(pairs) < maxRounds*2; i-- {
		m := msgs[i]
		if m == nil || m.Content == "" {
			continue
		}
		role := string(m.Role)
		if role == "system" {
			continue
		}
		// user 和 assistant 交替收集，用最近的优先
		roleLabel := "用户"
		if role == "assistant" {
			roleLabel = "助手"
		}
		pairs = append([]string{fmt.Sprintf("%s：%s", roleLabel, m.Content)}, pairs...)
	}
	if len(pairs) == 0 {
		return ""
	}
	// 只取 maxRounds*2 条（即 maxRounds 轮）
	if len(pairs) > maxRounds*2 {
		pairs = pairs[len(pairs)-maxRounds*2:]
	}
	return strings.Join(pairs, "\n")
}

// rewriteParallelMaxWait Retrieve 阶段等待后台 LLM 改写的最长时间。
// 原始 query 检索通常 0.3-0.8s，LLM 改写通常 0.5-2s——等 500ms 给快模型一个机会，
// 超时就放弃，不阻塞主路径。
const rewriteParallelMaxWait = 500 * time.Millisecond

// addQuickRetrieveNode 节点 2：Retrieve
// 改进：用 LambdaNode 替代 AddRetrieverNode，在 Lambda 内部提前检查 SkipRetrieve / NeedClarify，
// 避免 EinoRetrieverAdapter 被实例化后才被 PostHandler 清空——那样知识库查询的开销已经花出去了。
func addQuickRetrieveNode(g *einoCompose.Graph[*quickGraphInput, *schema.StreamReader[*schema.Message]], einoRetriever *rag.EinoRetrieverAdapter) error {
	return g.AddLambdaNode(graphQuickNodeRetrieve,
		einoCompose.InvokableLambda(func(ctx context.Context, query string) ([]*schema.Document, error) {
			var state *quickGraphState
			if err := einoCompose.ProcessState(ctx, func(_ context.Context, s *quickGraphState) error {
				state = s
				return nil
			}); err != nil || state == nil {
				return nil, apperrors.NewDefault(apperrors.CodeInternalError)
			}

			// 提前短路：Rewrite 阶段已判定不需要检索 → 不查知识库，直接返回空 docs
			if state.SkipRetrieve || state.NeedClarify {
				state.RetrievedDocs = nil
				return nil, nil
			}

			// 构造 retriever.Option（KBIDs / UserID / TopK）
			opts := buildRetrieverOpts(state.Input)

			// 用当前 query（Rewrite 返回的原始或改写后 query）先查
			docs, err := einoRetriever.Retrieve(ctx, query, opts...)
			if err != nil {
				logger.Warnf("quickRetrieveFn: 检索失败，降级为空结果: %v", err)
				state.RetrievedDocs = nil
				return nil, nil
			}
			state.RetrievedDocs = docs

			// 等后台 LLM 改写（最多 rewriteParallelMaxWait），改写完成后补一次检索 + 合并去重
			if state.RewriteDone != nil {
				select {
				case <-state.RewriteDone:
					if state.RewrittenQuery != "" && state.RewrittenQuery != state.Input.OriginalQuery {
						if !state.SkipRetrieve && !state.NeedClarify {
							rewrittenDocs, rErr := einoRetriever.Retrieve(ctx, state.RewrittenQuery, opts...)
							if rErr == nil && len(rewrittenDocs) > 0 {
								docs = mergeDocsByScore(docs, rewrittenDocs)
								state.RetrievedDocs = docs
							}
						}
					}
				case <-time.After(rewriteParallelMaxWait):
				}
			}
			return docs, nil
		}),
		einoCompose.WithNodeName("KnowledgeRetrieve"),
	)
}

// buildRetrieverOpts 从 quickGraphInput 构造 retriever.Option 切片，
// 替代之前 quickRetrieverCallOpts 通过 einoCompose.WithRetrieverOption 注入的方式。
func buildRetrieverOpts(input *quickGraphInput) []retriever.Option {
	var opts []retriever.Option
	if input != nil {
		if len(input.KnowledgeBaseIDs) > 0 {
			opts = append(opts, rag.WithKnowledgeBaseIDs(input.KnowledgeBaseIDs))
		}
		if input.UserID != "" {
			opts = append(opts, rag.WithUserID(input.UserID))
		}
	}
	if cfg := config.Get(); cfg != nil && cfg.RAG.TopK > 0 {
		opts = append(opts, retriever.WithTopK(cfg.RAG.TopK))
	}
	return opts
}

// mergeDocsByScore 合并两组 docs，按 ID 去重取最高分，最后按分数降序。
func mergeDocsByScore(a, b []*schema.Document) []*schema.Document {
	scoreMap := make(map[string]*schema.Document, len(a)+len(b))
	for _, d := range a {
		if d == nil {
			continue
		}
		scoreMap[d.ID] = d
	}
	for _, d := range b {
		if d == nil {
			continue
		}
		if existing, ok := scoreMap[d.ID]; ok {
			if d.Score() > existing.Score() {
				scoreMap[d.ID] = d
			}
		} else {
			scoreMap[d.ID] = d
		}
	}
	merged := make([]*schema.Document, 0, len(scoreMap))
	for _, d := range scoreMap {
		merged = append(merged, d)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score() > merged[j].Score()
	})
	return merged
}

// addQuickBuildMsgsNode 节点 3：BuildPromptMessages。
// 从 State 拿 Input，在 userQuestionIndex 前插入检索上下文。
func addQuickBuildMsgsNode(g *einoCompose.Graph[*quickGraphInput, *schema.StreamReader[*schema.Message]]) error {
	return g.AddLambdaNode(graphQuickNodeBuildMsgs,
		einoCompose.InvokableLambda(quickBuildMsgsFn),
		einoCompose.WithNodeName("BuildPromptMessages"),
	)
}
// quickBuildMsgsFn 节点 3 实现：在用户问题前插入检索上下文块

func quickBuildMsgsFn(ctx context.Context, docs []*schema.Document) ([]*schema.Message, error) {
	var (
		input          *quickGraphInput
		rewrittenQuery string
	)
	if err := einoCompose.ProcessState(ctx, func(_ context.Context, state *quickGraphState) error {
		input = state.Input
		rewrittenQuery = state.RewrittenQuery
		return nil
	}); err != nil || input == nil {
		return nil, apperrors.NewDefault(apperrors.CodeInternalError)
	}

	// 用 RewrittenQuery 替换用户问题（如果有改写结果）
	questionContent := input.OriginalQuery
	if rewrittenQuery != "" && rewrittenQuery != input.OriginalQuery {
		questionContent = rewrittenQuery
	}

	msgs := make([]*schema.Message, 0, len(input.InputMsgs)+2)
	injected := false
	for i, m := range input.InputMsgs {
		if i == input.UserQuestionIndex && len(docs) > 0 {
			block := buildDocsContextBlock(docs, input.RetrievalBudget, input.ModelName)
			msgs = append(msgs, schema.UserMessage(block))
			injected = true
		}
		// 替换 UserQuestion 位置的内容为改写后的 query
		if i == input.UserQuestionIndex {
			msgs = append(msgs, schema.UserMessage(questionContent))
		} else {
			msgs = append(msgs, m)
		}
	}
	if len(docs) > 0 && !injected {
		last := msgs[len(msgs)-1]
		block := buildDocsContextBlock(docs, input.RetrievalBudget, input.ModelName)
		msgs = append(append(msgs[:len(msgs)-1], schema.UserMessage(block)), last)
	}
	return msgs, nil
}

// addQuickGenerateNode 节点 4：ChatModelGenerate。
// 用 InvokableLambda 而非 StreamableLambda，因为返回值是 StreamReader 本身，
// StreamableLambda 会推断 O 为流内元素 Message，和 END 期望的 StreamReader[Message] 类型不匹配。
func addQuickGenerateNode(g *einoCompose.Graph[*quickGraphInput, *schema.StreamReader[*schema.Message]]) error {
	return g.AddLambdaNode(graphQuickNodeGenerate,
		einoCompose.InvokableLambda(quickGenerateFn),
		einoCompose.WithNodeName("ChatModelGenerate"),
	)
}

// quickGenerateFn 节点 4 实现：调用 ChatModel 流式生成回复
func quickGenerateFn(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
	var cm einoModel.BaseChatModel
	var modelName string
	if err := einoCompose.ProcessState(ctx, func(_ context.Context, state *quickGraphState) error {
		if state.Input == nil {
			return errors.New("nil input in state")
		}
		cm, _ = graphChatModelFromContext(ctx)
		if state.Input != nil {
			modelName = state.Input.ModelName
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if cm == nil {
		return nil, apperrors.NewDefault(apperrors.CodeInternalError)
	}
	// 写 Generate span 的输入 attrs
	var (
		promptTokensEst int
		lastUser        = findLastMessageByRole(msgs, "user")
		firstSystem     = findFirstMessageByRole(msgs, "system")
	)
	// 粗估 prompt tokens（流式不返回 usage）
	if modelName == "" {
		modelName = "cl100k_base"
	}
	for _, m := range msgs {
		if m != nil && m.Content != "" {
			promptTokensEst += tokenutil.CountTokens(m.Content, modelName)
		}
	}
	inAttrs := observability.Attrs{
		"messages_n":    len(msgs),
		"prompt_tokens": promptTokensEst,
		"model_id":      modelName,
	}
	if lastUser != nil && lastUser.Content != "" {
		inAttrs["last_user_msg_preview"] = lastUser.Content
	}
	if firstSystem != nil && firstSystem.Content != "" {
		inAttrs["system_prompt_preview"] = firstSystem.Content
	}
	observability.SetSpanAttrs(ctx, inAttrs)
	sr, err := cm.Stream(ctx, msgs)
	if err != nil {
		return nil, err
	}
	return sr, nil
}

// findLastMessageByRole 找 msgs 中指定 role 的最后一条消息
func findLastMessageByRole(msgs []*schema.Message, role string) *schema.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i] != nil && string(msgs[i].Role) == role {
			return msgs[i]
		}
	}
	return nil
}

// findFirstMessageByRole 找 msgs 中指定 role 的第一条消息
func findFirstMessageByRole(msgs []*schema.Message, role string) *schema.Message {
	for _, m := range msgs {
		if m != nil && string(m.Role) == role {
			return m
		}
	}
	return nil
}

// registerQuickGraphEdges 按 4 节点流水线一次性注册 5 条边
func registerQuickGraphEdges(g *einoCompose.Graph[*quickGraphInput, *schema.StreamReader[*schema.Message]]) error {
	edges := [][2]string{
		{einoCompose.START, graphQuickNodeRewrite},
		{graphQuickNodeRewrite, graphQuickNodeRetrieve},
		{graphQuickNodeRetrieve, graphQuickNodeBuildMsgs},
		{graphQuickNodeBuildMsgs, graphQuickNodeGenerate},
		{graphQuickNodeGenerate, einoCompose.END},
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			return fmt.Errorf("edge %s→%s: %w", e[0], e[1], err)
		}
	}
	return nil
}

// buildDocsContextBlock 按 score 占比分配 token 预算，格式化检索文档为引用上下文。
func buildDocsContextBlock(docs []*schema.Document, retrievalBudget int, modelName string) string {
	if len(docs) == 0 {
		return ""
	}
	// 预算非法时退化
	if retrievalBudget <= 0 {
		retrievalBudget = 2000
	}
	if modelName == "" {
		modelName = "cl100k_base"
	}

	header := "以下是知识库返回的参考资料（可能与问题相关）：\n" +
		"回答时必须把对应引用块的 chunk_id 插到对应句末，格式为 <kb doc=\"文档名\" chunk_id=\"chunk_id\" />。\n" +
		"【禁止】直接复制参考资料原文作为回答。\n\n"
	headerTokens := tokenutil.CountTokens(header, modelName)

	var sb strings.Builder
	sb.WriteString(header)

	// 按 score 排序 + 分配配额
	type scored struct {
		idx   int
		score float64
		title string
	}
	scoredDocs := make([]scored, 0, len(docs))
	totalScore := 0.0
	for i, d := range docs {
		title := ""
		if d.MetaData != nil {
			if v, ok := d.MetaData[rag.MetaTitle()].(string); ok {
				title = v
			}
		}
		// eino Score() 一般在 [0,1]，加 1e-6 避免除零
		s := d.Score()
		if s <= 0 {
			s = 1e-6
		}
		totalScore += s
		scoredDocs = append(scoredDocs, scored{idx: i, score: s, title: title})
	}
	// 降序：高分段先分配
	sort.Slice(scoredDocs, func(i, j int) bool {
		return scoredDocs[i].score > scoredDocs[j].score
	})

	perDocHeadBudget := 60 // chunk_id/score/文档名 行的粗估
	remainingBudget := retrievalBudget - headerTokens
	if remainingBudget < 200 {
		// 预算太少时直接按 rune 粗截
		sb2 := strings.Builder{}
		sb2.WriteString(header)
		for i, d := range docs {
			title := ""
			if d.MetaData != nil {
				if v, ok := d.MetaData[rag.MetaTitle()].(string); ok {
					title = v
				}
			}
			sb2.WriteString(fmt.Sprintf("--- 参考 %d [chunk_id=%s] [score=%.3f] ---\n", i+1, d.ID, d.Score()))
			if title != "" {
				sb2.WriteString(fmt.Sprintf("文档名：%s\n", title))
			}
			cut, _ := tokenutil.TruncateByTokens(d.Content, modelName, remainingBudget/(len(docs)+1))
			sb2.WriteString(cut)
			sb2.WriteString("\n\n")
		}
		return sb2.String()
	}

	// 按比例分配内容 token
	headTokensAll := perDocHeadBudget * len(docs)
	contentBudget := remainingBudget
	if contentBudget > headTokensAll+100 {
		contentBudget -= headTokensAll
	}
	const minContentPerDoc = 120

	for i, sd := range scoredDocs {
		d := docs[sd.idx]
		headStr := fmt.Sprintf("--- 参考 %d [chunk_id=%s] [score=%.3f] ---\n", sd.idx+1, d.ID, d.Score())
		if sd.title != "" {
			headStr += fmt.Sprintf("文档名：%s\n", sd.title)
		}
		sb.WriteString(headStr)
		headUsed := tokenutil.CountTokens(headStr, modelName)

		ratio := sd.score / totalScore
		bonus := 1.0
		if i == 0 {
			bonus = 1.15
		}
		share := int(float64(contentBudget) * ratio * bonus)
		if share < minContentPerDoc {
			share = minContentPerDoc
		}
		shareForContent := share - (headUsed - perDocHeadBudget)
		if shareForContent < minContentPerDoc/2 {
			shareForContent = minContentPerDoc / 2
		}
		if shareForContent > remainingBudget {
			shareForContent = remainingBudget
		}
		if shareForContent <= 0 {
			sb.WriteString("\n")
			continue
		}

		cut, used := tokenutil.TruncateByTokens(d.Content, modelName, shareForContent)
		sb.WriteString(cut)
		sb.WriteString("\n\n")

		usedTotal := headUsed + used
		if usedTotal > remainingBudget {
			remainingBudget = 0
		} else {
			remainingBudget -= usedTotal
		}
		if usedTotal+perDocHeadBudget > contentBudget {
			contentBudget = 0
		} else {
			contentBudget -= usedTotal + perDocHeadBudget
		}
		if remainingBudget < minContentPerDoc {
			break
		}
	}
	return sb.String()
}

// graphCtxChatModelKey 用作 context.WithValue 的 key，存放 per-request 的 ChatModel。
type graphCtxChatModelKeyType struct{}

var graphCtxChatModelKey = graphCtxChatModelKeyType{}

// withGraphChatModel 把 ChatModel 注入 context
func withGraphChatModel(ctx context.Context, cm einoModel.BaseChatModel) context.Context {
	return context.WithValue(ctx, graphCtxChatModelKey, cm)
}
// graphChatModelFromContext 从 context 取出 ChatModel

func graphChatModelFromContext(ctx context.Context) (einoModel.BaseChatModel, bool) {
	v := ctx.Value(graphCtxChatModelKey)
	if v == nil {
		return nil, false
	}
	cm, ok := v.(einoModel.BaseChatModel)
	return cm, ok
}

// processMessageGraphQuick 快速模式入口：QueryRewrite → Retrieve → BuildPrompt → Generate 四节点 Graph。
func (s *chatService) processMessageGraphQuick(
	ctx context.Context,
	userID, sessionID, userMsgID string,
	req requestdto.SendMessageRequest,
	eventCh chan<- dto.StreamEvent,
) {
	// 根 Span + panic recover（单独抽成 startQuickSpan 更清爽）
	ctx, span, obsOk := startQuickSpan(ctx, s.obs, sessionID, userID, req.ModelID)
	if obsOk {
		defer func() {
			status := observability.SpanStatusOK
			var errVal error
			if r := recover(); r != nil {
				status = observability.SpanStatusError
				errVal = fmt.Errorf("panic: %v", r)
				eventCh <- dto.StreamEvent{Type: "error", Detail: "处理过程中发生未预期错误", Done: true}
			}
			s.obs.EndSpan(ctx, span, status, errVal, nil)
		}()
		s.obs.Incr(ctx, "chat_quick_graph_requests_total", map[string]string{"model_id": req.ModelID}, 1)
	}

	// 1) 初始化上下文（历史/摘要/记忆/画像/预算）
	sendProgressEvent(eventCh, "正在加载上下文...")
	t0 := time.Now()
	client, enhancedCtx, err := s.initContext(ctx, userID, sessionID, req.ModelID, req.ModelType, req.Content)
	if err != nil {
		quickIncrError(ctx, s.obs, obsOk, "init_ctx")
		if obsOk {
			s.obs.MarkTraceError(ctx, err)
		}
		sendErrorEvent(eventCh, err, err.Error())
		return
	}
	chatModel := client.ChatModel()
	if obsOk {
		s.obs.Observe(ctx, "chat_quick_graph_init_ctx_seconds", map[string]string{"model_id": req.ModelID}, time.Since(t0).Seconds())
	}

	// 2~3) 组装 Graph Input：System Prompt / History / 模型名 / 检索预算
	graphInput := buildQuickInput(req, userID, userMsgID, enhancedCtx, client)

	// 3.5) 预执行 Rewrite + 澄清检查：needClarify=true 时短路返回，不浪费后续节点
	rewriteCheckCtx := withGraphChatModel(ctx, chatModel)
	rewriteStart := time.Now()
	rewritten, intent, keywords, skipRetrieve, needClarify, clarifyQuestion, clarifyOptions := doRewriteWithLLM(rewriteCheckCtx, graphInput)
	logger.Infof("[意图识别] original=%q → intent=%s, skipRetrieve=%v, needClarify=%v, rewritten=%q, keywords=%v, cost=%dms",
		req.Content, intent, skipRetrieve, needClarify, rewritten, keywords, time.Since(rewriteStart).Milliseconds())

	if needClarify {
		// 存 PendingClarify 到 session
		pendingData, _ := json.Marshal(entity.PendingClarifyData{
			Question: clarifyQuestion,
			Options:  clarifyOptions,
			SetAt:    time.Now(),
		})
		if err := s.sessionRepo.SetPendingClarify(ctx, sessionID, pendingData); err != nil {
			logger.Warnf("存储澄清追问状态失败: %v", err)
		}
		// 存一条 assistant 消息（追问），让历史自然串成 [user问题 → assistant追问 → user回答]
		clarifyMsgID := uuid.New().String()
		if err := s.saveAssistantMessage(ctx, sessionID, clarifyMsgID, clarifyQuestion, req, nil, nil); err != nil {
			logger.Warnf("存储澄清追问消息失败: %v", err)
		}
		obsNow := time.Now()
		eventCh <- dto.StreamEvent{Type: "clarify", Clarify: &dto.ClarifyPayload{
			Question: clarifyQuestion,
			Options:  clarifyOptions,
		}, Done: true}
		if obsOk {
			s.obs.EndSpan(ctx, span, observability.SpanStatusOK, nil, observability.Attrs{
				"need_clarify": "true",
				"clarify_intent": intent,
				"clarify_ms": fmt.Sprintf("%d", time.Since(obsNow).Milliseconds()),
			})
		}
		return
	}

	// 不需要澄清 → 把 rewrite 结果填到 graphInput，让 Graph 内 Rewrite 节点快速复用
	graphInput.PreRewrittenQuery = rewritten
	graphInput.PreIntent = intent
	graphInput.PreKeywords = keywords
	graphInput.PreSkipRetrieve = skipRetrieve

	// 4) 提前创建 graphState：既是 eino stateGenerator 返回值，也是 Invoke 后外部读取 RetrievedDocs 的入口。
	// RewriteDone channel 在这里初始化——rewriteFn 后台 goroutine 完成后 close 它，retrieve StatePostHandler 等它。
	graphState := &quickGraphState{RewriteDone: make(chan struct{})}

	// 5) 构建并编译 compose.Graph（内部已经 push error 事件）
	sendProgressEvent(eventCh, "正在组装快速检索链路...")
	graphCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	runnable, err := compileQuickGraphLocal(graphState, s.einoRetriever, req.ModelID, s.obs, obsOk, eventCh)
	if err != nil {
		if obsOk {
			s.obs.MarkTraceError(ctx, err)
		}
		return
	}

	// 6) 注入 per-request ChatModel + Retriever 节点选项
	graphCtx = withGraphChatModel(graphCtx, chatModel)
	callOpts := quickRetrieverCallOpts(req, userID)

	// 7) 生成助手消息 ID + 流式驱动 Graph 执行
	assistantMsgID := uuid.New().String()
	if obsOk {
		s.obs.AddRootAttrs(ctx, observability.Attrs{"assistant_message_id": assistantMsgID})
	}
	eventCh <- dto.StreamEvent{Type: "start", MessageID: assistantMsgID}

	fullContent, err := runQuickStream(
		graphCtx, runnable, graphInput, callOpts,
		eventCh, req.ModelID, assistantMsgID, s.obs, obsOk,
	)
	if err != nil {
		if obsOk {
			s.obs.MarkTraceError(ctx, err)
		}
		llmpkg.ReduceContextBudgetOnError(req.ModelID, err)
		return
	}

	// 8) 直接从 graphState 读 RetrievedDocs（genState 返回的就是这个对象，Invoke 内部 StatePostHandler 写的就是它）
	var (
		sources   []dto.SourceInfo
		docsCount int
	)
	if len(graphState.RetrievedDocs) > 0 {
		sources = einoDocsToSourceInfos(graphState.RetrievedDocs)
		docsCount = len(graphState.RetrievedDocs)
	}
	if obsOk {
		s.obs.AddRootAttrs(ctx, observability.Attrs{
			"assistant_chars": fmt.Sprintf("%d", len([]rune(fullContent))),
			"retrieved_docs":  fmt.Sprintf("%d", docsCount),
		})
	}

	// 8) 结束事件 + 异步落库 + 异步刷新摘要记忆
	s.emitDoneAndSave(eventCh, sessionID, assistantMsgID, fullContent, req, sources, nil, func(meta map[string]any) {
		if obsOk && meta != nil {
			meta["trace_id"] = observability.TraceIDFromContext(ctx)
			meta["eino_quick_graph_mode"] = true
		}
	})
	s.refreshContextAsync(ctx, userID, sessionID, enhancedCtx.History, chatModel)
}

// startQuickSpan 创建根 Span，返回 (newCtx, span, ok)，避免主流程写一长串可观测性样板
func startQuickSpan(ctx context.Context, obs observability.Recorder, sessionID, userID, modelID string) (context.Context, *observability.Span, bool) {
	if obs == nil {
		return ctx, nil, false
	}
	newCtx, span := obs.StartSpan(ctx, "chat.quick.graph", observability.ComponentAgentEngine, observability.Attrs{
		"session_id":  sessionID,
		"user_id":     userID,
		"model_id":    modelID,
		"search_mode": "quick_graph",
	})
	if span == nil {
		return ctx, nil, false
	}
	return newCtx, span, true
}

// buildQuickInput 组装 quickGraphInput：System Prompt / History / 预算 / 模型名
func buildQuickInput(
	req requestdto.SendMessageRequest,
	userID, userMsgID string,
	enhancedCtx *EnhancedContext,
	client *llmpkg.OpenAIClient,
) *quickGraphInput {
	history := excludeByMessageID(enhancedCtx.History, userMsgID)
	pb := NewPromptBuilder(PromptModeQuick, quickModeAgentSystemPrompt, enhancedCtx.Summary, enhancedCtx.Memories, enhancedCtx.UserCtx).
		WithProfile(enhancedCtx.Profile).
		WithPreference(enhancedCtx.Preference)
	inputMsgs := append([]*schema.Message{schema.SystemMessage(pb.BuildSystem())}, pb.BuildHistory(history)...)
	inputMsgs = append(inputMsgs, schema.UserMessage(req.Content))
	return &quickGraphInput{
		OriginalQuery:     req.Content,
		UserID:            userID,
		KnowledgeBaseIDs:  req.KnowledgeBaseIDs,
		InputMsgs:         inputMsgs,
		UserQuestionIndex: len(inputMsgs) - 1,
		ModelName:         client.ModelName(),
		RetrievalBudget:   enhancedCtx.RetrievalBudget,
	}
}

// compileQuickGraphLocal build + compile graph，失败时自动推 error 事件
// graphState 在调用处提前创建好，此函数会把它传给 buildQuickGraph，使其成为 eino stateGenerator 的返回值。
// 这样 Invoke 返回后外部直接读 graphState.RetrievedDocs 即可，不需要再从 context 里 ProcessState。
func compileQuickGraphLocal(
	graphState *quickGraphState,
	einoRetriever *rag.EinoRetrieverAdapter,
	modelID string,
	obs observability.Recorder,
	obsOk bool,
	eventCh chan<- dto.StreamEvent,
) (einoCompose.Runnable[*quickGraphInput, *schema.StreamReader[*schema.Message]], error) {
	g, err := buildQuickGraph(graphState, einoRetriever)
	if err != nil {
		quickIncrError(nil, obs, obsOk, "build_graph")
		sendErrorEvent(eventCh, err, "快速检索链路初始化失败")
		return nil, err
	}
	r, err := g.Compile(nil, einoCompose.WithGraphName("quick_rag_pipeline"))
	if err != nil {
		quickIncrError(nil, obs, obsOk, "compile_graph")
		sendErrorEvent(eventCh, err, "快速检索链路编译失败")
		return nil, err
	}
	return r, nil
}

// runQuickStream 驱动 Graph 执行并消费流式输出
func runQuickStream(
	graphCtx context.Context,
	runnable einoCompose.Runnable[*quickGraphInput, *schema.StreamReader[*schema.Message]],
	graphInput *quickGraphInput,
	callOpts []einoCompose.Option,
	eventCh chan<- dto.StreamEvent,
	modelID, assistantMsgID string,
	obs observability.Recorder,
	obsOk bool,
) (string, error) {
	sendProgressEvent(eventCh, "正在执行快速检索链路...")
	t0 := time.Now()
	reader, invErr := runnable.Invoke(graphCtx, graphInput, callOpts...)
	if obsOk {
		obs.Observe(graphCtx, "chat_quick_graph_run_seconds", map[string]string{"model_id": modelID}, time.Since(t0).Seconds())
	}
	if invErr != nil {
		sendErrorEvent(eventCh, invErr, "快速检索执行失败")
		return "", invErr
	}
	if reader == nil {
		sendErrorEvent(eventCh, fmt.Errorf("nil stream reader"), "快速检索未返回结果")
		return "", fmt.Errorf("nil stream reader")
	}
	defer reader.Close()
	return consumeQuickGraphStream(graphCtx, reader, assistantMsgID, eventCh)
}

// consumeQuickGraphStream 消费 ChatModel 输出的 StreamReader[*schema.Message]
// 转成 dto.StreamEvent 推给前端，返回最终完整内容。
func consumeQuickGraphStream(
	ctx context.Context,
	sr *schema.StreamReader[*schema.Message],
	assistantMsgID string,
	eventCh chan<- dto.StreamEvent,
) (string, error) {
	var fullContent string
	var assistantSeen bool
	for {
		msg, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			sendErrorEvent(eventCh, err, "快速检索流式生成失败")
			return "", err
		}
		if msg == nil {
			continue
		}
		if !assistantSeen {
			sendProgressEvent(eventCh, "正在生成回答...")
			assistantSeen = true
		}
		if msg.Content != "" {
			fullContent += msg.Content
			eventCh <- dto.StreamEvent{
				Type:      "content",
				MessageID: assistantMsgID,
				Content:   msg.Content,
			}
		}
		// 快速模式不期望 tool_calls，收到时打 warn 但不报错
		if len(msg.ToolCalls) > 0 {
			logger.Warnf("quick graph 模式收到模型 tool_calls（数量=%d），但快速模式没有挂工具链，已忽略。首条 tool name=%v",
				len(msg.ToolCalls), safeFirstToolName(msg.ToolCalls))
		}
	}
	_ = ctx
	return fullContent, nil
}

// safeFirstToolName 避免空指针
func safeFirstToolName(calls []schema.ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	return calls[0].Function.Name
}

// einoDocsToSourceInfos 把 eino schema.Document 列表转为前端 SourceInfo 结构
func einoDocsToSourceInfos(docs []*schema.Document) []dto.SourceInfo {
	ragDocs := make([]rag.Document, 0, len(docs))
	for _, d := range docs {
		ragDocs = append(ragDocs, rag.EinoDocToRagDoc(d))
	}
	return groupDocumentsToSources(ragDocs)
}

// ---------- 前向声明：可观测性 & 选项小工具（被上面拆分后的子函数调用） ----------

// quickIncrError 快速模式错误计数（避免 if obsOk 到处写）
func quickIncrError(ctx context.Context, obs observability.Recorder, obsOk bool, stage string) {
	if !obsOk {
		return
	}
	obs.Incr(ctx, "chat_quick_graph_errors_total", map[string]string{"stage": stage}, 1)
}

// quickRetrieverCallOpts 现在返回 nil——Retrieve 节点已改为 LambdaNode，
// retriever.Option 通过 buildRetrieverOpts 在 Lambda 内部直接构造。
// 保留函数签名以减少调用处改动。
func quickRetrieverCallOpts(_ requestdto.SendMessageRequest, _ string) []einoCompose.Option {
	return nil
}
