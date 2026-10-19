package agent

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// JSON Pointer按RFC6901定位字段，不执行代码或模型生成的表达式。
func artifactJSONPointer(content, pointer string) (string, error) {
	if !json.Valid([]byte(content)) {
		return "", fmt.Errorf("原文不是有效JSON")
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("原文不是有效JSON")
	}
	if pointer != "" && !strings.HasPrefix(pointer, "/") {
		return "", fmt.Errorf("json_path必须为空或以/开头的JSON Pointer")
	}
	if pointer != "" {
		for _, part := range strings.Split(pointer[1:], "/") {
			for i := 0; i < len(part); i++ {
				if part[i] == '~' {
					if i+1 >= len(part) || (part[i+1] != '0' && part[i+1] != '1') {
						return "", fmt.Errorf("json_path转义无效")
					}
					i++
				}
			}
			key := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			switch current := value.(type) {
			case map[string]any:
				var ok bool
				value, ok = current[key]
				if !ok {
					return "", fmt.Errorf("json_path未找到对应字段")
				}
			case []any:
				index, err := strconv.Atoi(key)
				if err != nil || index < 0 || index >= len(current) || strconv.Itoa(index) != key {
					return "", fmt.Errorf("json_path数组索引无效")
				}
				value = current[index]
			default:
				return "", fmt.Errorf("json_path不能继续展开该值")
			}
		}
	}
	raw, err := json.Marshal(value)
	return string(raw), err
}

func artifactSection(content, section string) (int, int, bool) {
	position := 0
	start, end, level := -1, len([]rune(content)), 0
	fence := ""
	for _, line := range strings.SplitAfter(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
			position += len([]rune(line))
			continue
		}
		if fence != "" {
			position += len([]rune(line))
			continue
		}
		heading := strings.TrimLeft(trimmed, "#")
		n := len(trimmed) - len(heading)
		if n > 0 && n <= 6 && strings.HasPrefix(heading, " ") {
			if start >= 0 && n <= level {
				return start, position, true
			}
			if start < 0 && strings.EqualFold(strings.TrimSpace(heading), strings.TrimSpace(strings.TrimLeft(section, "#"))) {
				start, level = position, n
			}
		}
		position += len([]rune(line))
	}
	return start, end, start >= 0
}

func artifactKnowledgeChunk(content, id string) (string, error) {
	var value struct {
		Sources []json.RawMessage `json:"sources"`
	}
	if json.Unmarshal([]byte(content), &value) != nil {
		return "", fmt.Errorf("原文不是知识库结果JSON")
	}
	for _, source := range value.Sources {
		var key struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(source, &key) == nil && key.ID == id {
			return string(source), nil
		}
	}
	return "", fmt.Errorf("未找到该知识分块，请使用结果中的真实id")
}

func sameArtifactSelection(a, b *artifactSelection) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// 对象/数组按完整成员投影，保持有效JSON；省略项通过外层complete与原文引用公开。
func projectJSONMembers(content string, fits func(string) bool) (string, bool) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return "", false
	}
	encode := func(value any) string { raw, _ := json.Marshal(value); return string(raw) }
	var list []any
	var object map[string]any
	field := ""
	switch v := value.(type) {
	case []any:
		list = v
	case map[string]any:
		object = v
		for _, key := range []string{"sources", "results", "data", "content", "items"} {
			if entries, ok := v[key].([]any); ok {
				list, field = entries, key
				break
			}
		}
		if field == "" {
			return "", false
		}
	default:
		return "", false
	}
	build := func(items []any) string {
		if object == nil {
			return encode(items)
		}
		copy := make(map[string]any, len(object))
		for k, v := range object {
			copy[k] = v
		}
		copy[field] = items
		return encode(copy)
	}
	// 首先压掉重复摘要或巨大附加字段，再分配完整记录。
	if object != nil && !fits(build([]any{})) {
		reduced := map[string]any{}
		for _, key := range []string{"success", "message", "code", "isError", "_meta"} {
			if v, ok := object[key]; ok {
				reduced[key] = v
			}
		}
		object = reduced
	}
	// 保留原有排名的连续前缀；二分成员数，避免大数组逐条序列化的平方开销。
	low, high, count := 0, len(list), 0
	for low <= high {
		mid := low + (high-low)/2
		if fits(build(list[:mid])) {
			count, low = mid, mid+1
		} else {
			high = mid - 1
		}
	}
	chosen := list[:count]
	if chosen == nil {
		chosen = []any{}
	}
	result := build(chosen)
	return result, fits(result)
}
