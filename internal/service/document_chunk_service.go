package service

import (
	"context"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"gorm.io/datatypes"

	"solvify-agent/internal/model/entity"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/textseg"
)

const (
	documentChunkTargetSize  = 800
	documentChunkOverlapSize = 100
	// chunkKeywordHardLimit 是「保险丝」，不是筛选规则。
	//
	// 存储层**不再挑词**：截断是不可逆的（词没落库，以后怎么调参都补不回来），
	// 而打分是可逆的（存了但权重给低，以后调高即可）⇒「哪些词重要」交给检索侧打分决定，
	// 存储层能不丢就不丢。实测 800 rune 窗口下每块词项中位 159、最大 183，
	// 这个上限正常永远碰不到，只为防止异常输入把一行撑爆。
	chunkKeywordHardLimit = 512
)

var (
	htmlTagPattern          = regexp.MustCompile(`<[^>]+>`)
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

// extractKeywords 提取分块关键词。
//
// 口径唯一：与检索侧（`rag.ExtractKeywords` → `HybridRetriever.keywordSearch`）调用
// **同一个** `textseg.Extract`。这是本函数只做一层转发的唯一理由 ——
// 建库侧与检索侧各写一套切词规则，「什么算一个词」的定义就会漂移。这一点真实踩过：
// 建库侧曾是穷举 2~12 字 ngram 子串、检索侧是真分词，两侧求交集几乎撞不上 ——
// 正文里明明挂着「布隆过滤器」，用户搜「布隆」却永远搜不到。
//
// 换掉旧实现顺带修掉两个问题：
//  1. 旧的 englishKeywordPattern（`[A-Za-z0-9_./:-]{2,64}`）**不要求含字母或数字**，
//     markdown 分隔线 `---`、命令行参数 `-p` 都会被当成「英文关键词」入库；
//     现在由 textseg.HasWordChar 兜住。
//  2. 旧实现把候选按词频排序后砍到 20 个，而排序用的计数随后即被丢弃 ——
//     结果是「能不丢的词被丢了」，见 pkg/textseg 的包注释。
//
// 入参 content 恒已过 NormalizeContent（6 个调用点全部如此）：html 已剥离、
// 分隔符已规整，所以这里不再重复处理。
func (s *documentChunkService) extractKeywords(content string) entity.TextArray {
	terms := textseg.Extract(content)
	if len(terms) > chunkKeywordHardLimit {
		terms = terms[:chunkKeywordHardLimit]
	}
	return entity.TextArray(terms)
}
