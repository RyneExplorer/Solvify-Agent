package service

import (
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/rag"
	"solvify-agent/pkg/logger"
)

// 检索 query 规划：把「当前问题 + 最近几轮用户提问」交给向量侧，
// 把「指代回填后的短句」交给关键字侧。
//
// 为什么要分两路：混合检索的两路对 query 长度偏好相反。
//   - 向量侧（bge-m3）对长文本鲁棒，多给上下文能提高召回；
//   - 关键字侧**不是 BM25**，是 pgvector 表上的数组重叠（`&&`）+ 命中率打分：
//     score = COUNT(chunk 关键词 ∩ query 词项) / cardinality(query 词项)
//     （见 hybrid_retriever.go keywordSearch —— 无词频、无 IDF、无 chunk 长度归一化）。
//     **分母就是 query 自己的词项数**，所以 query 越长，所有候选的分数被同一比例压低：
//     排序被拉平，而且会撞上「向量侧全灭时才启用」的 keywordScoreThreshold（默认 0.25），
//     把所有候选一起滤掉 —— 实测同一问题把关键字 query 从 4 个词项拉到 10 个词项，
//     命中数由 2 条掉到 0 条。所以关键字侧必须是「短且含实体」的 query：词项少，
//     且每个词项都指向本轮话题。
//
// 为什么只做指代回填、不整段拼接：上面这个口径下，query 的词项集**就是**排序函数本身 ——
// 每多一个无关词项，既抬高分母、又把排序往历史话题上拉（历史 chunk 因为「命中词项更多」
// 而反超本轮该有的 chunk）。用户切换话题时这就是污染。只替换指代表达，长度天然受控，
// 且副作用可枚举。
//
// 全程本地规则，不调 LLM —— 依据是线上日志：快速模式 36 个样本里 LLM 改写的产出质量很差
// （78% 只是确认本地默认意图 question、need_clarify 命中 0 次、keywords 下游未消费、
// 39% 改写结果与原问题完全相同），而它唯一的不可替代价值就是消解指代。
const (
	// retrievalHistoryRounds 拼入向量 query 的历史提问轮数上限（只取用户提问）。
	retrievalHistoryRounds = 2
	// retrievalHistoryMaxRunes 单条历史提问的截断长度。
	retrievalHistoryMaxRunes = 120
	// retrievalVectorMaxRunes 向量 query 总长上限，超出时保留尾部（最近的内容优先）。
	retrievalVectorMaxRunes = 400
	// retrievalKeywordMaxRunes 关键字 query 长度上限（回填后仍过长时截断）。
	retrievalKeywordMaxRunes = 200
	// retrievalEntityMaxCandidates 实体表候选数上限。
	retrievalEntityMaxCandidates = 8
)

// retrievalQueries 双轨检索 query。
type retrievalQueries struct {
	// Vector 向量检索 query：当前问题（或 LLM 改写结果）+ 最近 1~2 轮用户提问拼接。
	Vector string
	// Keyword 关键字检索 query：指代回填后的短句（回填不成立时等于原问题）。
	Keyword string
	// Entities 本轮使用的实体表，供日志/观测排查。
	Entities []string
	// Backfilled 关键字侧是否真的发生了指代回填。
	Backfilled bool
}

// planQueriesFromInput 从 Graph Input 组装双轨检索 query。
// base 是当前问题的基准文本：有 LLM 改写结果时传改写结果，否则传原问题。
func planQueriesFromInput(input *quickGraphInput, base string) retrievalQueries {
	if input == nil {
		return retrievalQueries{Vector: base, Keyword: base}
	}
	history := recentUserQuestions(input.InputMsgs, input.UserQuestionIndex, retrievalHistoryRounds)
	start := time.Now()
	q := planRetrievalQueries(history, input.OriginalQuery, base)
	logger.Infof("[检索query规划] historyRounds=%d, entities=%v, backfilled=%v, vectorQuery=%q, keywordQuery=%q, cost=%dms",
		len(history), q.Entities, q.Backfilled, q.Vector, q.Keyword, time.Since(start).Milliseconds())
	return q
}

// planRetrievalQueries 依据「当前问题 + 历史用户提问」规划双轨检索 query。
// history 需按时间正序（旧 → 新）传入；raw 与 base 分别为当前问题的原文与基准文本。
func planRetrievalQueries(history []string, raw, base string) retrievalQueries {
	raw = strings.TrimSpace(raw)
	base = strings.TrimSpace(base)
	if base == "" {
		base = raw
	}
	if raw == "" && base == "" {
		return retrievalQueries{}
	}

	entities := buildEntityTable(history, retrievalEntityMaxCandidates)
	keyword, backfilled := backfillAnaphora(raw, entities)
	switch {
	case backfilled:
		// 本地回填成功：关键字侧拿「实体 + 原问题剩余词」组成短 query。
	case hasAnaphora(raw) && base != raw:
		// 有指代但实体表拿不到候选（例如首轮「那个方案呢」），而 LLM 改写给出了消解结果
		// —— 这是唯一一处关键字侧借用 LLM 结果的情况，且必然比带指代的原句好。
		keyword = base
	default:
		// 无指代：关键字侧就用原问题。不把历史实体前缀上去，
		// 否则用户切换话题时会给关键字 query 灌无关词项（污染排序）。
		keyword = raw
	}
	keyword = truncateRunes(keyword, retrievalKeywordMaxRunes)

	// 向量侧：历史提问在前、当前问题在后（近期内容落在尾部，截断时优先保留）。
	parts := make([]string, 0, len(history)+1)
	for _, h := range history {
		h = truncateRunes(strings.TrimSpace(h), retrievalHistoryMaxRunes)
		if h != "" {
			parts = append(parts, h)
		}
	}
	parts = append(parts, base)
	vector := truncateTailRunes(strings.Join(parts, ". "), retrievalVectorMaxRunes)

	return retrievalQueries{
		Vector:     vector,
		Keyword:    keyword,
		Entities:   entities,
		Backfilled: backfilled,
	}
}

// recentUserQuestions 从 messages 里取「当前问题之前」最近 maxRounds 条用户提问，按时间正序返回。
// 只取用户提问、不取助手回答：助手回答是模型生成的词，拼进检索 query 反而引入噪声。
func recentUserQuestions(msgs []*schema.Message, currentIdx, maxRounds int) []string {
	if len(msgs) == 0 || maxRounds <= 0 {
		return nil
	}
	if currentIdx > len(msgs) || currentIdx <= 0 {
		currentIdx = len(msgs)
	}
	// 倒序收集（最近在前）
	reversed := make([]string, 0, maxRounds)
	for i := currentIdx - 1; i >= 0 && len(reversed) < maxRounds; i-- {
		m := msgs[i]
		if m == nil || strings.TrimSpace(m.Content) == "" {
			continue
		}
		if string(m.Role) != "user" {
			continue
		}
		content := stripTruncationMarker(m.Content)
		if content == "" {
			continue
		}
		reversed = append(reversed, content)
	}
	// 反转为时间正序
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed
}

// stripTruncationMarker 剥掉上下文预算追加的截断标记，还原用户提问正文。
// 不剥的话，标记里的「内容/过长/截断」会作为正文词汇混进检索 query 和实体表。
func stripTruncationMarker(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, truncationMarker)
	return strings.TrimSpace(s)
}

// metaQuestionTerms 是疑问/元话语词：它们在任何话题里都会出现，不可能是「上文实体」。
var metaQuestionTerms = map[string]struct{}{
	"多大": {}, "多少": {}, "几个": {}, "几年": {}, "几天": {}, "多久": {},
	"哪些": {}, "哪里": {}, "哪个": {}, "什么": {}, "怎么": {}, "怎样": {},
	"如何": {}, "为什么": {}, "是不是": {}, "能不能": {}, "可不可以": {},
	"一下": {}, "具体": {}, "详细": {}, "相关": {}, "有关": {}, "目前": {},
	"什么样": {}, "哪种": {}, "这边": {}, "那边": {},
}

// questionShellVerbs 是提问套话动词：它们描述「我在问什么」，不描述「问的是谁」。
// 例如「支持哪些协议」的主题是协议，不是支持。
var questionShellVerbs = map[string]struct{}{
	"支持": {}, "提供": {}, "包含": {}, "包括": {}, "介绍": {}, "说明": {},
	"解释": {}, "列举": {}, "推荐": {}, "讲解": {}, "分析": {}, "对比": {},
}

// buildEntityTable 从历史用户提问里抽取候选实体表（近似 NER）。
//
// 没有引入 NER 模型，用「与关键字检索完全一致的分词 + 过滤疑问/套话词 + 长度/位置启发式」近似：
//   - 越新的轮次越优先（话题延续性）；
//   - 同一轮内「专名」（含拉丁字母或数字，如 osi / raid / redis）优先于中文通用词；
//   - 同为专名或同为中文时，词越长越优先（中文里更长的切分单元通常更具体，例如 超时时间 > 超时）；
//   - 等长时靠前的优先（中文提问的主题通常在前半句）。
//
// 注意这里额外要求「至少 2 个字符」，与 rag.extractKeywords 现在的口径一致：
// chunk 侧的关键词只有 2~12 字 ngram 和 ≥2 字符的英文/数字串，不存在单字符词条，
// 所以单字词项既当不了实体，也匹配不到任何东西。关键字检索侧的关键词口径保持不动。
func buildEntityTable(history []string, max int) []string {
	if len(history) == 0 || max <= 0 {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, max)

	for i := len(history) - 1; i >= 0 && len(out) < max; i-- {
		terms := rag.ExtractKeywords(history[i])
		if len(terms) == 0 {
			continue
		}
		pos := make(map[string]int, len(terms))
		for p, t := range terms {
			if _, ok := pos[t]; !ok {
				pos[t] = p
			}
		}
		sort.SliceStable(terms, func(a, b int) bool {
			ta, tb := terms[a], terms[b]
			if pa, pb := isProperNameToken(ta), isProperNameToken(tb); pa != pb {
				return pa
			}
			la, lb := len([]rune(ta)), len([]rune(tb))
			if la != lb {
				return la > lb
			}
			return pos[ta] < pos[tb]
		})
		for _, t := range terms {
			if len([]rune(t)) < 2 {
				continue
			}
			if _, skip := metaQuestionTerms[t]; skip {
				continue
			}
			if _, skip := questionShellVerbs[t]; skip {
				continue
			}
			// 历史提问自身可能带指代（「那它一共分了几层？」），别把「那它」当实体 ——
			// 否则下一轮回填会拿它去替换指代，等于什么都没消解。
			if hasAnaphora(t) {
				continue
			}
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			out = append(out, t)
			if len(out) >= max {
				break
			}
		}
	}
	return out
}

// isProperNameToken 判断词项是否像「专名」：含拉丁字母或数字。
//
// 中文语料里这类词项几乎总是产品名 / 协议名 / 缩写 / 版本号（redis、osi、raid、pgvector），
// 在同一篇文档内的区分度远高于中文通用词。反例是「网络安全」这种**文档级泛词**：
// 它在半篇手册里都出现，拿它回填只会把噪声 chunk 一起拉上来。
//
// 实测（test1/kw_strategy_probe.go，KB 1934a116，文档「网络安全plus」）：
// 历史「网络安全里 OSI 七层模型有哪些层？」→ 当前「那它一共分了几层？」
//
//	长度优先选「网络安全」→ 命中 chunk#1(正解) + chunk#2(DoS 噪声段)
//	专名优先选「osi」    → 命中 chunk#0(OSI 总述) + chunk#1(各层解释)   两条都是正解
func isProperNameToken(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return true
		}
	}
	return false
}

var (
	// reAnaphoraHead 指示代词 + 中心语（「这个方案」「那个配置项」）。
	// 整段一起替换：只换掉限定词会得到「成本方案」这类残句。
	// 中心语用「非虚词/非标点」的负字符类收口，避免贪婪吞掉「的」后面的内容。
	reAnaphoraHead = regexp.MustCompile(`(?:这个|那个|这些|那些|上述|前述|刚才[说提讲]的|之前[说提问]的|前面[说提讲]的)[^` + anaphoraTailStopChars + `]{1,4}`)
	// reAnaphoraPronoun 人称/物称代词，可带「那/这」前缀（gse 会把「那它」切成一个词，兜住它）。
	reAnaphoraPronoun = regexp.MustCompile(`(?:那|这)?(?:它们|他们|她们|它|他|她)`)
)

// anaphoraTailStopChars 是「中心语到此为止」的收口字符：助词、标点、常见虚词/动词/方位词。
// 没有它，「那个配置项在哪里」会被整体吞成中心语。
const anaphoraTailStopChars = `的了吗呢吧啊是和与及或，。？！、；：\s,?.!;:在能有会要会被把给对从向到就都也还再又很更最没不里外中上下个之`

// anaphoraPrevBlocklist 是「紧跟其后的指代词其实不是指代」的字：
// 「其他」的「他」、「任何」的「何」同理。
var anaphoraPrevBlocklist = map[rune]struct{}{
	'其': {}, '任': {}, '无': {}, '另': {},
}

// anaphoraMatches 返回 question 中所有真实指代表达的下标区间（按位置升序、互不重叠）。
func anaphoraMatches(question string) [][]int {
	if question == "" {
		return nil
	}
	all := make([][]int, 0, 4)
	all = append(all, reAnaphoraHead.FindAllStringIndex(question, -1)...)
	all = append(all, reAnaphoraPronoun.FindAllStringIndex(question, -1)...)
	sort.Slice(all, func(i, j int) bool { return all[i][0] < all[j][0] })

	kept := make([][]int, 0, len(all))
	lastEnd := 0
	for _, loc := range all {
		if loc[0] < lastEnd {
			continue // 已被更靠前的（更长的）匹配覆盖
		}
		if r, ok := prevRune(question, loc[0]); ok {
			if _, blocked := anaphoraPrevBlocklist[r]; blocked {
				continue // 「其他」的「他」不是指代
			}
		}
		kept = append(kept, loc)
		lastEnd = loc[1]
	}
	return kept
}

// hasAnaphora 判断问题里是否含真实指代表达。
// 用于决定要不要调 LLM 改写：只有指代场景下 LLM 才有不可替代的价值。
func hasAnaphora(question string) bool {
	return len(anaphoraMatches(question)) > 0
}

// backfillAnaphora 把问题中的指代表达替换为实体表首位实体，返回替换结果与是否命中。
// 实体表为空、或问题里没有真实指代时原样返回（不猜）。
func backfillAnaphora(question string, entities []string) (string, bool) {
	if len(entities) == 0 || strings.TrimSpace(question) == "" {
		return question, false
	}
	locs := anaphoraMatches(question)
	if len(locs) == 0 {
		return question, false
	}
	entity := entities[0]
	var sb strings.Builder
	last := 0
	for _, loc := range locs {
		sb.WriteString(question[last:loc[0]])
		sb.WriteString(entity)
		last = loc[1]
	}
	sb.WriteString(question[last:])
	return strings.TrimSpace(sb.String()), true
}

// prevRune 返回 s 中字节下标 idx 之前的一个字符。
func prevRune(s string, idx int) (rune, bool) {
	if idx <= 0 || idx > len(s) {
		return 0, false
	}
	r, _ := utf8.DecodeLastRuneInString(s[:idx])
	return r, true
}

// truncateRunes 按字符数截断，超出时补省略号。
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[:max]))
}

// truncateTailRunes 按字符数截断但保留尾部：拼接型 query 里越靠后越新，优先保新。
func truncateTailRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return strings.TrimSpace(string(r[len(r)-max:]))
}
