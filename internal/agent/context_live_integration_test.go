package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/jackc/pgx/v5"
	"solvify-agent/internal/llm"
	"solvify-agent/pkg/config"
)

// 显式启用；只读取模型配置、发送合成样本，不注册外部副作用工具。
func TestContextLiveDeepSeek(t *testing.T) {
	if os.Getenv("SOLVIFY_CONTEXT_LIVE_TEST") != "1" {
		t.Skip("设置 SOLVIFY_CONTEXT_LIVE_TEST=1 执行 DeepSeek 合成样本联调")
	}
	cfg, err := config.Load("../../configs/config.yaml")
	if err != nil {
		t.Fatal("配置加载失败")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	db := cfg.Database.Postgres
	dsn := url.URL{Scheme: "postgres", User: url.UserPassword(db.Username, db.Password), Host: net.JoinHostPort(db.Host, strconv.Itoa(db.Port)), Path: db.Database}
	conn, err := pgx.Connect(ctx, dsn.String())
	if err != nil {
		t.Fatal("数据库连接失败")
	}
	defer conn.Close(ctx)
	var name, base, key string
	var window int
	// 精确限定 DeepSeek，系统配置优先，无目标模型时不回退到其他厂商。
	err = conn.QueryRow(ctx, `SELECT model_id,base_url,api_key,max_context_length FROM (
		SELECT model_id,base_url,api_key,max_context_length,0 AS priority FROM models WHERE is_enabled=true AND model_id ILIKE '%deepseek%'
		UNION ALL SELECT model_id,base_url,api_key,max_context_length,1 AS priority FROM user_model_configs WHERE model_id ILIKE '%deepseek%'
	) AS candidates ORDER BY CASE WHEN model_id='deepseek-v4-pro' THEN 0 WHEN model_id='deepseek-chat' THEN 1 WHEN model_id='deepseek-flash' THEN 2 ELSE 3 END,priority,model_id LIMIT 1`).Scan(&name, &base, &key, &window)
	if err != nil {
		t.Fatal("未找到可用 DeepSeek 模型配置")
	}
	window = min(window, 8192)
	client, err := llm.NewOpenAIClient(ctx, llm.OpenAIClientConfig{APIKey: key, BaseURL: base, Model: name, MaxContextLength: window, Config: []byte(`{"max_input_tokens":8192,"max_output_tokens":8192,"max_completion_tokens":4096,"output_token_field":"max_completion_tokens","tool_calling":true,"reasoning_capability":"none","reasoning_mode":"default"}`)})
	if err != nil {
		t.Fatal("DeepSeek 客户端初始化失败")
	}
	t.Logf("模型=%s 窗口=%d 输出=%d", name, window, client.OutputLimit())
	for _, scenario := range []string{"quick_short", "quick_history", "quick_tools", "deep_tools"} {
		t.Run(scenario, func(t *testing.T) {
			profile := ProfileQuick
			if scenario == "deep_tools" {
				profile = ProfileDeep
			}
			req := Request{Profile: profile, ModelID: "synthetic-live", ModelName: name, MaxContextLength: window, OutputLimit: client.OutputLimit(), UserID: "synthetic", SessionID: scenario, RunID: scenario}
			messages := quickSummaryHistory()
			if scenario == "quick_short" {
				messages[0] = schema.UserMessage("项目代号海星，预算4096，来源doc-ctx/chunk-1。")
			} else if strings.HasSuffix(scenario, "tools") {
				messages = []*schema.Message{schema.UserMessage("根据检索证据回答：项目代号、预算和证据是什么？引用doc-ctx/chunk-1。")}
				messages = append(messages, testToolGroup("old", "项目代号海星，预算4096，来源doc-ctx/chunk-1。\n"+strings.Repeat("record: alpha=7;\n", 500))...)
				messages = append(messages, testToolGroup("latest", "补充证据：预算单位为token。")...)
			}
			e := NewEngine(nil, cfg.Agent).(*Engine)
			t.Logf("摘要前=%d 摘要输入上限=%d", llm.MessagesTokens(messages, name), e.runtimePolicy().SummaryInput)
			summaryCalls := 0
			// 包装真实客户端记录摘要调用，所有实际调用仍经过生产预算保护。
			cm := &deepTestModel{respond: func(callCtx context.Context, input []*schema.Message) (*schema.Message, error) {
				summaryCalls++
				response, err := client.ChatModel().Generate(callCtx, input, model.WithTools([]*schema.ToolInfo{}), model.WithMaxTokens(e.runtimePolicy().SummaryOutput))
				if err != nil {
					t.Logf("摘要错误类别=%s", liveContextErrorCategory(err))
					return nil, errors.New("真实摘要模型调用失败：" + liveContextErrorCategory(err))
				}
				return response, nil
			}}
			started := time.Now()
			runContextTestInContext(t, ctx, nil, func(sessionCtx context.Context) error {
				m := e.runtimeHandler(req, "依据合成证据回答，保留代号、预算和来源标识。", cm)
				_, next, err := m.BeforeModelRewriteState(sessionCtx, &adk.ChatModelAgentState{Messages: messages}, nil)
				if err != nil {
					return err
				}
				want := 1
				if scenario == "quick_short" {
					want = 0
				}
				if summaryCalls != want || (want == 1 && llm.MessagesTokens(next.Messages, name) >= llm.MessagesTokens(messages, name)) {
					t.Logf("摘要调用=%d 压缩后=%d", summaryCalls, llm.MessagesTokens(next.Messages, name))
					return errors.New("摘要触发或压缩效果不符合预期")
				}
				input := append([]*schema.Message{schema.SystemMessage("只根据提供的数据回答代号、预算和证据；引用doc-ctx/chunk-1。")}, next.Messages...)
				guarded := &budgetModel{inner: client.ChatModel(), guard: newBudgetGuard(req, "")}
				response, err := guarded.Generate(ctx, input, model.WithTools([]*schema.ToolInfo{}), model.WithMaxTokens(512))
				if err != nil {
					return errors.New("真实回答模型调用失败：" + liveContextErrorCategory(err))
				}
				answer := response.Content
				facts := strings.Contains(answer, "海星") && strings.Contains(answer, "4096") && strings.Contains(answer, "doc-ctx/chunk-1")
				report := map[string]any{"model": name, "scenario": scenario, "window": window, "before_tokens": llm.MessagesTokens(messages, name), "after_tokens": llm.MessagesTokens(next.Messages, name), "summary_calls": summaryCalls, "facts_and_citation": facts, "elapsed_ms": time.Since(started).Milliseconds()}
				if response.ResponseMeta != nil {
					report["answer_usage"] = response.ResponseMeta.Usage
				}
				raw, _ := json.Marshal(report)
				t.Log(string(raw))
				if !facts {
					return errors.New("真实回答未保留固定样本事实或引用")
				}
				return nil
			})
		})
	}
}

func liveContextErrorCategory(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(err, llm.ErrContextBudget) {
		return "local_context_budget"
	}
	if errors.Is(err, llm.ErrContextConfig) {
		return "local_context_config"
	}
	if errors.Is(err, io.EOF) {
		return "eof"
	}
	var networkError *url.Error
	if errors.As(err, &networkError) {
		return fmt.Sprintf("network_%T", networkError.Err)
	}
	if status := regexp.MustCompile(`\b(?:400|401|403|404|429|500|502|503|504)\b`).FindString(err.Error()); status != "" {
		return "http_" + status
	}
	for _, category := range []string{"connection refused", "timeout", "no such host"} {
		if strings.Contains(strings.ToLower(err.Error()), category) {
			return category
		}
	}
	return fmt.Sprintf("unclassified_%T", err)
}
