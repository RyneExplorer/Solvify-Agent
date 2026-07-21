package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"solvify-agent/pkg/logger"
)

// modelContextRegistry 维护已知模型的上下文窗口长度。
// 该表用于用户未手动填写 max_context_length 时自动推断。
var modelContextRegistry = map[string]int{
	// OpenAI
	"gpt-4o":            128000,
	"gpt-4o-mini":       128000,
	"gpt-4-turbo":       128000,
	"gpt-4":             8192,
	"gpt-4-32k":         32000,
	"gpt-3.5-turbo":     16385,
	"gpt-3.5-turbo-16k": 16000,
	"o1":                200000,
	"o1-mini":           128000,
	"o3-mini":           200000,

	// DeepSeek
	"deepseek-chat":     64000,
	"deepseek-coder":    64000,
	"deepseek-reasoner": 64000,
	"deepseek-v3":       64000,
	"deepseek-v4":       64000,
	"deepseek-v4-pro":   64000,
	"deepseek-v4-flash": 64000,

	// 通义千问
	"qwen-turbo":           8192,
	"qwen-plus":            32000,
	"qwen-max":             32000,
	"qwen-max-longcontext": 32000,
	"qwen2.5":              128000,
	"qwen2.5-72b":          128000,
	"qwen3":                128000,
	"qwen3.6-plus":         128000,
	"qwen3.7-plus":         128000,

	// 智谱
	"glm-4":         128000,
	"glm-4-flash":   128000,
	"glm-4v":        8192,
	"glm-4-9b":      128000,
	"glm-4-32k":     32000,
	"glm-5":         128000,
	"glm-5.1":       128000,
	"glm-5.2":       128000,
	"glm-5v-turbo":  128000,
	"glm-4.7-flash": 128000,
	"chatglm3-6b":   8192,

	// Moonshot
	"moonshot-v1-8k":   8192,
	"moonshot-v1-32k":  32000,
	"moonshot-v1-128k": 128000,

	// Kimi
	"kimi-k2.6":      200000,
	"kimi-k2.7":      200000,
	"kimi-k2.7-code": 200000,

	// Doubao / MiniMax
	"doubao-seed-2.1-pro":   128000,
	"doubao-seed-2.1-turbo": 128000,
	"doubao-seed-code":      128000,
	"minimax-m3":            100000,
	"minimax-m2.7":          100000,

	// Gitee AI 常见模型
	"bge-m3":          8192,
	"qwen3-235b-a22b": 128000,
}

// 从模型名解析上下文标识，例如 glm-4-128k → 128000
var contextPattern = regexp.MustCompile(`(?i)(\d+)\s*(k|千)`)

// InferMaxContextLength 推断模型上下文窗口长度。
// 优先级：用户填写值 > 注册表精确匹配 > 模型名解析 > 默认 8192
func InferMaxContextLength(modelID string, userProvided int) int {
	if userProvided > 0 {
		return userProvided
	}

	modelID = strings.TrimSpace(modelID)

	// 1. 注册表精确匹配
	if ctx, ok := modelContextRegistry[modelID]; ok {
		return ctx
	}

	// 2. 从模型名解析上下文标识
	if ctx := parseContextFromName(modelID); ctx > 0 {
		return ctx
	}

	// 3. 兜底默认值
	return 8192
}

func parseContextFromName(modelID string) int {
	matches := contextPattern.FindStringSubmatch(modelID)
	if len(matches) < 3 {
		return 0
	}

	num, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0
	}

	unit := strings.ToLower(matches[2])
	switch unit {
	case "k", "千":
		return num * 1000
	}
	return num
}

// ─── 运行时上下文窗口保护 ───────────────────────────────────────────

// effectiveContextLengths 记录每个模型经运行时错误修正后的有效上下文长度。
// key: modelID，value: 当前认为安全的最大上下文长度。
var (
	effectiveContextLengths   = make(map[string]int)
	effectiveContextLengthsMu sync.RWMutex
)

// GetEffectiveMaxContextLength 获取模型当前有效的上下文窗口长度。
// 如果运行时因错误被降低过，返回降低后的值；否则返回声明值。
func GetEffectiveMaxContextLength(modelID string, declared int) int {
	effectiveContextLengthsMu.RLock()
	defer effectiveContextLengthsMu.RUnlock()

	if effective, ok := effectiveContextLengths[modelID]; ok && effective < declared {
		return effective
	}
	return declared
}

// ResetEffectiveMaxContextLength 重置某模型的运行时有效上下文长度（例如用户更新配置后）。
func ResetEffectiveMaxContextLength(modelID string) {
	effectiveContextLengthsMu.Lock()
	defer effectiveContextLengthsMu.Unlock()
	delete(effectiveContextLengths, modelID)
}

// contextLengthErrorKeywords 用于识别上下文长度超限错误。
var contextLengthErrorKeywords = []string{
	"context length", "context_length", "maximum context length",
	"token limit", "token_limit", "max tokens", "max_tokens",
	"too long", "request too large", "exceeds", "exceed",
	"context window", "上下文长度", "token 数超过", "超出最大长度",
	"tokens limit", "input length", "prompt is too long",
}

// ReduceContextBudgetOnError 当模型返回上下文长度相关错误时，降低该模型的有效预算。
func ReduceContextBudgetOnError(modelID string, err error) {
	if err == nil || !looksLikeContextLengthError(err) {
		return
	}

	effectiveContextLengthsMu.Lock()
	defer effectiveContextLengthsMu.Unlock()

	current, ok := effectiveContextLengths[modelID]
	if !ok {
		// 首次错误时先降到 8k（实际生效值还会受声明值限制）
		effectiveContextLengths[modelID] = 8192
		logger.Warnf("模型 %s 首次触发上下文长度错误，有效上下文窗口已降低为 8192", modelID)
		return
	}

	// 后续错误继续减半，但不少于 1024
	newValue := current / 2
	if newValue < 1024 {
		newValue = 1024
	}

	effectiveContextLengths[modelID] = newValue
	logger.Warnf("模型 %s 再次触发上下文长度错误，有效上下文窗口已降低为 %d", modelID, newValue)
}

func looksLikeContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, kw := range contextLengthErrorKeywords {
		if strings.Contains(msg, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// ─── /v1/models 接口探测 ───────────────────────────────────────────

// ModelListItem 描述 /v1/models 返回的单个模型信息
type ModelListItem struct {
	ID            string `json:"id"`
	Object        string `json:"object"`
	Created       int64  `json:"created"`
	OwnedBy       string `json:"owned_by"`
	ContextWindow int    `json:"context_window,omitempty"`
	MaxTokens     int    `json:"max_tokens,omitempty"`
}

// ModelsListResponse 描述 /v1/models 返回结构
type ModelsListResponse struct {
	Object string          `json:"object"`
	Data   []ModelListItem `json:"data"`
}

// DetectContextLengthViaAPI 尝试通过 /v1/models 接口探测指定模型的上下文窗口。
// 如果接口不可用、未返回该模型或未包含上下文信息，返回 0 和错误。
func DetectContextLengthViaAPI(ctx context.Context, baseURL, apiKey, modelID string) (int, error) {
	if baseURL == "" || modelID == "" {
		return 0, fmt.Errorf("baseURL 或 modelID 为空")
	}

	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := getSharedHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("/v1/models 返回非 200: %d, %s", resp.StatusCode, string(body))
	}

	var list ModelsListResponse
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return 0, err
	}

	for _, m := range list.Data {
		if m.ID == modelID {
			if m.ContextWindow > 0 {
				return m.ContextWindow, nil
			}
			if m.MaxTokens > 0 {
				return m.MaxTokens, nil
			}
			return 0, fmt.Errorf("/v1/models 返回模型 %s 但未包含 context_window", modelID)
		}
	}

	return 0, fmt.Errorf("/v1/models 未找到模型 %s", modelID)
}
