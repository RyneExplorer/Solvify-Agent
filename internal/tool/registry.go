package tool

import (
	"fmt"

	"solvify-agent/internal/llm"
)

// Registry 管理所有可用工具
type Registry struct {
	tools map[string]Tool
}

// NewRegistry 创建工具注册表
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

// Register 注册一个工具
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Get 根据名称获取工具
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// List 返回所有已注册工具
func (r *Registry) List() []Tool {
	result := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	return result
}

// ToLLMTools 将注册的工具转换为 LLM Tool 格式
func (r *Registry) ToLLMTools() []llm.Tool {
	result := make([]llm.Tool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, llm.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}
	return result
}

// ToToolMap 返回工具执行映射表（name → Tool），供 Agent 并发执行工具
func (r *Registry) ToToolMap() map[string]Tool {
	result := make(map[string]Tool, len(r.tools))
	for name, t := range r.tools {
		result[name] = t
	}
	return result
}

// MustRegister 注册工具，如果名称重复则 panic
func (r *Registry) MustRegister(t Tool) *Registry {
	name := t.Name()
	if _, exists := r.tools[name]; exists {
		panic(fmt.Sprintf("工具 %s 已注册", name))
	}
	r.tools[name] = t
	return r
}
