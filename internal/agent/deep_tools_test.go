package agent

import (
	"context"
	"testing"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type namedTestTool struct{ name string }

func (t namedTestTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name}, nil
}
func (t namedTestTool) InvokableRun(context.Context, string, ...einoTool.Option) (string, error) {
	return "", nil
}

var _ einoTool.InvokableTool = namedTestTool{}

func TestSelectRequestToolsUsesExplicitDomains(t *testing.T) {
	sorted := []internalToolRegistryEntry{
		{Name: "knowledge", Domain: DomainKnowledge},
		{Name: "docs", Domain: DomainDocMgmt},
	}
	buckets := map[ToolDomain][]einoTool.BaseTool{
		DomainKnowledge: {namedTestTool{name: "knowledge"}},
		DomainDocMgmt:   {namedTestTool{name: "docs"}},
	}
	req := Request{Profile: ProfileDeep, ToolDomains: []ToolDomain{DomainKnowledge}}
	set := selectRequestTools(sorted, buckets, req)
	if len(set.Tools) != 1 || set.Tools[0] == nil {
		t.Fatalf("unexpected tools: %#v", set.Tools)
	}
	info, _ := set.Tools[0].Info(context.Background())
	if info.Name != "knowledge" {
		t.Fatalf("tool domain leaked: %s", info.Name)
	}
	if set := selectRequestTools(sorted, buckets, Request{Profile: ProfileDeep, ToolDomains: []ToolDomain{}}); len(set.Tools) != 0 || len(set.Entries) != 0 {
		t.Fatal("汇总 Agent 不应获得工具")
	}
	if set := selectRequestTools(sorted, buckets, Request{Profile: ProfileDeep}); len(set.Tools) != 2 {
		t.Fatal("普通 Agent 应沿用 profile 工具集")
	}
}
