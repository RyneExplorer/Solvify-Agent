package agent

import (
	"context"
	"encoding/json"
	"sort"

	einoTool "github.com/cloudwego/eino/components/tool"

	"solvify-agent/pkg/tokenutil"
)

type prebuiltToolsCtxKeyType struct{}

var prebuiltToolsCtxKey = prebuiltToolsCtxKeyType{}

type prebuiltToolsBundle struct {
	Tools       []einoTool.BaseTool
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

// buildAllTools 用 registry 构建所有内置工具 + 用户配置工具
func (e *Engine) buildAllTools(ctx context.Context, userID string, kbIDs []string) ([]einoTool.BaseTool, error) {
	var allTools []einoTool.BaseTool
	sorted := make([]internalToolRegistryEntry, len(e.internalTools))
	copy(sorted, e.internalTools)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Order < sorted[j].Order })
	for _, entry := range sorted {
		allTools = append(allTools, entry.Build(ctx, userID, kbIDs))
	}
	allTools = append(allTools, e.toolFactory.CreateAgentTools(ctx, userID)...)
	return allTools, nil
}

// EstimateToolsTokens 返回「工具定义的真 BPE token 数」以及预构建好的工具集。
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
		bs, mErr := json.Marshal(info)
		if mErr != nil {
			total += tokenutil.CountTokens(info.Name+"\n"+info.Desc, modelName)
			continue
		}
		total += tokenutil.CountTokens(string(bs), modelName)
	}
	overhead := int(float64(total) * 0.2)
	if overhead < 200 {
		overhead = 200
	}
	total += overhead
	bundle := prebuiltToolsBundle{Tools: tools, TotalTokens: total}
	return total, withPrebuiltTools(ctx, bundle), nil
}
