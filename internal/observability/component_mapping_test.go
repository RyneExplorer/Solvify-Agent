package observability

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/compose"
)

// einoComponentConstants 是 eino v0.9.1 全部组件字符串常量的清单。
//
// 来源三个包：components(10) + compose(8) + adk(2) = 20 个。
// ⚠️ eino 升级后要同步维护这份清单 —— 它是「compMap 漏映射」的唯一外部真值来源。
func einoComponentConstants() []string {
	return []string{
		// components 包（components/types.go）
		string(components.ComponentOfPrompt),
		string(components.ComponentOfAgenticPrompt),
		string(components.ComponentOfChatModel),
		string(components.ComponentOfAgenticModel),
		string(components.ComponentOfEmbedding),
		string(components.ComponentOfIndexer),
		string(components.ComponentOfRetriever),
		string(components.ComponentOfLoader),
		string(components.ComponentOfTransformer),
		string(components.ComponentOfTool),
		// compose 包（compose/types.go）
		string(compose.ComponentOfUnknown),
		string(compose.ComponentOfGraph),
		string(compose.ComponentOfWorkflow),
		string(compose.ComponentOfChain),
		string(compose.ComponentOfPassthrough),
		string(compose.ComponentOfToolsNode),
		string(compose.ComponentOfAgenticToolsNode),
		string(compose.ComponentOfLambda),
		// adk 包（adk/interface.go）
		string(adk.ComponentOfAgent),
		string(adk.ComponentOfAgenticAgent),
	}
}

const wantEinoComponentCount = 20

// TestCompMapCoversAllEinoComponentConstants 锁定「20 个常量一个都不能漏」。
//
// 回归背景：compMap 曾用手写字符串字面量，只覆盖 10 个；漏掉的 ToolsNode /
// Lambda 直接落到兜底分支，被贴上有业务含义的 agent.engine。
// 漏任何一个，这里就红一条。
func TestCompMapCoversAllEinoComponentConstants(t *testing.T) {
	consts := einoComponentConstants()
	if len(consts) != wantEinoComponentCount {
		t.Fatalf("常量清单数量变了：期望 %d，实际 %d —— eino 升级后请同步更新本清单",
			wantEinoComponentCount, len(consts))
	}
	for _, c := range consts {
		m, ok := compMap[c]
		if !ok {
			t.Errorf("组件 %q 没有映射，会走兜底分支", c)
			continue
		}
		// compose.ComponentOfUnknown 的字面值就是 "Unknown" —— eino 自己声明
		// 「我不知道这是什么」，映到 ComponentUnknown 正是本意，不算漏映射。
		if c != string(compose.ComponentOfUnknown) && m.component == ComponentUnknown {
			t.Errorf("组件 %q 被映射成 unknown，等于没映射", c)
		}
		if m.label == "" {
			t.Errorf("组件 %q 的 label 是空的", c)
		}
		if got := mapComponent(c); got != m.component {
			t.Errorf("mapComponent(%q) = %q，与 compMap 里的 %q 不一致", c, got, m.component)
		}
	}
}

// TestMapComponentUnknownFallbackIsNeutral 锁定「认不出来时返回中性值」。
//
// 回归背景：兜底曾返回 ComponentAgentEngine，而该标签会被前端原样渲染成
// 「组件：agent.engine」—— 等于把「我不认识」伪装成「这是 Agent」。
// 实测污染了 24 个 span 里的 10 个（41.7%）。
func TestMapComponentUnknownFallbackIsNeutral(t *testing.T) {
	for _, c := range []string{"NoSuchComponentXyz", "SomeFutureNode", "Typhoon"} {
		got := mapComponent(c)
		if got == ComponentAgentEngine {
			t.Errorf("未知组件 %q 兜底成了 agent.engine（有业务含义，会被渲染成「这是 Agent」）", c)
		}
		if got != ComponentUnknown {
			t.Errorf("未知组件 %q 兜底成了 %q，期望 %q", c, got, ComponentUnknown)
		}
	}
}

// TestMapComponentEmptyStringIsNeutral 锁定空组件字符串也不冒充 Agent。
func TestMapComponentEmptyStringIsNeutral(t *testing.T) {
	if got := mapComponent(""); got != ComponentUnknown {
		t.Errorf("mapComponent(\"\") = %q，期望 %q", got, ComponentUnknown)
	}
}

// TestGenAIOperationNameOrchestrationNodes 锁定编排节点的 gen_ai.operation.name。
//
// 回归背景：switch 只覆盖了 Graph/Chain/Workflow 三个字面量，于是 Lambda /
// Passthrough / ToolsNode 这些实测已踩中的节点连 op.name 都拿不到。
func TestGenAIOperationNameOrchestrationNodes(t *testing.T) {
	cases := map[string]string{
		// 编排类 → invoke_workflow
		string(compose.ComponentOfGraph):       genAIOpInvokeFlow,
		string(compose.ComponentOfWorkflow):    genAIOpInvokeFlow,
		string(compose.ComponentOfChain):       genAIOpInvokeFlow,
		string(compose.ComponentOfLambda):      genAIOpInvokeFlow,
		string(compose.ComponentOfPassthrough): genAIOpInvokeFlow,
		// 工具执行节点 → execute_tool
		string(compose.ComponentOfToolsNode):        genAIOpExecuteTool,
		string(compose.ComponentOfAgenticToolsNode): genAIOpExecuteTool,
		// Agent
		string(adk.ComponentOfAgent):        genAIOpInvokeAgent,
		string(adk.ComponentOfAgenticAgent): genAIOpInvokeAgent,
		// 其他既有映射
		string(components.ComponentOfChatModel):    genAIOpChat,
		string(components.ComponentOfAgenticModel): genAIOpChat,
		string(components.ComponentOfTool):         genAIOpExecuteTool,
		string(components.ComponentOfRetriever):    genAIOpRetrieval,
		string(components.ComponentOfEmbedding):    genAIOpEmbeddings,
		// 刻意留空：gen_ai 语义规范里没有对应操作名，硬套一个是错标
		string(components.ComponentOfPrompt): "",
		string(compose.ComponentOfUnknown):   "",
	}
	for comp, want := range cases {
		if got := genAIOperationName(components.Component(comp)); got != want {
			t.Errorf("genAIOperationName(%q) = %q，期望 %q", comp, got, want)
		}
	}
}

// TestOnStartAlwaysRecordsRawEinoComponent 锁定「原始组件字符串必须落盘」。
//
// 为什么要它：mapComponent 会把多个 eino 组件折叠成同一个业务 component
// （Graph / Chain / Lambda / Workflow 都进 agent.engine），一旦不落原始的
// eino_comp，就再也无法反查这个 span 究竟是哪一类节点。
func TestOnStartAlwaysRecordsRawEinoComponent(t *testing.T) {
	for _, comp := range []components.Component{
		compose.ComponentOfChain,
		compose.ComponentOfLambda,
		compose.ComponentOfToolsNode,
		adk.ComponentOfAgent,
	} {
		rec := newTestRecorder(t)
		ctx := rec.WithTraceRoot(context.Background(), TraceRootAttrs{UserID: "u1", SessionID: "s1"})
		ctx = NewEinoCallbackHandler(rec).OnStart(ctx, &callbacks.RunInfo{
			Name:      "SolvifyDeepAgent",
			Component: comp,
		}, nil)

		state, ok := ctx.Value(einoSpanKey{}).(*einoSpanState)
		if !ok || state == nil || state.span == nil {
			t.Fatalf("组件 %q：OnStart 之后 ctx 里没有 span", comp)
		}
		got, _ := state.span.Attrs["eino_comp"].(string)
		if got != string(comp) {
			t.Errorf("eino_comp = %q，期望 %q —— 折叠成业务 component 后没有它就无法反查节点真实类型",
				got, string(comp))
		}
	}
}
