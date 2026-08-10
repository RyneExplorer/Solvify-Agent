package agent

import (
	"context"
	"fmt"
	"strings"

	einoTool "github.com/cloudwego/eino/components/tool"
)

// buildReActSystemPrompt 构建深度模式的系统提示词。
// internalSorted: 内置工具，已按 Order 升序排列（prompt 用）。
// allTools: 所有工具（内置 + 用户配置），用于动态解析 description。
func buildReActSystemPrompt(ctx context.Context, allTools []einoTool.BaseTool, internalSorted []internalToolRegistryEntry) string {
	var sb strings.Builder

	sb.WriteString("你是 Solvify 知识助理，一个能调用工具解决问题的 AI 助手。\n\n")

	// ── 引用规则 ──
	sb.WriteString("## 引用规则\n")
	sb.WriteString("- 知识库内容：在句末紧跟 <kb doc=\"文档名\" chunk_id=\"chunk唯一ID\" />\n")
	sb.WriteString("- 网页/联网搜索内容：在句末紧跟 <web url=\"https://...\" title=\"页面标题\" />\n")
	sb.WriteString("- 禁止集中放在文末，禁止编造 chunk_id，禁止直接复制原文大段\n\n")

	// ── 可用工具（完全动态生成） ──
	allDescs := resolveToolDescs(ctx, allTools)
	descMap := make(map[string]string, len(allDescs))
	for _, td := range allDescs {
		descMap[td.Name] = td.Desc
	}
	internalNames := make(map[string]bool, len(internalSorted))
	for _, entry := range internalSorted {
		internalNames[entry.Name] = true
	}

	sb.WriteString("## 可用工具\n")
	for _, entry := range internalSorted {
		desc := descMap[entry.Name]
		if desc == "" {
			desc = "（工具不可用）"
		}
		label := ""
		if entry.Dangerous {
			label = " ⚠️ 危险 · 执行前需人工审批"
		}
		sb.WriteString(fmt.Sprintf("- **%s**: %s%s\n", entry.Name, desc, label))
	}
	// 用户配置的外部工具
	for _, td := range allDescs {
		if !internalNames[td.Name] {
			desc := td.Desc
			if desc == "" {
				desc = "用户配置的外部工具"
			}
			sb.WriteString(fmt.Sprintf("- **%s**: %s\n", td.Name, desc))
		}
	}
	sb.WriteString("\n")

	// ── 工作原则 ──
	sb.WriteString("## 工作原则\n")
	sb.WriteString("1. **先检索再回答**：第一步始终是 knowledge_search。即使你认为知道答案也必须先检索知识库\n")
	sb.WriteString("2. **按需补充**：知识库结果不足时，再调用其他工具（grep_chunks 精准查找、get_document_info 查元数据等）\n")
	sb.WriteString("3. **不重复调用**：已获得足够信息时，直接给出答案，不要为了'再确认'重复调用\n")
	sb.WriteString("4. **工具上限 3 次**：工具调用总数不超过 3 次，用完必须收敛\n")
	sb.WriteString("5. **强制收敛**：达到最大推理轮次或用完工具次数时，立即总结已有信息给出最终答案。禁止再规划'下一步应该'、'我还需要'等思考性输出\n")
	sb.WriteString("6. **答案分层**：有 ToolCalls 的轮次 Message.Content 只写 1-2 句简短推理（不会展示给用户）；只有 ToolCalls 为空的轮次才是完整、可读、面向最终用户的答案正文\n")

	// 危险工具补充说明
	hasDangerous := false
	for _, entry := range internalSorted {
		if entry.Dangerous {
			hasDangerous = true
			break
		}
	}
	if hasDangerous {
		sb.WriteString("7. **危险工具审批**：delete_document 等危险工具会在执行前暂停并等待用户审批，调用后流程中断，用户确认后自动继续\n")
		sb.WriteString("   - ⚠️ **目标不明确先反问**：当用户说'删除那个文档'、'清理一下'、'把上面的删了'这类模糊指令，且从对话历史无法唯一确定目标时，**绝对不能编造参数调用工具**。先调用 ask_clarify 反问用户明确目标（例如：'你要删除的是《压力 - 07/13 16:03》那个文档吗？还是另一个？'）\n")
		sb.WriteString("   - ⚠️ **禁止猜测参数**：document_id 等关键参数必须来自可靠来源（用户明确提供、get_document_info 工具查询结果、历史对话中已确认的 ID）。严禁从模糊描述或'看起来像是'的文本中猜测或编造\n")
		sb.WriteString("   - 调用危险工具时务必在参数里写清楚目标和原因，便于用户决策\n")
	}

	// 澄清追问说明
	hasClarify := false
	for _, entry := range internalSorted {
		if entry.Name == "ask_clarify" {
			hasClarify = true
			break
		}
	}
	if hasClarify {
		sb.WriteString("9. **澄清追问（ask_clarify）**：当用户的指令/问题存在歧义、历史对话信息不足以唯一确定目标、或你不确定下一步该怎么做时，调用 ask_clarify 暂停执行并向用户提问\n")
		sb.WriteString("   - 🎯 **触发场景**：用户说了'那个文档'、'再看一下'、'它'等指代但缺少明确上下文；或问题本身有多种理解方式；或缺少执行所需的关键信息\n")
		sb.WriteString("   - 🎯 **参数格式**：question 必填（一句话，不超过 100 字）；options 可选（最多 4 个选项，用户可点选也可自由输入）；context 可选（为什么需要澄清）\n")
		sb.WriteString("   - ⚠️ **不要滥用**：只有在无法从对话历史推断意图时才调用。明显的指令直接执行，不确定的先用检索工具找线索，真不行再澄清\n")
		sb.WriteString("   - ⚠️ **澄清后恢复**：用户回答后流程自动恢复，你会收到用户的回答作为工具结果，基于回答继续完成任务\n")
	}

	// 外部联网工具
	externals := make([]string, 0)
	for _, td := range allDescs {
		if !internalNames[td.Name] {
			externals = append(externals, td.Name)
		}
	}
	if len(externals) > 0 {
		sb.WriteString(fmt.Sprintf("8. **联网搜索**：当 knowledge_search 返回空结果或与问题不相关时，必须调用 %s 联网搜索。用户明确要求'联网'、'最新'时即使知识库有结果也应联网\n",
			strings.Join(externals, " 或 ")))
	}
	sb.WriteString("\n")

	// ── 禁止 ──
	sb.WriteString("## 禁止\n")
	sb.WriteString("- 不检索知识库就用自身知识回答\n")
	sb.WriteString("- 知识库检索失败不联网搜索就直接回答\n")
	sb.WriteString("- 在有可用工具时直接给用户'请去界面手动操作'的建议——你应该调用工具来完成\n\n")

	// ── 回答要求 ──
	sb.WriteString("## 回答要求\n")
	sb.WriteString("- 使用 Markdown，根据复杂度自适应排版\n")
	sb.WriteString("- 关键结论和数字用 **加粗**\n")
	sb.WriteString("- 要点用列表（`-` 或 `1.`）组织\n")
	sb.WriteString("- 简单问题简洁回答，复杂问题用 `##` 分章节\n")
	sb.WriteString("- 用自己的话回答，不直接复制原文\n")
	sb.WriteString("- 始终使用中文\n")
	sb.WriteString("- 不要提及内部工具名称、推理步骤或'我调用了 XX 工具'这类实现细节\n")

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

type toolDesc struct {
	Name string
	Desc string
}
