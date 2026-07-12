package dingtalk

import (
	"fmt"
	"strconv"
	"strings"
)

// DocumentBlocksToMarkdown 将钉钉在线文档块元素转换为 Markdown
func DocumentBlocksToMarkdown(blocks []DocumentBlock) string {
	lines := make([]string, 0, len(blocks))
	for _, block := range blocks {
		blockType := strings.TrimSpace(stringValue(block["blockType"]))
		payload, _ := block[blockType].(map[string]any)
		text := blockText(payload)
		if text == "" {
			text = blockText(block)
		}
		if text == "" {
			continue
		}
		switch blockType {
		case "heading":
			lines = append(lines, strings.Repeat("#", headingLevel(payload))+" "+text)
		case "blockquote":
			lines = append(lines, "> "+text)
		case "orderedList", "ordered_list":
			lines = append(lines, "1. "+text)
		case "unorderedList", "unordered_list", "bulletList":
			lines = append(lines, "- "+text)
		default:
			lines = append(lines, text)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n\n"))
}

// blockText 提取块元素中的可读文本
func blockText(value any) string {
	switch current := value.(type) {
	case string:
		return strings.TrimSpace(current)
	case []any:
		parts := make([]string, 0, len(current))
		for _, item := range current {
			if text := blockText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "")
	case map[string]any:
		for _, key := range []string{"text", "plainText", "content", "value"} {
			if text := blockText(current[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

// headingLevel 解析标题层级
func headingLevel(payload map[string]any) int {
	if payload == nil {
		return 1
	}
	raw := strings.TrimPrefix(strings.TrimSpace(stringValue(payload["level"])), "heading-")
	level, err := strconv.Atoi(raw)
	if err != nil || level < 1 || level > 6 {
		return 1
	}
	return level
}

// stringValue 将基础类型转换为字符串
func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
