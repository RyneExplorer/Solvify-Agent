package agent

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type mcpBoundaryModel struct {
	*deepTestModel
	bound []string
}

func (m *mcpBoundaryModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	m.recordTools(tools)
	return m, nil
}

func (m *mcpBoundaryModel) recordTools(tools []*schema.ToolInfo) {
	for _, info := range tools {
		m.bound = append(m.bound, info.Name)
	}
}

func (m *mcpBoundaryModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	m.recordTools(model.GetCommonOptions(nil, opts...).Tools)
	return m.deepTestModel.Generate(ctx, messages, opts...)
}

func (m *mcpBoundaryModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	m.recordTools(model.GetCommonOptions(nil, opts...).Tools)
	return m.deepTestModel.Stream(ctx, messages, opts...)
}

func TestMCPMountedOnlyOnDeepSpecialist(t *testing.T) {
	for _, tc := range []struct {
		name     string
		profile  Profile
		agent    string
		prebuilt bool
		wantMCP  bool
	}{
		{"快速模式过滤预构建MCP", ProfileQuick, "", true, false},
		{"普通深度过滤预构建MCP", ProfileDeep, "", true, false},
		{"普通深度不发现MCP", ProfileDeep, "", false, false},
		{"专业子Agent获得MCP", ProfileDeep, "web_research", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(deepTestContext(t), 5*time.Second)
			defer cancel()
			var calls atomic.Int32
			if tc.prebuilt {
				ctx = withPrebuiltTools(ctx, prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainExternal: {deepApprovalTool{calls: &calls}}}})
			}
			factory := &countingToolFactory{names: []string{"unexpected"}}
			e := &Engine{toolFactory: factory}
			m := &mcpBoundaryModel{deepTestModel: &deepTestModel{respond: func(context.Context, []*schema.Message) (*schema.Message, error) {
				return schema.AssistantMessage("完成", nil), nil
			}}}
			events, err := e.Execute(ctx, Request{Profile: tc.profile, UserID: "u", SessionID: tc.name, Query: "测试", AgentName: tc.agent, MaxContextLength: 64000, ModelName: "gpt-4o"}, m)
			if err != nil {
				t.Fatal(err)
			}
			done := false
			for ev := range events {
				if ev.Type == EventError {
					t.Fatalf("执行失败: %+v", ev)
				}
				done = done || ev.Type == EventDone
			}
			if !done || slices.Contains(m.bound, "write_file") != tc.wantMCP || factory.calls != 0 {
				t.Fatalf("done=%v 挂载=%v MCP发现次数=%d", done, m.bound, factory.calls)
			}
		})
	}
}
