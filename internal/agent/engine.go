package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/adk"
	einoTool "github.com/cloudwego/eino/components/tool"

	"solvify-agent/internal/observability"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
)

// dangerousDynamicPatterns 动态工具（MCP 等）名称中的高危操作关键词。
// MCP 工具由 ToolFactory 在运行时动态创建，不在 internalTools 注册表里，
// 若不在此按名称兜底识别，write_file / edit_file / move_file 这类写操作
// 会完全绕过人工审批直接执行。
var dangerousDynamicPatterns = []string{
	"write", "edit", "delete", "remove", "move", "rename",
	"mkdir", "create_directory", "execute", "run_", "kill", "terminate",
}

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

// dangerousToolNames 返回需要人工审批的工具名集合，用于构建审批中间件。
// 来源一：内置工具注册时显式标记的 Dangerous；
// 来源二：动态工具（MCP 等）按名称模式识别出的写/删除类高危操作。
func (e *Engine) dangerousToolNames(ctx context.Context, allTools []einoTool.BaseTool) map[string]bool {
	m := make(map[string]bool, len(e.internalTools))
	internalSet := make(map[string]bool, len(e.internalTools))
	for _, entry := range e.internalTools {
		if entry.Dangerous {
			m[entry.Name] = true
		}
		internalSet[entry.Name] = true
	}
	for _, t := range allTools {
		info, err := t.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		if internalSet[info.Name] {
			// 内置工具已按显式 Dangerous 标记处理，不参与模式匹配
			continue
		}
		if isDangerousDynamicTool(info.Name) {
			m[info.Name] = true
		}
	}
	return m
}

// isDangerousDynamicTool 判断动态工具（MCP 等）名称是否属于高危写/删除类操作
func isDangerousDynamicTool(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range dangerousDynamicPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
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
