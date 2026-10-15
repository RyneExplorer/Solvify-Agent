package agent

import (
	"context"
	"strings"
	"testing"

	einoTool "github.com/cloudwego/eino/components/tool"
)

func TestPlannerUsesCurrentKnowledgeToolDescriptions(t *testing.T) {
	r := &peRuntime{engine: deepTestEngine()}
	buckets := map[ToolDomain][]einoTool.BaseTool{DomainKnowledge: {namedTestTool{name: "existing_knowledge_tool"}, namedTestTool{name: "new_knowledge_tool"}}}
	if err := r.describeTools(context.Background(), buckets); err != nil {
		t.Fatal(err)
	}
	instruction := r.plannerInstruction()
	for _, name := range []string{"existing_knowledge_tool", "new_knowledge_tool"} {
		if !strings.Contains(instruction, name) {
			t.Fatalf("Planner无法识别当前知识工具 %s", name)
		}
	}
	if r.external || r.document {
		t.Fatal("知识工具扩大了其他角色能力")
	}
	// 删除或权限过滤后重新描述，旧工具不能留在恢复后的Planner输入里。
	buckets[DomainKnowledge] = buckets[DomainKnowledge][:1]
	if err := r.describeTools(context.Background(), buckets); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.plannerInstruction(), "new_knowledge_tool") {
		t.Fatal("Planner仍看到已移除工具")
	}
	buckets[DomainKnowledge] = []einoTool.BaseTool{namedTestTool{name: ""}}
	if err := r.describeTools(context.Background(), buckets); err == nil {
		t.Fatal("无效知识工具描述被放行")
	}
}
