package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/tool"
)

type timeoutInternalTool struct {
	calls atomic.Int32
	name  string
}

func (t *timeoutInternalTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name, Desc: "可取消工具"}, nil
}
func (t *timeoutInternalTool) InvokableRun(ctx context.Context, _ string, _ ...einotool.Option) (string, error) {
	t.calls.Add(1)
	<-ctx.Done()
	return "", ctx.Err()
}

func TestInternalToolTimeoutAtRealToolsNode(t *testing.T) {
	for _, mode := range []string{"execution", "parent", "canceled", "approval", "gate"} {
		t.Run(mode, func(t *testing.T) {
			e := &Engine{toolTimeout: 35 * time.Millisecond, internalTools: []internalToolRegistryEntry{{Name: "knowledge_search", Domain: DomainKnowledge}}}
			slow := &timeoutInternalTool{name: "knowledge_search"}
			cfg := buildToolsNodeConfig()
			cfg.Tools = []einotool.BaseTool{slow}
			if mode == "approval" {
				cfg.ToolCallMiddlewares = append(cfg.ToolCallMiddlewares, compose.ToolMiddleware{Invokable: buildDangerousToolMiddleware(map[string]bool{"knowledge_search": true})})
			}
			if mode == "gate" {
				cfg.ToolCallMiddlewares = append(cfg.ToolCallMiddlewares, compose.ToolMiddleware{Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
					return func(ctx context.Context, in *compose.ToolInput) (*compose.ToolOutput, error) {
						if _, ok := ctx.Deadline(); ok {
							t.Error("审批外层不应已开启工具deadline")
						}
						time.Sleep(60 * time.Millisecond)
						return next(ctx, in)
					}
				}})
			}
			node, err := compose.NewToolNode(context.Background(), ptrToolsConfig(e.withToolExecutionTimeout(cfg)))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			if mode == "parent" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 10*time.Millisecond)
				defer stop()
			}
			_, err = node.Invoke(ctx, &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "call", Type: "function", Function: schema.FunctionCall{Name: slow.name, Arguments: `{}`}}}})
			if err == nil {
				t.Fatal("执行应中断或超时")
			}
			if mode == "approval" || mode == "canceled" {
				if slow.calls.Load() != 0 {
					t.Fatal("未放行或取消后仍执行工具")
				}
				return
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if slow.calls.Load() != 1 {
				t.Fatal("工具超时不应自动重试")
			}
		})
	}
}
func ptrToolsConfig(cfg compose.ToolsNodeConfig) *compose.ToolsNodeConfig { return &cfg }

func TestToolTimeoutExcludesClarifyAndMCP(t *testing.T) {
	e := &Engine{toolTimeout: 35 * time.Millisecond}
	called := 0
	endpoint := e.toolTimeoutMiddleware()(func(ctx context.Context, _ *compose.ToolInput) (*compose.ToolOutput, error) {
		if _, ok := ctx.Deadline(); ok {
			t.Error("澄清与MCP不应套用内置工具时限")
		}
		called++
		return &compose.ToolOutput{Result: "ok"}, nil
	})
	for _, name := range []string{clarifyToolName, "mcp__server__search"} {
		if _, err := endpoint(context.Background(), &compose.ToolInput{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	if called != 2 {
		t.Fatal("澄清与MCP不应套用新策略")
	}
}

type timeoutDocumentRepo struct{ repository.DocumentRepository }

func (timeoutDocumentRepo) FindByID(ctx context.Context, _, _ string, _ int) (entity.Document, bool, error) {
	<-ctx.Done()
	return entity.Document{}, false, ctx.Err()
}

func TestDocumentToolTimeoutPropagatesThroughStreamingNode(t *testing.T) {
	// 真实文档工具会把仓库错误转成Success:false的JSON；超时不能因此变成成功输出。
	e := &Engine{toolTimeout: 35 * time.Millisecond, internalTools: []internalToolRegistryEntry{{Name: "get_document_info", Domain: DomainKnowledge}}}
	documentTool := tool.NewGetDocumentInfoTool(timeoutDocumentRepo{})("user", []string{"kb"})
	cfg := buildToolsNodeConfig()
	cfg.Tools = []einotool.BaseTool{documentTool}
	node, err := compose.NewToolNode(context.Background(), ptrToolsConfig(e.withToolExecutionTimeout(cfg)))
	if err != nil {
		t.Fatal(err)
	}
	sr, err := node.Stream(context.Background(), &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "doc", Type: "function", Function: schema.FunctionCall{Name: "get_document_info", Arguments: `{"document_id":"doc"}`}}}})
	if err == nil {
		defer sr.Close()
		_, err = sr.Recv()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("流式节点吞掉文档超时: %v", err)
	}
}
