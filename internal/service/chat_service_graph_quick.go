package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/retriever"
	einoCompose "github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"gorm.io/datatypes"

	llmpkg "solvify-agent/internal/llm"
	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/rag"
	"solvify-agent/pkg/config"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/eventch"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/traceid"
)

// quickGraphInput 快速模式 Graph 入参：一次请求内的全部共享上下文。
//
// 它是 Graph 的输入类型，沿边流到每个节点 —— 节点需要的东西**只能**从这里来（含 ChatModel）。
// 旧实现把其中一部分（Graph Local State、ChatModel）塞进 context 再由节点取出来，
// 于是同一份数据有了两条来源。现在只有一条。
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

	// ChatModel 本次请求要用的对话模型。
	//
	// 图结构在启动期编译一次、被所有请求并发复用，所以「用哪个模型」只能是请求级数据，
	// 必须随入参进来。旧实现把它塞进 context 再由节点取出来，取不到时只打一行 warn ——
	// 属于「没接上也看不出来」：注入漏了表现为回答质量下降，而不是报错。
	ChatModel einoModel.BaseChatModel
}

// quickGraphPayload 快速模式 Graph 内**唯一**的数据载体，沿边从一个节点流到下一个。
//
// 节点各写自己那一份字段，下游从同一个对象上读：
//
//	query_rewrite → Rewrite / VectorQuery / KeywordQuery
//	retrieve      → Docs
//	build_msgs    → Msgs
//	generate      → 出参 quickGraphOutput
//
// 「谁负责写哪个字段」在类型上一眼可见，也不需要再往 context 里塞任何东西。
// 旧实现把同一份数据分两条路走（一部分走边、一部分走 Graph Local State 经 ctx 取用），
// 两条路一旦不一致不会报错，只会让下游读到半份旧值。
type quickGraphPayload struct {
	// Input 请求级上下文，全链路只读
	Input *quickGraphInput
	// Rewrite 本次改写的完整产出（意图 / 改写文本 / 澄清诉求），由改写节点写一次。
	//
	// 它是「改写结果」在快速模式链路里的唯一载体，下游各读自己那一部分：
	// 分叉条件读 NeedClarify、检索节点读 SkipRetrieve、拼装节点读 Rewritten。
	// 拆成 7 个平行字段就必须成组、按序赋值，漏写一个不会编译报错、只会静默丢语义。
	// 改写节点永不返回 nil（doRewriteWithLLM 有 fallback 保证），所以下游不判空。
	Rewrite *rewriteResult
	// VectorQuery 向量检索 query：当前问题 + 最近 1~2 轮用户提问（长 query）
	VectorQuery string
	// KeywordQuery 关键字检索 query：指代回填后的短句（关键字打分是命中率，分母=query 词项数）
	KeywordQuery string
	// Docs 检索结果；跳过检索或检索失败时为 nil
	Docs []*schema.Document
	// Msgs 最终发给模型的 prompt
	Msgs []*schema.Message
}

// quickGraphOutput 快速模式 Graph 出参。
//
// 出参必须带上检索到的 docs：调用方要用它拼前端 sources，而「Invoke 之后还需要的东西」
// 只有出参一个出口。旧实现把 docs 留在 Graph Local State 里再由调用方读回来，
// 等于给同一份数据开了第二条通道 —— 也正是 docs 曾经必须依赖 ctx 才拿得到的原因。
//
// ⚠️ 不变式：Clarify 与 Stream 互斥。两条终态路径各只写一个 ——
// generate 写 Stream（Clarify 为 nil），clarify_end 写 Clarify（Stream 为 nil）。
type quickGraphOutput struct {
	// Stream 模型输出流，由调用方消费并关闭
	Stream *schema.StreamReader[*schema.Message]
	// Docs 本次命中的文档；跳过检索/检索失败时为空
	Docs []*schema.Document
	// Clarify 非 nil 表示本次改为向用户追问（改写后就分叉了，没走检索与生成）
	Clarify *quickGraphClarify
}

// quickGraphClarify 「本轮改为向用户追问」这条终态路径的产出。
//
// 只带数据、不带副作用：写 session 的 PendingClarify、落一条 assistant 追问消息、
// 推 SSE 事件都在 Graph 外的入口函数里做 —— 节点保持纯函数，重跑不会重复落库。
type quickGraphClarify struct {
	// Question 追问文本
	Question string
	// Options 追问选项（可选）
	Options []string
	// Intent 触发追问的意图，埋点用
	Intent string
}

// 查询改写意图类型
const (
	intentGreeting = "greeting" // 问候语
	intentChitchat = "chitchat" // 闲聊
	intentQuestion = "question" // 知识查询（默认，最常见）
	intentIdentity = "identity" // 身份确认（你是谁、你能做什么）
	intentMeta     = "meta"     // 元问题（我的历史记录、你刚才说了什么）
	intentRealtime = "realtime" // 实时信息（天气/新闻/行情，本模式无工具，直接告知）
)

// rewriteResult 一次查询改写的完整产出：LLM 返回的 JSON 字段 + 本地推导的派生字段。
//
// 它是「改写结果」在快速模式链路里的唯一载体：由改写节点（quickRewriteFn）算出一次、
// 挂到 quickGraphPayload.Rewrite，下游各读自己需要的那一部分。
//
// 之所以收敛成一个结构体而不是 7 个平行字段：平行字段必须**成组、按序、在多个调用点**赋值，
// 漏写一个不会编译报错、只会静默丢掉语义（「同一个东西有两个来源」的典型温床）。
type rewriteResult struct {
	Rewritten       string   `json:"rewritten"`
	Intent          string   `json:"intent"`
	Keywords        []string `json:"keywords"`
	NeedClarify     bool     `json:"need_clarify"`
	ClarifyQuestion string   `json:"clarify_question,omitempty"`
	ClarifyOptions  []string `json:"clarify_options,omitempty"`

	// SkipRetrieve 由 Intent / NeedClarify 本地推导，不是 LLM 字段（规则见 deriveSkipRetrieve）。
	SkipRetrieve bool `json:"-"`
}

// rewriteMaxHistoryRounds 改写时拼入历史的最大轮数（每轮=user+assistant）
const rewriteMaxHistoryRounds = 3

// rewriteLLMTimeout 是「指代场景」下 LLM 改写的硬超时。
//
// 背景：线上日志里改写耗时 p50=1.87s / p90=10.0s / max=27.05s，而它的产出
// 78% 只是确认本地默认意图、need_clarify 命中 0 次 —— 唯一的不可替代价值是消解指代。
// 所以：只在含指代表达时才调 LLM，且给硬超时兜底（超时后检索 query 由本地实体回填提供，
// 不会像改造前那样「白等满超时再 fallback」）。
const rewriteLLMTimeout = 2 * time.Second

// rewriteSystemPrompt 改写专用的 System Prompt
const rewriteSystemPrompt = `你是一个查询改写助手。根据用户的原始问题和对话历史，对问题进行改写并识别意图。

## 改写规则
1. 消解指代：把"这个"、"那个方案"、"它"、"之前说的"等代词替换为对话历史中的具体名词
2. 扩展关键词：补充与问题相关的同义词、上下位词，方便知识库检索
3. 拆分复合问题：如果原问题包含多个子问题，改写为一个完整句子即可（不要拆成多行）
4. 保持原意：改写后的问题必须和原问题核心意图一致，不要引入新主题

## 意图识别
- greeting: 问候语（你好、hi、在吗、早上好）
- chitchat: 闲聊（讲个笑话、随便聊聊、安慰一下我）
- realtime: 实时信息（天气、气温、最近的新闻、股价、汇率等，必须联网或调工具才能答对）
- question: 知识查询（业务问题、技术问题、需要从知识库找答案）
- identity: 身份确认（你是谁、你能做什么、介绍一下你自己）
- meta: 元问题（我的历史记录、你刚才说了什么、回顾对话）

## 澄清追问判断
当用户问题过于模糊、存在多种理解且无法从历史对话推断真实意图时，设置 need_clarify=true：
- 没有历史上下文时，单个指代性问题（如"那个方案"、"它"）且知识库依赖强 → 追问
- 问题包含可能冲突的关键概念（如"怎么导出数据"未指明导出格式/导出范围）→ 追问
- 用户同时提及多个实体且未指明主体 → 追问
以下情况**不要**追问：
- 打招呼、闲聊、身份、实时类意图（greeting/chitchat/identity/realtime）→ 直接返回原问题
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
	graphQuickNodeClarify   = "clarify_end"
)

// buildQuickGraph 构建
// START → query_rewrite →（分支）→ { retrieve → build_prompt_messages → generate | clarify_end } → END。
//
// 图结构是静态的（5 节点 + 5 条边 + 1 条分支），请求级变量（ChatModel）装在入参
// quickGraphInput 里沿边传递，节点之间不再通过 context 交换数据 ——
// 所以本函数只在启动期跑一次，编译结果被所有请求并发复用，
// 而「并发请求互相串数据」在结构上不可能发生（已经没有跨请求共享的可变对象可串）。
//
// 分叉点放在改写之后：改写一旦判定 need_clarify，这一轮就不该再检索、也不该再生成
// （省掉一次知识库查询 + 一整轮 LLM）。写成图上的分支而不是图外提前 return，
// 是为了让「一次请求可能走哪几条路」全部落在同一个地方 —— 图外只剩副作用。
func buildQuickGraph(
	einoRetriever *rag.EinoRetrieverAdapter,
) (*einoCompose.Graph[*quickGraphInput, *quickGraphOutput], error) {
	g := einoCompose.NewGraph[*quickGraphInput, *quickGraphOutput]()
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
	if err := addQuickClarifyNode(g); err != nil {
		return nil, wrapGraphErr("add clarify node", err)
	}
	// 分支必须在起点节点（rewrite）与两个终点节点都注册好之后再挂
	if err := addQuickClarifyBranch(g); err != nil {
		return nil, wrapGraphErr("add clarify branch", err)
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
func addQuickRewriteNode(g *einoCompose.Graph[*quickGraphInput, *quickGraphOutput]) error {
	return g.AddLambdaNode(graphQuickNodeRewrite,
		einoCompose.InvokableLambda(quickRewriteFn),
		einoCompose.WithNodeName("QueryRewrite"),
	)
}

// quickRewriteFn 节点 1 实现：做一次查询改写（本地规则优先，必要时才调 LLM），
// 并规划双轨检索 query。
//
// 改写**只有这一个调用点**（由 TestDoRewriteHasSingleCallSite 钉住）：图外不再预跑一次，
// 所以「同一次改写跑两遍、其中一遍白等」在结构上不可能发生。
//
// 硬超时兜底：只有含真实指代表达时才会真的调 LLM，且上限 rewriteLLMTimeout ——
// 超时/失败都 fallback 到原问题，检索 query 由本地实体回填提供，不会卡住整条链路。
func quickRewriteFn(ctx context.Context, input *quickGraphInput) (*quickGraphPayload, error) {
	if input == nil {
		return nil, apperrors.NewDefault(apperrors.CodeInvalidParam)
	}

	// 超时只包住这一次改写调用：收敛在节点内，图外不必再管。
	rewriteCtx, cancelRewrite := context.WithTimeout(ctx, rewriteLLMTimeout)
	rewriteStart := time.Now()
	result := doRewriteWithLLM(rewriteCtx, input)
	cancelRewrite()
	rewriteMs := time.Since(rewriteStart).Milliseconds()
	logger.Infof("[意图识别] original=%q → intent=%s, skipRetrieve=%v, needClarify=%v, rewritten=%q, keywords=%v, cost=%dms",
		input.OriginalQuery, result.Intent, result.SkipRetrieve, result.NeedClarify, result.Rewritten, result.Keywords, rewriteMs)

	// 检索 query 双轨规划：本地规则，不依赖 LLM 结果是否可用。
	// 向量侧吃「当前问题 + 最近几轮用户提问」，关键字侧吃「指代回填后的短句」。
	queries := planQueriesFromInput(input, result.Rewritten)

	// 节点输出 = 载荷本身：改写结果与两路 query 挂上去，后面所有节点从同一个对象上读。
	return &quickGraphPayload{
		Input:        input,
		Rewrite:      result,
		VectorQuery:  queries.Vector,
		KeywordQuery: queries.Keyword,
	}, nil
}

// 覆盖五类场景：
//
//	greeting: 你好 / hi / 早上好 / 在吗
//	identity: 你是谁 / 你能做什么 / 介绍一下自己
//	realtime: 今天天气怎么样 / 最近的新闻 / 股价多少（本模式答不了，走「告知」路径）
//	chitchat: 今天星期几 / 讲个笑话 / 随便聊聊（含"今天/现在+时间查询"）
//	meta:     我的历史 / 刚才说了什么
//
// localIntentRules 是本地意图规则表，顺序即优先级：命中即返回。
// 新增规则只需在这里加一行，正则本身统一维护在下方 re* 变量里。
var localIntentRules = []struct {
	intent string
	re     *regexp.Regexp
}{
	{intentGreeting, reGreeting},
	{intentIdentity, reIdentity},
	// realtime 必须排在 chitchat 之前：天气/新闻类问法看起来像闲聊，但答案不在知识库里
	// （随时间变化），本模式又没有实时工具 —— 跑检索只会白花时间再回一句「知识库没有」；
	// 判成 chitchat 更糟：模型会顺着「闲聊」这个标签给你编一个天气出来。
	{intentRealtime, reRealtimeQuery},
	// chitchat 覆盖「闲聊 + 系统信息查询」，都是 LLM 容易误识别成 question 的场景。
	{intentChitchat, reTimeInfo}, // 时间日期类
	{intentChitchat, reChitchat}, // 纯闲聊类
	{intentMeta, reMeta},
}

// matchLocalIntent 用本地规则做意图识别，命中返回 (intent, true)；
// 未命中返回 ("", false)，交由 LLM 判断。
func matchLocalIntent(raw string) (string, bool) {
	q := strings.TrimSpace(strings.ToLower(raw))
	if q == "" {
		return intentQuestion, true
	}
	for _, rule := range localIntentRules {
		if rule.re.MatchString(q) {
			return rule.intent, true
		}
	}
	return "", false
}

// 本地意图匹配用的正则，包级编译一次、全局复用。
//
// reRealtimeQuery 命中「只有实时数据才能答对」的问法（天气/新闻/行情）。本地只负责把它们
// 导到「告知」路径：真正的答案在深度模式，靠工具拿。
var (
	reGreeting      = regexp.MustCompile(`^(你好|您好|hi+|hello+|嗨|哈喽|在吗|在不在|早|早上好|下午好|晚上好|晚安|早安|午安|晚安)$`)
	reIdentity      = regexp.MustCompile(`^(你是谁|你是谁呀|你叫什么|你叫什么名字|你能做什么|你能干什么|你是干什么的|介绍一下你自己|自我介绍|你是什么模型|你是什么)$`)
	reTimeInfo      = regexp.MustCompile(`(今天|现在|当前|明天|后天)+(星期几|礼拜几|几号|多少号|日期|几号了|几点|几点钟|时间|日期是)`)
	reChitchat      = regexp.MustCompile(`^(讲个笑话|来个笑话|随便聊聊|聊聊呗|聊聊天|说点什么|有什么好玩的|心情不好|我心情不好|安慰一下我|夸夸我)$`)
	reRealtimeQuery = regexp.MustCompile(`(天气|气温|温度|下雨|降雨|降水|台风|空气质量|紫外线|最近的新闻|最新新闻|今日新闻|股价|股票行情|汇率|大盘)`)
	reMeta          = regexp.MustCompile(`(我的历史|聊天记录|你刚才说了什么|刚才说的什么|上一个问题|前一个问题|回顾对话|我们聊了什么|你还记得|之前说的)`)
)

// deriveSkipRetrieve 判定「这次请求是否跳过知识库检索」，是全包唯一的规则来源。
//
// greeting/chitchat：无需知识库，直接闲聊
// identity："你是谁/你能做什么"，System Prompt 里已定义，不需要检索
// meta："我的历史记录/你刚才说了什么"，属于会话层，不走知识检索
// realtime：天气/新闻/行情等实时信息，知识库里没有、检索也检索不出来，本模式又没有工具
// ⇒ 跳过检索，让 LLM 按 prompt 的「实时信息处理」段直接告知用户
// needClarify：需要先追问用户，同样不检索
//
// 旧实现把这条规则写在两处（本地命中链路 + LLM 结果链路），其中一处漏掉 needClarify 分支
// 不会报错、只表现为多跑一次检索，所以这里收敛成唯一的函数。
func deriveSkipRetrieve(intent string, needClarify bool) bool {
	switch intent {
	case intentGreeting, intentChitchat, intentIdentity, intentMeta, intentRealtime:
		return true
	}
	return needClarify
}

// rewriteFallback 构造「本次改写无产出」时的兜底结果：回退到原问题 + 指定意图。
// 所有失败/跳过路径都经它返回，这也是 doRewriteWithLLM 永不返回 nil 的实现依据。
func rewriteFallback(input *quickGraphInput, intent string) *rewriteResult {
	return &rewriteResult{
		Rewritten:    input.OriginalQuery,
		Intent:       intent,
		SkipRetrieve: deriveSkipRetrieve(intent, false),
	}
}

// doRewriteWithLLM 做一次查询改写，失败/跳过时 fallback 原始 query。
// 返回单一结构体 rewriteResult；**永不返回 nil**，调用方无需判空。
//
// ⚠️ 全包只允许有一个调用点（Graph 的改写节点 quickRewriteFn），由 TestDoRewriteHasSingleCallSite 钉住：
// 第二个调用点意味着同一次改写要跑两遍，其中一遍纯属白等。
//
// 优化：先本地快速意图匹配（0ms，覆盖问候/身份/闲聊/系统查询等常见场景），
// 命中后直接返回，省掉 LLM 调用。本地没命中时再判一次「有没有必要调 LLM」：
// 只有问题里含真实指代表达时才调 —— 见 hasAnaphora 与 rewriteLLMTimeout 的说明。
//
// 检索 query 已经与这里解耦（见 retrieval_query.go），所以即使本函数走本地短路，
// 检索侧依然拿得到上下文相关的 query。
func doRewriteWithLLM(ctx context.Context, input *quickGraphInput) *rewriteResult {
	// ── Step 0: 本地快速意图匹配（0ms） ──
	if localIntent, ok := matchLocalIntent(input.OriginalQuery); ok {
		out := rewriteFallback(input, localIntent)
		logger.Infof("[意图识别-本地] original=%q → intent=%s, skipRetrieve=%v, cost=0ms",
			input.OriginalQuery, out.Intent, out.SkipRetrieve)
		return out
	}

	// ── Step 1: 无指代 → 用本地默认意图，不调 LLM ──
	// 依据（线上日志实测，36 个快速模式样本）：
	//   - 23 次真调 LLM 中 18 次（78%）返回默认意图 question，等于白调；
	//   - need_clarify 命中 0 次；
	//   - keywords 字段下游从未消费（只是打个日志）。
	// 因此「无指代」时 LLM 没有不可替代的产出，直接判定为 question。
	// 代价：本地正则漏掉的闲聊/元问题（约 22%）会多跑一次检索（p50≈1s），
	// 但生成侧有完整 history，回答质量不受影响。
	if !hasAnaphora(input.OriginalQuery) {
		logger.Infof("[意图识别-跳过大模型] original=%q → intent=%s（无疑义词，检索 query 已由本地规划）cost=0ms",
			input.OriginalQuery, intentQuestion)
		return rewriteFallback(input, intentQuestion)
	}

	// ── Step 2: 有指代 → 调 LLM（消解指代是它不可替代的能力） ──
	cm := input.ChatModel
	if cm == nil {
		logger.Warnf("doRewriteWithLLM: 入参里没有 ChatModel，跳过改写")
		return rewriteFallback(input, intentQuestion)
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

	// 4. 同步调 Generate（改写不需要流式）。ctx 由调用方带硬超时。
	msg, err := cm.Generate(ctx, msgs)
	if err != nil || msg == nil || msg.Content == "" {
		logger.Warnf("quickRewriteFn: LLM 改写失败/超时（%v），fallback 到原问题；检索 query 走本地实体回填", err)
		return rewriteFallback(input, intentQuestion)
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
		return rewriteFallback(input, intentQuestion)
	}

	// 6. 清洗 + 验证
	if strings.TrimSpace(result.Rewritten) == "" {
		result.Rewritten = input.OriginalQuery
	}
	if !isValidIntent(result.Intent) {
		result.Intent = intentQuestion
	}

	// 7. 澄清检查: need_clarify=true 且有 question 才生效
	result.NeedClarify = result.NeedClarify && strings.TrimSpace(result.ClarifyQuestion) != ""

	// 8. 派生字段就地算好：跳过检索的规则只有 deriveSkipRetrieve 一处
	result.SkipRetrieve = deriveSkipRetrieve(result.Intent, result.NeedClarify)

	return &result
}

// isValidIntent 检查 LLM 返回的意图是否在合法枚举内
func isValidIntent(intent string) bool {
	switch intent {
	case intentGreeting, intentChitchat, intentQuestion, intentIdentity, intentMeta, intentRealtime:
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

// addQuickRetrieveNode 节点 2：Retrieve
// 用 LambdaNode 替代 AddRetrieverNode，在 Lambda 内部提前检查 SkipRetrieve，
// 避免 EinoRetrieverAdapter 被实例化后才被 PostHandler 清空——那样知识库查询的开销已经花出去了。
// QueryRewrite 已经同步完成，Retrieve 直接用改写后的 query（或原始 query）查一次即可，不再做并行改写等待。
//
// 向量检索 query 取载荷的 VectorQuery；关键字检索 query 通过 retriever option 单独传入。
func addQuickRetrieveNode(g *einoCompose.Graph[*quickGraphInput, *quickGraphOutput], einoRetriever *rag.EinoRetrieverAdapter) error {
	return g.AddLambdaNode(graphQuickNodeRetrieve,
		einoCompose.InvokableLambda(func(ctx context.Context, p *quickGraphPayload) (*quickGraphPayload, error) {
			if p == nil || p.Input == nil {
				return nil, apperrors.NewDefault(apperrors.CodeInternalError)
			}

			// 提前短路：Rewrite 阶段已判定不需要检索 → 不查知识库，直接返回空 docs。
			// 判据只看 SkipRetrieve：它已由 deriveSkipRetrieve 涵盖 needClarify，
			// 而 need_clarify 在进入本节点之前就已被分叉到 clarify_end，这里判它才是死条件。
			// 此处 p.Rewrite 非 nil 由分叉条件保证（分支会先校验再路由过来）。
			if p.Rewrite.SkipRetrieve {
				p.Docs = nil
				return p, nil
			}

			// 构造 retriever.Option（KBIDs / UserID / TopK / 关键字侧短 query）
			opts := buildRetrieverOpts(p.Input, p.KeywordQuery)

			docs, err := einoRetriever.Retrieve(ctx, p.VectorQuery, opts...)
			if err != nil {
				logger.Warnf("quickRetrieveFn: 检索失败，降级为空结果: %v", err)
				p.Docs = nil
				return p, nil
			}
			p.Docs = docs
			return p, nil
		}),
		einoCompose.WithNodeName("KnowledgeRetrieve"),
	)
}

// buildRetrieverOpts 从 quickGraphInput 构造 retriever.Option 切片。
// keywordQuery 非空时，关键字侧改用它而不是公共 query。
func buildRetrieverOpts(input *quickGraphInput, keywordQuery string) []retriever.Option {
	var opts []retriever.Option
	if input != nil {
		if len(input.KnowledgeBaseIDs) > 0 {
			opts = append(opts, rag.WithKnowledgeBaseIDs(input.KnowledgeBaseIDs))
		}
		if input.UserID != "" {
			opts = append(opts, rag.WithUserID(input.UserID))
		}
	}
	if strings.TrimSpace(keywordQuery) != "" {
		opts = append(opts, rag.WithKeywordQuery(keywordQuery))
	}
	if cfg := config.Get(); cfg != nil && cfg.RAG.TopK > 0 {
		opts = append(opts, retriever.WithTopK(cfg.RAG.TopK))
	}
	return opts
}

// addQuickBuildMsgsNode 节点 3：BuildPromptMessages。
// 从 State 拿 Input，在 userQuestionIndex 前插入检索上下文。
func addQuickBuildMsgsNode(g *einoCompose.Graph[*quickGraphInput, *quickGraphOutput]) error {
	return g.AddLambdaNode(graphQuickNodeBuildMsgs,
		einoCompose.InvokableLambda(quickBuildMsgsFn),
		einoCompose.WithNodeName("BuildPromptMessages"),
	)
}

// quickBuildMsgsFn 节点 3 实现：在用户问题前插入检索上下文块
func quickBuildMsgsFn(_ context.Context, p *quickGraphPayload) (*quickGraphPayload, error) {
	if p == nil || p.Input == nil {
		return nil, apperrors.NewDefault(apperrors.CodeInternalError)
	}
	input := p.Input

	// 用改写结果替换用户问题。rewriteFallback 保证 Rewritten 恒为非空串，
	// 所以这里只需处理「与原问题相同（无需替换）」这一种情况。
	questionContent := input.OriginalQuery
	if rw := p.Rewrite.Rewritten; rw != "" && rw != input.OriginalQuery {
		questionContent = rw
	}

	msgs := make([]*schema.Message, 0, len(input.InputMsgs)+2)
	injected := false
	for i, m := range input.InputMsgs {
		if i == input.UserQuestionIndex && len(p.Docs) > 0 {
			block := buildDocsContextBlock(p.Docs, input.RetrievalBudget, input.ModelName)
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
	if len(p.Docs) > 0 && !injected {
		last := msgs[len(msgs)-1]
		block := buildDocsContextBlock(p.Docs, input.RetrievalBudget, input.ModelName)
		msgs = append(append(msgs[:len(msgs)-1], schema.UserMessage(block)), last)
	}
	p.Msgs = msgs
	return p, nil
}

// addQuickGenerateNode 节点 4：ChatModelGenerate。
// 用 InvokableLambda 而非 StreamableLambda，因为返回值是 StreamReader 本身，
// StreamableLambda 会推断 O 为流内元素 Message，和 END 期望的 StreamReader[Message] 类型不匹配。
func addQuickGenerateNode(g *einoCompose.Graph[*quickGraphInput, *quickGraphOutput]) error {
	return g.AddLambdaNode(graphQuickNodeGenerate,
		einoCompose.InvokableLambda(quickGenerateFn),
		einoCompose.WithNodeName("ChatModelGenerate"),
	)
}

// quickGenerateFn 节点 4 实现：调用 ChatModel 流式生成回复
func quickGenerateFn(ctx context.Context, p *quickGraphPayload) (*quickGraphOutput, error) {
	if p == nil || p.Input == nil {
		return nil, apperrors.NewDefault(apperrors.CodeInternalError)
	}
	cm := p.Input.ChatModel
	if cm == nil {
		return nil, apperrors.NewDefault(apperrors.CodeInternalError)
	}
	msgs := p.Msgs
	sr, err := cm.Stream(ctx, msgs)
	if err != nil {
		return nil, err
	}
	// docs 随出参带出去：调用方要用它拼 sources，这是「Invoke 之后还需要的东西」的唯一出口。
	return &quickGraphOutput{Stream: sr, Docs: p.Docs}, nil
}

// addQuickClarifyBranch 改写之后分叉：需要澄清 → clarify_end，否则 → retrieve。
//
// ⚠️ 条件函数的入参类型必须等于「分叉起点节点的输出类型」，也就是 *quickGraphPayload。
// 条件本身是纯函数（只读 Rewrite，不做任何副作用），所以它不落库、不推事件。
func addQuickClarifyBranch(g *einoCompose.Graph[*quickGraphInput, *quickGraphOutput]) error {
	condition := func(_ context.Context, p *quickGraphPayload) (string, error) {
		if p == nil || p.Rewrite == nil {
			return "", apperrors.NewDefault(apperrors.CodeInternalError)
		}
		if p.Rewrite.NeedClarify {
			return graphQuickNodeClarify, nil
		}
		return graphQuickNodeRetrieve, nil
	}
	return g.AddBranch(graphQuickNodeRewrite, einoCompose.NewGraphBranch(condition, map[string]bool{
		graphQuickNodeRetrieve: true,
		graphQuickNodeClarify:  true,
	}))
}

// addQuickClarifyNode 终态节点：把「本轮要追问」这件事作为**数据**交给调用方。
//
// 它不落库、不推事件、不结束 span —— 那些都是入口函数的职责。节点只把流程走到终点，
// 让 Graph 的返回值完整描述这一次请求的结局：要么有回答流，要么有追问。
func addQuickClarifyNode(g *einoCompose.Graph[*quickGraphInput, *quickGraphOutput]) error {
	return g.AddLambdaNode(graphQuickNodeClarify,
		einoCompose.InvokableLambda(quickClarifyFn),
		einoCompose.WithNodeName("ClarifyEnd"),
	)
}

// quickClarifyFn 终态节点实现：把改写结果里的澄清诉求翻译成出参。
func quickClarifyFn(_ context.Context, p *quickGraphPayload) (*quickGraphOutput, error) {
	if p == nil || p.Rewrite == nil {
		return nil, apperrors.NewDefault(apperrors.CodeInternalError)
	}
	return &quickGraphOutput{Clarify: &quickGraphClarify{
		Question: p.Rewrite.ClarifyQuestion,
		Options:  p.Rewrite.ClarifyOptions,
		Intent:   p.Rewrite.Intent,
	}}, nil
}

// registerQuickGraphEdges 一次性注册全部边。
// registerQuickGraphEdges 一次性注册全部边。
//
// ⚠️ query_rewrite → retrieve 这条边**不在这里**：它由 addQuickClarifyBranch 注册的分支决定
// （改写之后要么去 retrieve，要么去 clarify_end）。同一起点既有边又有分支会被判冲突。
func registerQuickGraphEdges(g *einoCompose.Graph[*quickGraphInput, *quickGraphOutput]) error {
	edges := [][2]string{
		{einoCompose.START, graphQuickNodeRewrite},
		{graphQuickNodeRetrieve, graphQuickNodeBuildMsgs},
		{graphQuickNodeBuildMsgs, graphQuickNodeGenerate},
		{graphQuickNodeGenerate, einoCompose.END},
		{graphQuickNodeClarify, einoCompose.END},
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			return fmt.Errorf("edge %s→%s: %w", e[0], e[1], err)
		}
	}
	return nil
}

// processMessageGraphQuick 快速模式入口：QueryRewrite → Retrieve → BuildPrompt → Generate 四节点 Graph。
func (s *chatService) processMessageGraphQuick(
	ctx context.Context,
	userID, sessionID, userMsgID string,
	req requestdto.SendMessageRequest,
	eventCh chan<- dto.StreamEvent,
) {
	// panic recover：Graph 内部已有自己的错误出口，这里只兜住本函数栈上的意外 panic，
	// 保证 SSE 流一定拿到终态事件、不会永远停在「正在生成」。
	defer func() {
		if r := recover(); r != nil {
			eventch.Send(ctx, eventCh, dto.StreamEvent{Type: "error", Detail: "处理过程中发生未预期错误", Done: true})
		}
	}()

	// 1) 初始化上下文（历史/摘要/记忆/画像/预算）
	sendProgressEvent(ctx, eventCh, "正在加载上下文...")
	client, enhancedCtx, err := s.initContext(ctx, userID, sessionID, req.ModelID, req.ModelType, req.Content)
	if err != nil {
		sendErrorEvent(ctx, eventCh, err, err.Error())
		return
	}
	chatModel := client.ChatModel()

	// 2) 组装 Graph Input：System Prompt / History / 模型名 / 检索预算 / ChatModel
	graphInput := buildQuickInput(req, userID, userMsgID, enhancedCtx, client, chatModel)

	// 3) 跑完整条链路：改写 →（要澄清就到此为止）→ 检索 → 拼 prompt → 生成。
	//    「改写要不要调 LLM」「要不要澄清」「要不要检索」全在图内判定并分叉，
	//    图外只剩副作用（推事件、落库）——于是「一次请求走哪几条路」只有一处定义。
	graphCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	graphOut, err := invokeQuickGraph(graphCtx, s.quickGraph, graphInput, eventCh)
	if err != nil {
		llmpkg.ReduceContextBudgetOnError(req.ModelID, err)
		return
	}

	// 3a) 需要澄清：改写阶段就分叉到 clarify_end，检索与生成这一轮都没发生。
	//     落库/推事件在下面这个函数里做（节点只产出数据，不碰副作用）。
	if clarify := graphOut.Clarify; clarify != nil {
		s.emitQuickClarify(ctx, eventCh, sessionID, req, clarify)
		return
	}

	// 4) 生成助手消息 ID 并消费回答流。
	//    ⚠️ start 必须等「确定不是澄清」之后再发：澄清路径不该先给前端开一个回答气泡。
	assistantMsgID := uuid.New().String()
	eventch.Send(ctx, eventCh, dto.StreamEvent{Type: "start", MessageID: assistantMsgID})

	fullContent, err := consumeQuickGraphStream(graphCtx, graphOut.Stream, assistantMsgID, eventCh)
	if err != nil {
		llmpkg.ReduceContextBudgetOnError(req.ModelID, err)
		return
	}

	// 空回答守卫：上游（尤其 OpenAI 兼容网关）会偶发返回「成功但 content 为空」。
	// 不拦的话这里会照发 done 并把空 assistant 消息落库，用户看到空白气泡，
	// 且空消息进入后续 history 后会让该会话之后每一轮都失败 —— 一次空回答污染整条会话。
	// 具体口径见 rejectEmptyAnswer（与深度模式共用）。
	// graph 成功返回时 Docs 就是本次命中的文档（唯一来源：载荷 → 出参）。
	if rejectEmptyAnswer(ctx, eventCh, "quick", sessionID, req.ModelID, assistantMsgID,
		fullContent, fmt.Sprintf("retrievedDocs=%d", len(graphOut.Docs))) {
		return
	}

	// 6) 出参里的 docs 就是本次命中的文档（来源唯一：载荷 → 出参，不再经过 context）
	var sources []dto.SourceInfo
	if len(graphOut.Docs) > 0 {
		sources = einoDocsToSourceInfos(graphOut.Docs)
	}

	// 7) 结束事件 + 异步落库 + 异步刷新摘要记忆
	s.emitDoneAndSave(ctx, eventCh, sessionID, assistantMsgID, fullContent, req, sources, nil, func(meta map[string]any) {
		if meta == nil {
			return
		}
		if traceID := traceid.FromContext(ctx); traceID != "" {
			meta["trace_id"] = traceID
		}
		meta["eino_quick_graph_mode"] = true
	})
	s.refreshContextAsync(ctx, userID, sessionID, enhancedCtx.History, chatModel)
}

// emitQuickClarify 处理快速模式的澄清终态：写 session 的 PendingClarify、
// 落一条 assistant 追问消息（让历史自然串成 [user问题 → assistant追问 → user回答]）、
// 推 clarify 事件。
func (s *chatService) emitQuickClarify(
	ctx context.Context,
	eventCh chan<- dto.StreamEvent,
	sessionID string,
	req requestdto.SendMessageRequest,
	clarify *quickGraphClarify,
) {
	pendingData, _ := json.Marshal(entity.PendingClarifyData{
		Question: clarify.Question,
		Options:  clarify.Options,
		SetAt:    time.Now(),
	})
	if err := s.sessionRepo.SetPendingClarify(ctx, sessionID, pendingData); err != nil {
		logger.Warnf("存储澄清追问状态失败: %v", err)
	}

	// 存一条 assistant 消息（追问），让历史自然串成 [user问题 → assistant追问 → user回答]
	clarifyMsgID := uuid.New().String()
	// 追问消息同样要挂 trace_id：否则前端点开这条消息查不到链路（普通回答那条是挂的）。
	var clarifyMeta datatypes.JSON
	if traceID := traceid.FromContext(ctx); traceID != "" {
		clarifyMeta = datatypes.JSON(mustMarshal(map[string]any{"trace_id": traceID}))
	}
	if err := s.saveAssistantMessage(ctx, sessionID, clarifyMsgID, clarify.Question, req, nil, clarifyMeta); err != nil {
		logger.Warnf("存储澄清追问消息失败: %v", err)
	}

	eventch.Send(ctx, eventCh, dto.StreamEvent{Type: "clarify", Clarify: &dto.ClarifyPayload{
		Question: clarify.Question,
		Options:  clarify.Options,
	}, Done: true})
}

// buildQuickInput 组装 quickGraphInput：System Prompt / History / 预算 / 模型名 / ChatModel
func buildQuickInput(
	req requestdto.SendMessageRequest,
	userID, userMsgID string,
	enhancedCtx *EnhancedContext,
	client *llmpkg.OpenAIClient,
	chatModel einoModel.BaseChatModel,
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
		ChatModel:         chatModel,
	}
}

// compileQuickGraph 在启动期构建并编译快速检索链路，返回可被所有请求并发复用的 Runnable。
//
// 这里没有 ctx / eventCh：装配期没有请求上下文可推事件，失败一律上抛给构造函数，由启动流程
// 决定是否退出。于是「Graph 编译失败」在请求期**结构上不可能发生**——它要么在启动时就失败，
// 要么根本不存在。（旧实现每次请求都 build + Compile，并把错误推成一张请求级 error 事件，
// 同一个静态错误会在每一个请求上重复出现一次。）
func compileQuickGraph(
	einoRetriever *rag.EinoRetrieverAdapter,
) (einoCompose.Runnable[*quickGraphInput, *quickGraphOutput], error) {
	g, err := buildQuickGraph(einoRetriever)
	if err != nil {
		return nil, err
	}
	r, err := g.Compile(nil, einoCompose.WithGraphName("quick_rag_pipeline"))
	if err != nil {
		return nil, err
	}
	return r, nil
}

// invokeQuickGraph 驱动 Graph 执行一次，返回它的出参。
//
// 只做「跑图 + 出错推事件」，不碰流：出参要么带回答流、要么带澄清诉求，分流交给调用方。
// 于是「这一次要不要回答」这个判断只存在于图上的分支里，调用方只需读结果。
//
// 成功返回时出参必非 nil，且 Stream / Clarify 至少有一个非 nil。
func invokeQuickGraph(
	graphCtx context.Context,
	runnable einoCompose.Runnable[*quickGraphInput, *quickGraphOutput],
	graphInput *quickGraphInput,
	eventCh chan<- dto.StreamEvent,
) (*quickGraphOutput, error) {
	sendProgressEvent(graphCtx, eventCh, "正在执行快速检索链路...")
	out, err := runnable.Invoke(graphCtx, graphInput)
	if err != nil {
		sendErrorEvent(graphCtx, eventCh, err, "快速检索执行失败")
		return nil, err
	}
	// 出参不变式：Stream / Clarify 至少一个非 nil（见 quickGraphOutput 的说明）
	if out == nil || (out.Stream == nil && out.Clarify == nil) {
		err = fmt.Errorf("快速检索未返回结果")
		sendErrorEvent(graphCtx, eventCh, err, "快速检索未返回结果")
		return nil, err
	}
	return out, nil
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
			sendErrorEvent(ctx, eventCh, err, "快速检索流式生成失败")
			return "", err
		}
		if msg == nil {
			continue
		}
		if !assistantSeen {
			sendProgressEvent(ctx, eventCh, "正在生成回答...")
			assistantSeen = true
		}
		if msg.Content != "" {
			fullContent += msg.Content
			eventch.Send(ctx, eventCh, dto.StreamEvent{
				Type:      "content",
				MessageID: assistantMsgID,
				Content:   msg.Content,
			})
		}
		// 快速模式不期望 tool_calls，收到时打 warn 但不报错
		if len(msg.ToolCalls) > 0 {
			logger.Warnf("quick graph 模式收到模型 tool_calls（数量=%d），但快速模式没有挂工具链，已忽略。首条 tool name=%v",
				len(msg.ToolCalls), safeFirstToolName(msg.ToolCalls))
		}
	}
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
