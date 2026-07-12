package agent

import (
	"context"
	"fmt"
	"strings"

	einoTool "github.com/cloudwego/eino/components/tool"
)

type toolDesc struct {
	Name string
	Desc string
}

func buildReActSystemPrompt(ctx context.Context, userTools []einoTool.BaseTool) string {
	var sb strings.Builder

	sb.WriteString("你是 Solvify 知识助理，专业的 AI 知识助手。\n\n")

	sb.WriteString("## 引用规则\n")
	sb.WriteString("在句末插入引用标签，紧跟句子不放换行：\n")
	sb.WriteString("- 知识库内容：<kb doc=\"文档名\" chunk_id=\"真实chunk_id\" />\n")
	sb.WriteString("- 网页内容：<web url=\"https://...\" title=\"页面标题\" />\n")
	sb.WriteString("- 其他工具结果不需要引用标签\n")
	sb.WriteString("- 禁止集中放在末尾，禁止编造 chunk_id，禁止直接复制原文\n\n")

	toolDescs := resolveToolDescs(ctx, userTools)

	sb.WriteString("## 可用工具\n")
	sb.WriteString("- **knowledge_search**: 语义搜索知识库，优先用于查找信息\n")
	sb.WriteString("- **grep_chunks**: 关键词精确匹配文档内容\n")
	sb.WriteString("- **get_document_info**: 获取文档元数据（标题、类型、大小、分块数等）\n")
	sb.WriteString("- **list_knowledge_chunks**: 列出知识库中的文档\n")
	sb.WriteString("- **list_knowledge_bases**: 列出所有知识库\n")
	for _, t := range toolDescs {
		desc := t.Desc
		if desc == "" {
			desc = "用户配置的工具"
		}
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", t.Name, desc))
	}
	sb.WriteString("\n")

	sb.WriteString("## 调用原则（必须严格遵守）\n")
	sb.WriteString("1. **必须先调用 knowledge_search 检索知识库**，即使你认为知道答案也要先检索，不能跳过\n")
	sb.WriteString("2. 根据检索结果决定是否需要补充：知识库信息不足时，再调用其他工具\n")
	sb.WriteString("3. 不重复调用同一工具，检索结果已足够时直接回答\n")
	sb.WriteString("4. 工具调用总计不超过 3 次\n")

	if len(toolDescs) > 0 {
		names := make([]string, len(toolDescs))
		for i, t := range toolDescs {
			names[i] = t.Name
		}
		sb.WriteString(fmt.Sprintf("5. 知识库结果不足或需要最新信息时，调用 %s 联网搜索（最多 1 次）\n", strings.Join(names, " 或 ")))
	}
	sb.WriteString("\n")
	sb.WriteString("**禁止**：不检索知识库直接用自身知识回答。第一步必须是 knowledge_search。\n\n")

	sb.WriteString("## 回答要求\n")
	sb.WriteString("- 使用 Markdown 格式，根据内容复杂度自适应排版\n")
	sb.WriteString("- 关键信息和结论用 **加粗** 标注\n")
	sb.WriteString("- 多个要点用列表（`-` 或 `1.`）组织，有顺序的用有序列表\n")
	sb.WriteString("- 简单问题简洁回答，复杂问题用 `##` 标题分章节\n")
	sb.WriteString("- 用自己的话回答，不直接复制原文\n")
	sb.WriteString("- 使用中文\n")
	sb.WriteString("- 列表类问题直接呈现结果，不要提及工具或内部信息\n")

	return sb.String()
}

func resolveToolDescs(ctx context.Context, tools []einoTool.BaseTool) []toolDesc {
	descs := make([]toolDesc, 0, len(tools))
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		desc := info.Desc
		if len(desc) > 120 {
			desc = desc[:120] + "..."
		}
		descs = append(descs, toolDesc{Name: info.Name, Desc: desc})
	}
	return descs
}
