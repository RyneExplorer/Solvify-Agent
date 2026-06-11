package service

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"gorm.io/datatypes"

	"solvify-agent/internal/model/entity"
	apperrors "solvify-agent/pkg/errors"
)

const (
	documentChunkTargetSize  = 800
	documentChunkOverlapSize = 100
	documentKeywordLimit     = 20
)

var processableTextFileTypes = map[string]struct{}{
	"txt": {}, "md": {}, "markdown": {}, "html": {}, "csv": {}, "json": {},
}

var documentStopWords = map[string]struct{}{
	"我们": {}, "你们": {}, "他们": {}, "这个": {}, "那个": {}, "可以": {}, "需要": {},
	"进行": {}, "如果": {}, "时候": {}, "一个": {}, "相关": {}, "内容": {}, "文档": {},
	"用户": {}, "系统": {}, "支持": {}, "通过": {}, "使用": {}, "创建": {},
}

var (
	htmlTagPattern          = regexp.MustCompile(`<[^>]+>`)
	englishKeywordPattern   = regexp.MustCompile(`[A-Za-z0-9_./:-]{2,64}`)
	separatorNormalizeRegex = regexp.MustCompile(`[\s,，.。;；:：!！?？()（）\[\]【】{}<>《》"'“”‘’、/\\|]+`)
)

// documentChunkService 封装文档分块、关键词提取和向量生成能力
type documentChunkService struct {
	embeddingService EmbeddingServiceInterface
}

// NewDocumentChunkService 创建文档分块服务
func NewDocumentChunkService(embeddingService EmbeddingServiceInterface) DocumentChunkServiceInterface {
	return &documentChunkService{embeddingService: embeddingService}
}

// SupportsFileType 判断文件类型是否支持正文处理
func (s *documentChunkService) SupportsFileType(fileType string) bool {
	_, ok := processableTextFileTypes[fileType]
	return ok
}

// NormalizeContent 规整文档正文
func (s *documentChunkService) NormalizeContent(content, fileType string) string {
	if fileType == "html" {
		content = htmlTagPattern.ReplaceAllString(content, " ")
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\t", " ")
	return strings.TrimSpace(separatorNormalizeRegex.ReplaceAllString(content, " "))
}

// SplitContent 按固定窗口切分正文
func (s *documentChunkService) SplitContent(content string) []string {
	runes := []rune(content)
	if len(runes) == 0 {
		return nil
	}
	if len(runes) <= documentChunkTargetSize {
		return []string{content}
	}

	step := documentChunkTargetSize - documentChunkOverlapSize
	chunks := make([]string, 0)
	for start := 0; start < len(runes); start += step {
		end := start + documentChunkTargetSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
		if end == len(runes) {
			break
		}
	}
	return chunks
}

// BuildChunks 构建文档分块实体并写入向量
func (s *documentChunkService) BuildChunks(ctx context.Context, doc entity.Document, versionID string, contents []string) ([]entity.DocumentChunk, error) {
	if s.embeddingService == nil {
		return nil, apperrors.New(apperrors.CodeInternalError, "文本向量服务未初始化")
	}

	// 1. 先批量生成所有分块向量，避免部分 chunk 已构建但向量缺失
	vectors, err := s.embeddingService.EmbedTexts(ctx, contents)
	if err != nil {
		return nil, err
	}
	if len(vectors) != len(contents) {
		return nil, apperrors.New(apperrors.CodeInternalError, "文本向量数量与分块数量不一致")
	}

	// 2. 再组装入库实体，保证 content、keywords、embedding 使用同一批分块内容
	chunks := make([]entity.DocumentChunk, 0, len(contents))
	for index, content := range contents {
		if s.embeddingService.Dimension() > 0 && len(vectors[index]) != s.embeddingService.Dimension() {
			return nil, apperrors.New(apperrors.CodeInternalError, "文本向量维度与配置不一致")
		}
		chunks = append(chunks, entity.DocumentChunk{
			ID:              uuid.NewString(),
			UserID:          doc.UserID,
			KnowledgeBaseID: doc.KnowledgeBaseID,
			DocumentID:      doc.ID,
			VersionID:       versionID,
			ChunkIndex:      index,
			SectionTitle:    "",
			Content:         content,
			TokenCount:      len([]rune(content)),
			EmbeddingModel:  s.embeddingService.Model(),
			Embedding:       entity.FloatVector(vectors[index]),
			Keywords:        s.extractKeywords(content),
			Metadata:        datatypes.JSON([]byte("{}")),
		})
	}
	return chunks, nil
}

// extractKeywords 按轻量规则提取关键词
func (s *documentChunkService) extractKeywords(content string) entity.TextArray {
	cleaned := separatorNormalizeRegex.ReplaceAllString(htmlTagPattern.ReplaceAllString(content, " "), " ")
	candidates := make(map[string]int)

	// 1. 英文和数字标识保留为完整关键词，便于技术名词和文件名参与混合检索
	for _, match := range englishKeywordPattern.FindAllString(cleaned, -1) {
		s.addKeywordCandidate(candidates, strings.ToLower(match))
	}

	// 2. 中文暂用 2 到 12 字 ngram 轻量提取，后续可替换为 jieba 或 BM25 相关实现
	for _, segment := range strings.Fields(cleaned) {
		s.extractChineseCandidates(candidates, segment)
	}

	items := make([]string, 0, len(candidates))
	for keyword := range candidates {
		items = append(items, keyword)
	}
	s.sortKeywords(items, candidates)
	if len(items) > documentKeywordLimit {
		items = items[:documentKeywordLimit]
	}
	return entity.TextArray(items)
}

// extractChineseCandidates 提取中文候选关键词
func (s *documentChunkService) extractChineseCandidates(candidates map[string]int, segment string) {
	runes := []rune(segment)
	chinese := make([]rune, 0, len(runes))
	for _, r := range runes {
		if unicode.Is(unicode.Han, r) {
			chinese = append(chinese, r)
			continue
		}
		s.collectChineseNgrams(candidates, chinese)
		chinese = chinese[:0]
	}
	s.collectChineseNgrams(candidates, chinese)
}

// collectChineseNgrams 收集中文 ngram 关键词
func (s *documentChunkService) collectChineseNgrams(candidates map[string]int, runes []rune) {
	for start := 0; start < len(runes); start++ {
		for size := 2; size <= 12 && start+size <= len(runes); size++ {
			s.addKeywordCandidate(candidates, string(runes[start:start+size]))
		}
	}
}

// addKeywordCandidate 添加关键词候选
func (s *documentChunkService) addKeywordCandidate(candidates map[string]int, keyword string) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return
	}
	if _, ok := documentStopWords[keyword]; ok {
		return
	}
	candidates[keyword]++
}

// sortKeywords 按出现次数和长度排序关键词
func (s *documentChunkService) sortKeywords(items []string, scores map[string]int) {
	sort.Slice(items, func(i, j int) bool {
		leftScore := scores[items[i]]
		rightScore := scores[items[j]]
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		leftLen := len([]rune(items[i]))
		rightLen := len([]rune(items[j]))
		if leftLen != rightLen {
			return leftLen > rightLen
		}
		return items[i] < items[j]
	})
}
