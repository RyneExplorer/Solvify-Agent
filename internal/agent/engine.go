package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/cloudwego/eino/adk"
	einoTool "github.com/cloudwego/eino/components/tool"

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

// ToolDomain 工具域。一个域 = 一组同职责的工具，最终对应一个 Agent 的工具集。
//
// 域归属是「工具注册时写死的静态事实」，不是运行时按参数推断的 ——
// 这样「快速模式碰不到任何需审批的工具」这类保证才能在编译期就成立。
type ToolDomain string

const (
	// DomainKnowledge 知识库检索域（只读）
	DomainKnowledge ToolDomain = "knowledge"
	// DomainDocMgmt 文档管理域（delete_document，需人工审批）
	DomainDocMgmt ToolDomain = "doc_mgmt"
	// DomainClarify 澄清追问域（ask_clarify）
	DomainClarify ToolDomain = "clarify"
	// DomainExternal 外部工具域（用户 MCP / HTTP，运行时由 ToolFactory 动态创建）
	DomainExternal ToolDomain = "external"
)

// AllToolDomains 全部工具域。深度模式用全集，快速模式只取其中子集。
var AllToolDomains = []ToolDomain{DomainKnowledge, DomainDocMgmt, DomainClarify, DomainExternal}

// QuickToolDomains 快速模式的工具域：知识检索 5 个 + ask_clarify。
//
// 依据 docs/问答模块Agent化重构-方案与决策记录.md §5.2：
// 快速模式**不含** DomainDocMgmt（危险写）与 DomainExternal（用户 MCP），
// 即「快速模式碰不到任何需要人工审批或联网的工具」是结构保证，不靠运行时判断。
var QuickToolDomains = []ToolDomain{DomainKnowledge, DomainClarify}

// clarifyToolName 是澄清追问工具名。判断「本模式挂了哪些交互型工具」时读它，
// 与注册处（internal/app/app.go）同名 —— 改名要同时改两处。
const clarifyToolName = "ask_clarify"

// isKnownDomain 域是否在 AllToolDomains 里。
func isKnownDomain(d ToolDomain) bool {
	return slices.Contains(AllToolDomains, d)
}

// ToolBuildFn 内置工具的构建函数
// 每个工具从请求里取它需要的参数（userID、kbIDs），返回完整可用的 tool 实例
type ToolBuildFn func(ctx context.Context, userID string, kbIDs []string) einoTool.BaseTool

// internalToolRegistryEntry 内置工具注册表项
type internalToolRegistryEntry struct {
	Name      string      // 工具名（用于 prompt 里标记、switch 里分类）
	Order     int         // prompt 里的展示顺序（从小到大）
	Dangerous bool        // 危险工具标记 → prompt 里加 ⚠️ 和审批说明
	Domain    ToolDomain  // 所属工具域 → 决定挂到哪个 Agent
	Build     ToolBuildFn // 构建函数
}

type Engine struct {
	internalTools     []internalToolRegistryEntry
	toolFactory       tool.ToolFactory
	cfg               config.AgentConfig
	checkpointRepo    repository.AgentCheckpointRepo
	checkpointOnce    sync.Once
	memoryCheckpoints *InMemoryCheckPointStore
}

// NewEngine 只收通用依赖。内置工具通过 RegisterInternal 注册。
func NewEngine(
	toolFactory tool.ToolFactory,
	cfg config.AgentConfig,
) EngineInterface {
	return &Engine{
		toolFactory: toolFactory,
		cfg:         cfg,
	}
}

// RegisterInternal 注册一个内置工具。
// order 决定在 prompt "可用工具" 段里的展示顺序（建议：检索类靠前，危险类靠后）。
// dangerous=true 时 prompt 会额外追加危险工具审批说明。
// domain 决定它挂到哪个 Agent；非法域在启动期就 panic —— 写错域会让工具
// 从任何 Agent 的工具集里静默消失，比启动失败难查得多。
func (e *Engine) RegisterInternal(name string, order int, dangerous bool, domain ToolDomain, build ToolBuildFn) {
	if !isKnownDomain(domain) {
		panic(fmt.Sprintf("RegisterInternal(%s): 未知工具域 %q，合法值见 AllToolDomains", name, domain))
	}
	if domain == DomainExternal {
		// DomainExternal 桶只装 ToolFactory 动态创建的工具（append 语义，非覆盖）；
		// 内置工具登记进来会被桶尾追加的动态工具挤掉顺序，或反之被误当作动态工具。
		panic(fmt.Sprintf("RegisterInternal(%s): 内置工具不能登记到 %s，该域只放运行时动态工具", name, DomainExternal))
	}
	e.internalTools = append(e.internalTools, internalToolRegistryEntry{
		Name:      name,
		Order:     order,
		Dangerous: dangerous,
		Domain:    domain,
		Build:     build,
	})
}

func (e *Engine) WithCheckpointRepo(repo repository.AgentCheckpointRepo) {
	e.checkpointRepo = repo
}

// buildCheckpointStore 根据 Engine 配置构造 CheckPointStore。
func (e *Engine) buildCheckpointStore(sessionID string) adk.CheckPointStore {
	if e.checkpointRepo != nil && sessionID != "" {
		return NewDBCheckPointStore(e.checkpointRepo, sessionID, CheckpointTTL)
	}
	e.checkpointOnce.Do(func() { e.memoryCheckpoints = NewInMemoryCheckPointStore() })
	return e.memoryCheckpoints
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
		if entry.Name == clarifyToolName {
			m[entry.Name] = true
		}
	}
	return m
}
