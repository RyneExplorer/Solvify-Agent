package agent

import (
	"context"
	"encoding/json"

	einoTool "github.com/cloudwego/eino/components/tool"

	"solvify-agent/pkg/tokenutil"
)

// prebuiltToolsCtxKey 用作 context.WithValue 键，把深度模式入口"预构建好的工具集 + tokens 信息"
// 传到 runAgent 里，避免 runAgent 再调一次 factories 重复构建，同时保证 initContext 扣减的
// ToolsTokens 和实际发给模型的工具定义完全一致（P0-④ 的根保证）。
type prebuiltToolsCtxKeyType struct{}

var prebuiltToolsCtxKey = prebuiltToolsCtxKeyType{}

type prebuiltToolsBundle struct {
	Tools      []einoTool.BaseTool
	TotalTokens int
}

func withPrebuiltTools(ctx context.Context, bundle prebuiltToolsBundle) context.Context {
	return context.WithValue(ctx, prebuiltToolsCtxKey, bundle)
}

func prebuiltToolsFromContext(ctx context.Context) (prebuiltToolsBundle, bool) {
	v := ctx.Value(prebuiltToolsCtxKey)
	if v == nil {
		return prebuiltToolsBundle{}, false
	}
	b, ok := v.(prebuiltToolsBundle)
	return b, ok
}

// buildAllTools 集中封装一次工具构建流程：知识库 5 个内置工具 + 用户工具。
// 与 runAgent 里原有的构建顺序保持完全一致，避免"预构建版本 vs runAgent 版本"两套逻辑漂移。
func (e *Engine) buildAllTools(ctx context.Context, userID string, kbIDs []string) ([]einoTool.BaseTool, error) {
	ksTool := e.knowledgeSearchFactory(userID, kbIDs)
	grepTool := e.grepChunksFactory(userID, kbIDs)
	docInfoTool := e.getDocumentInfoFactory(userID)
	listChunksTool := e.listKnowledgeChunksFactory(userID, kbIDs)
	listBasesTool := e.listKnowledgeBasesFactory(userID)
	userTools := e.toolFactory.CreateAgentTools(ctx, userID)

	allTools := make([]einoTool.BaseTool, 0, 5+len(userTools))
	allTools = append(allTools, ksTool)
	allTools = append(allTools, grepTool)
	allTools = append(allTools, docInfoTool)
	allTools = append(allTools, listChunksTool)
	allTools = append(allTools, listBasesTool)
	allTools = append(allTools, userTools...)
	return allTools, nil
}

// EstimateToolsTokens 返回「工具定义的真 BPE token 数」以及预构建好的工具集。
// 调用方用返回的 ToolsTokens 喂给 initContext（calculateContextBudgets 先扣），再把 prebuilt 放进 ctx，
// 之后 Execute 会复用这份工具集，保证前后 token 计算一致。
func (e *Engine) EstimateToolsTokens(ctx context.Context, userID string, kbIDs []string, modelName string) (int, context.Context, error) {
	tools, err := e.buildAllTools(ctx, userID, kbIDs)
	if err != nil {
		return 0, ctx, err
	}
	total := 0
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		// ToolInfo.MarshalJSON 已经包含了 Name/Desc/Extra/ParamsOneOf → 传给模型的完整定义
		bs, mErr := json.Marshal(info)
		if mErr != nil {
			// 实在 marshal 失败，就退化成 desc+name 粗略估算
			total += tokenutil.CountTokens(info.Name+"\n"+info.Desc, modelName)
			continue
		}
		total += tokenutil.CountTokens(string(bs), modelName)
	}
	// ReAct Agent 在 system prompt 里还会加一段"可用工具一览 / How to use tools"的指令，
	// 经验上 ≈ tools_tokens 的 15%，保守取 20%。
	overhead := int(float64(total) * 0.2)
	if overhead < 200 {
		overhead = 200
	}
	total += overhead
	bundle := prebuiltToolsBundle{Tools: tools, TotalTokens: total}
	return total, withPrebuiltTools(ctx, bundle), nil
}
