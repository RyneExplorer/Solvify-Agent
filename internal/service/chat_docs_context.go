package service

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/rag"
	"solvify-agent/pkg/tokenutil"
)

// buildDocsContextBlock 按 score 占比分配 token 预算，格式化检索文档为引用上下文。
// 纯函数：输入检索到的文档 + 检索预算 + 模型名，输出拼好的上下文块字符串。
// 与 Graph 装配、流式消费等编排逻辑解耦，便于独立单测。
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
	// 降序：高分段先分配。
	// 必须全序：`s <= 0` 会被兜底成同一个 1e-6 ⇒ **所有 0 分文档彼此同分**，这不是边角情况。
	// 同分时按 idx（检索器给的原顺序）裁决 ⇒ 「谁先拿预算」有确定规则，而不是由排序算法决定。
	sort.Slice(scoredDocs, func(i, j int) bool {
		if scoredDocs[i].score != scoredDocs[j].score {
			return scoredDocs[i].score > scoredDocs[j].score
		}
		return scoredDocs[i].idx < scoredDocs[j].idx
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
