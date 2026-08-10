package agent

import (
	"context"

	"github.com/cloudwego/eino/adk"
	einoTool "github.com/cloudwego/eino/components/tool"

	"solvify-agent/internal/observability"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
)

// ToolBuildFn 内置工具的构建函数
// 每个工具从请求里取它需要的参数（userID、kbIDs），返回完整可用的 tool 实例
type ToolBuildFn func(ctx context.Context, userID string, kbIDs []string) einoTool.BaseTool

// internalToolRegistryEntry 内置工具注册表项
type internalToolRegistryEntry struct {
	Name      string       // 工具名（用于 prompt 里标记、switch 里分类）
	Order     int          // prompt 里的展示顺序（从小到大）
	Dangerous bool         // 危险工具标记 → prompt 里加 ⚠️ 和审批说明
	Build     ToolBuildFn  // 构建函数
}

type Engine struct {
	internalTools []internalToolRegistryEntry
	toolFactory   tool.ToolFactory
	cfg           config.AgentConfig
	obs           observability.Recorder
	checkpointRepo repository.AgentCheckpointRepo
}

// NewEngine 只收通用依赖。内置工具通过 RegisterInternal 注册。
func NewEngine(
	toolFactory tool.ToolFactory,
	cfg config.AgentConfig,
	obs ...observability.Recorder,
) *Engine {
	e := &Engine{
		toolFactory: toolFactory,
		cfg:         cfg,
	}
	if len(obs) > 0 && obs[0] != nil {
		e.obs = obs[0]
	}
	return e
}

// RegisterInternal 注册一个内置工具。
// order 决定在 prompt "可用工具" 段里的展示顺序（建议：检索类靠前，危险类靠后）。
// dangerous=true 时 prompt 会额外追加危险工具审批说明。
func (e *Engine) RegisterInternal(name string, order int, dangerous bool, build ToolBuildFn) {
	e.internalTools = append(e.internalTools, internalToolRegistryEntry{
		Name:      name,
		Order:     order,
		Dangerous: dangerous,
		Build:     build,
	})
}

func (e *Engine) WithObservability(obs observability.Recorder) {
	e.obs = obs
}

func (e *Engine) WithCheckpointRepo(repo repository.AgentCheckpointRepo) {
	e.checkpointRepo = repo
}

// buildCheckpointStore 根据 Engine 配置构造 CheckPointStore。
func (e *Engine) buildCheckpointStore(sessionID string) adk.CheckPointStore {
	if e.checkpointRepo != nil && sessionID != "" {
		return NewDBCheckPointStore(e.checkpointRepo, sessionID, CheckpointTTL)
	}
	return NewInMemoryCheckPointStore()
}

// dangerousToolNames 返回所有标记为 dangerous 的内置工具名集合。
// 用于构建审批中间件。
func (e *Engine) dangerousToolNames() map[string]bool {
	m := make(map[string]bool, len(e.internalTools))
	for _, entry := range e.internalTools {
		if entry.Dangerous {
			m[entry.Name] = true
		}
	}
	return m
}

func (e *Engine) clarifyToolNames() map[string]bool {
	m := make(map[string]bool)
	for _, entry := range e.internalTools {
		if entry.Name == "ask_clarify" {
			m[entry.Name] = true
		}
	}
	return m
}
