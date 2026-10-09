package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// ─── 快速模式 Profile 接线的形状守卫 ───────────────────────────────
//
// 背景：快速模式从「固定 Graph」改成 eino ChatModelAgent 后，模式选择变成一条
// **跨三层的字符串链路**：modeRegistry → processAgentMode 的 profile 形参 →
// agent.Request.Profile → agent 层分派。中间任何一层漏传，用户就会在选「快速」
// 的情况下静默吃到深度模式的全量工具（20+ 个 MCP + delete_document）。
//
// 这类缺陷没有任何单测能自然覆盖：processAgentMode 需要真实 model 与 DB，
// 端到端跑一次的成本高到不会进 CI。于是这里用形状守卫把链路钉死：
// **每一层都必须出现，且 Profile 字面量只能来自 agent 包的常量。**

const (
	fmodeRegistryFile = "chat_mode_registry.go"
	fmodeProcessFile  = "chat_service_mode.go"
)

// modeHandlerProfile 解析 modeRegistry 每个 handler 实际传给 processAgentMode 的
// profile 字面量。返回 搜索模式 → Profile 常量名。
func modeHandlerProfile(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, fmodeRegistryFile, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", fmodeRegistryFile, err)
	}

	out := map[string]string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		hdr := receiverType(fn)
		if hdr != "quickModeHandler" && hdr != "deepModeHandler" {
			continue
		}
		for _, stmt := range fn.Body.List {
			es, ok := stmt.(*ast.ExprStmt)
			if !ok {
				continue
			}
			call, ok := es.X.(*ast.CallExpr)
			if !ok {
				continue
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "processAgentMode" {
				continue
			}
			// processAgentMode 的最后一个形参是 eventCh，Profile 在**倒数第二**。
			// 这里按位置取而不是靠类型猜：位置固定，守卫才不会跟着签名漂移。
			const profileArgIdx = 5 // ctx,s,userID,sessionID,userMsgID,req,profile,eventCh
			if len(call.Args) <= profileArgIdx {
				t.Fatalf("%s 的 %s.Handle 调 processAgentMode 只有 %d 个实参，签名变了？",
					fmodeRegistryFile, hdr, len(call.Args))
			}
			last := call.Args[profileArgIdx]
			switch v := last.(type) {
			case *ast.SelectorExpr:
				if !strings.HasPrefix(selExprPkg(v), "agent") {
					t.Fatalf("%s 的 %s.Handle 传的是 %s，Profile 必须来自 agent 包的常量",
						fmodeRegistryFile, hdr, v.Sel.Name)
				}
				out[hdr] = v.Sel.Name
			case *ast.Ident:
				// 变量间接传递：不在守卫覆盖范围（当前代码里不存在这种写法）
				t.Fatalf("%s 的 %s.Handle 把 Profile 藏在变量 %s 里，"+
					"守卫追不到；请直接传 agent.ProfileXxx 常量", fmodeRegistryFile, hdr, v.Name)
			}
		}
	}
	return out
}

func receiverType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	t := fn.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func selExprPkg(sel *ast.SelectorExpr) string {
	if id, ok := sel.X.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// TestModeHandlersPassDistinctProfiles断言两个 handler 传的是**不同** Profile，
// 且恰好覆盖 agent 包的 quick/deep 两个常量。
//
// 为什么这条不能弱化成「都传了某个 Profile」：两个 handler 都传 ProfileDeep
// 时快速模式会静默拿到 delete_document 与全部 MCP 工具，而所有单测照样全绿。
func TestModeHandlersPassDistinctProfiles(t *testing.T) {
	got := modeHandlerProfile(t)
	if len(got) != 2 {
		t.Fatalf("从 %s 只解析出 %d 个 handler 的 Profile（%v），期望 2 个；"+
			"若 handler 改成了变量间接传递，守卫会在这里停下", fmodeRegistryFile, len(got), got)
	}
	if got["quickModeHandler"] != "ProfileQuick" {
		t.Errorf("quickModeHandler 传的是 %q，期望 ProfileQuick", got["quickModeHandler"])
	}
	if got["deepModeHandler"] != "ProfileDeep" {
		t.Errorf("deepModeHandler 传的是 %q，期望 ProfileDeep", got["deepModeHandler"])
	}
}

// TestRegistryQuickFallbackPointsToQuickProfile 断言未知 searchMode 回退到的
// 「快速」handler 确实挂快速 Profile。
//
// 回退链一旦指错，用户传错 mode 值时会静默进入深度模式（更贵、更慢、工具更多），
// 而且界面上看不出来 —— 请求是成功返回的。
func TestRegistryQuickFallbackPointsToQuickProfile(t *testing.T) {
	fallback := getModeHandler("完全不存在的模式")
	if fallback.Name() != chatModeQuick {
		t.Fatalf("未知模式回退到 %q，期望 %q", fallback.Name(), chatModeQuick)
	}
	if _, ok := fallback.(*quickModeHandler); !ok {
		t.Fatalf("回退处理器类型=%T，期望 *quickModeHandler", fallback)
	}
	got := modeHandlerProfile(t)
	if got["quickModeHandler"] != "ProfileQuick" {
		t.Fatalf("回退目标挂的是 %q，期望 ProfileQuick", got["quickModeHandler"])
	}
}

// TestDefaultSearchModeIsQuick 断言空 searchMode 也走快速。
// 前端不传 mode 是常态，这条一旦漂移，所有老客户端都会切到深度模式。
func TestDefaultSearchModeIsQuick(t *testing.T) {
	h := getModeHandler("")
	if h.Name() != chatModeQuick {
		t.Fatalf("空模式回退到 %q，期望 %q", h.Name(), chatModeQuick)
	}
}

// TestProcessAgentModePropagatesProfileToAgentRequest 守链路第二段：
// processAgentMode 必须把形参 profile 原样写进 agentReq.Profile。
//
// 退化形态：形参收了 profile 却只用于 token 预估，agentReq.Profile 留空 ——
// agent 层 effectiveProfile 会把空值当深度模式，于是快速模式静默变深度。
func TestProcessAgentModePropagatesProfileToAgentRequest(t *testing.T) {
	src, err := os.ReadFile(fmodeProcessFile)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", fmodeProcessFile, err)
	}
	body := string(src)

	if !strings.Contains(body, "func (s *chatService) processAgentMode(") {
		t.Fatalf("%s 里没有 processAgentMode", fmodeProcessFile)
	}
	// 形参必须在签名里（不是从别处推导）
	sigLine := lineContaining(body, "func (s *chatService) processAgentMode(")
	if !strings.Contains(sigLine, "profile agent.Profile") {
		t.Fatalf("processAgentMode 签名缺 profile 形参，实际：%s", strings.TrimSpace(sigLine))
	}
	// 必须写进 agentReq.Profile
	if !strings.Contains(body, "agentReq.Profile = profile") {
		t.Fatalf("%s 里 agentReq.Profile 没被赋值 ⇒ profile 到不了 agent 层", fmodeProcessFile)
	}
	// 写错字段名（比如写成 Profile: profile 到别的结构体）也要能发现：
	// 赋值目标必须是 agentReq。
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, ".Profile = profile") &&
			!strings.Contains(line, "agentReq.Profile = profile") {
			t.Fatalf("Profile 被写到了非 agentReq 的目标上：%s", strings.TrimSpace(line))
		}
	}
}

// TestQuickProfileSkipsExecutionPlanGuard 断言「没调工具就只给执行计划」的守卫
// 只对深度模式生效。
//
// 快速模式允许零工具直接回答（寒暄/常识），若沿用深度模式的判据，
// 用户问「你好」会收到一个 error 事件。
func TestQuickProfileSkipsExecutionPlanGuard(t *testing.T) {
	src, err := os.ReadFile(fmodeProcessFile)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", fmodeProcessFile, err)
	}
	body := string(src)

	guardLine := lineContaining(body, "looksLikeExecutionPlan(fullContent)")
	if guardLine == "" {
		t.Fatalf("%s 里找不到执行计划守卫", fmodeProcessFile)
	}
	if !strings.Contains(guardLine, "profile == agent.ProfileDeep") {
		t.Fatalf("执行计划守卫没有按 profile 区分（快速模式会被误伤）：%s",
			strings.TrimSpace(guardLine))
	}
}

// TestPromptBuilderHasNoQuickModeEnum 断言 PromptMode 枚举已彻底移除。
//
// 形态守卫而非行为守卫：这个枚举的唯一作用是「快速模式用另一套非 ReAct 的
// base prompt」。留着它就意味着存在第二套 prompt 来源，而它已经不再被任何
// 生产代码使用 —— 这正是「有两个来源 = 缺陷温床」。
func TestPromptBuilderHasNoQuickModeEnum(t *testing.T) {
	f, err := os.ReadFile("chat_prompt_builder.go")
	if err != nil {
		t.Fatalf("读取 chat_prompt_builder.go 失败: %v", err)
	}
	body := string(f)
	for _, banned := range []string{"PromptModeQuick", "PromptModeDeep", "quickModeAgentSystemPrompt", "type PromptMode"} {
		if strings.Contains(body, banned) {
			t.Errorf("chat_prompt_builder.go 里仍有 %q：旧的快速模式 prompt 入口未清干净", banned)
		}
	}
	// NewPromptBuilder 不该再收mode 形参
	sig := lineContaining(body, "func NewPromptBuilder(")
	if strings.Contains(sig, "PromptMode") {
		t.Fatalf("NewPromptBuilder 仍收 PromptMode 形参：%s", strings.TrimSpace(sig))
	}
}

// TestServiceLayerHasNoQuickGraphArtifacts 断言旧快速 Graph 的痕迹全部消失。
//
// 用文件存在性 + 符号扫描，而不是逐个 grep：新增一个叫 quickGraphXxx 的
// 节点函数时，这条会直接红。
func TestServiceLayerHasNoQuickGraphArtifacts(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取 service 目录失败: %v", err)
	}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "chat_service_graph_quick") || n == "retrieval_query.go" {
			t.Errorf("旧快速 Graph 的文件仍在：%s", n)
		}
	}

	// chatService 结构体不该再有 quickGraph / einoRetriever / retriever 字段
	src, err := os.ReadFile("chat_service.go")
	if err != nil {
		t.Fatalf("读取 chat_service.go 失败: %v", err)
	}
	for _, banned := range []string{"quickGraph", "einoRetriever", "compileQuickGraph"} {
		if strings.Contains(string(src), banned) {
			t.Errorf("chat_service.go 里仍有 %q", banned)
		}
	}
}

// TestBuildGraphNoLongerCompilesQuickGraphInCtor 断言构造函数不再编译 Graph。
// 编译失败曾是「启动期错误」，删掉 Graph 后这条能力自然消失；
// 若有人又把编译塞回构造期，这条会提醒他快速模式已经不需要 Graph 了。
func TestBuildGraphNoLongerCompilesQuickGraphInCtor(t *testing.T) {
	src, err := os.ReadFile("chat_service.go")
	if err != nil {
		t.Fatalf("读取 chat_service.go 失败: %v", err)
	}
	if strings.Contains(string(src), ".Compile(") || strings.Contains(string(src), "compileQuickGraph") {
		t.Fatal("chat_service.go 里仍有 Graph 编译调用：快速模式已改为 eino Agent，不该再构造 Graph")
	}
	// 构造函数也不该再有失败路径。
	// ⚠️ 截取必须按「大括号计数」而不是找下一个 "\n}\n"：
	// 构造函数结尾的 "}\n" 后面紧跟注释，下一个 "\n}\n" 可能隔了上百行，
	// 那样会把整个文件后半段（含 ListSessions 等方法的 return nil, err）框进来，
	// 守卫变成恒红。
	ctor := funcBody(string(src), "func NewChatService(")
	if ctor == "" {
		t.Fatal("chat_service.go 里找不到 NewChatService")
	}
	if strings.Contains(ctor, "return nil,") || strings.Contains(ctor, ", error)") {
		t.Errorf("NewChatService 仍返回 error / 有失败路径：\n%s", strings.TrimSpace(ctor))
	}
}

// funcBody 从 func 声明处开始，按大括号计数取出整个函数体（含签名）。
func funcBody(src, header string) string {
	i := strings.Index(src, header)
	if i < 0 {
		return ""
	}
	depth, started := 0, false
	for j := i; j < len(src); j++ {
		switch src[j] {
		case '{':
			depth++
			started = true
		case '}':
			depth--
			if started && depth == 0 {
				return src[i : j+1]
			}
		}
	}
	return ""
}

func lineContaining(body, sub string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, sub) {
			return line
		}
	}
	return ""
}
