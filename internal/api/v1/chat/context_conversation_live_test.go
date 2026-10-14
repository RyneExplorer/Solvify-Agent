package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"gorm.io/datatypes"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"solvify-agent/internal/agent"
	"solvify-agent/internal/llm"
	"solvify-agent/internal/middleware"
	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/cache"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/database"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/jwt"
)

type conversationRoundKey struct{}

type conversationMessage struct {
	ID      string `json:"id,omitempty"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

type conversationModelCall struct {
	Kind        string                `json:"kind"`
	Messages    []conversationMessage `json:"messages"`
	InputTokens int                   `json:"input_tokens"`
	Output      string                `json:"output,omitempty"`
	Error       string                `json:"error,omitempty"`
	ElapsedMS   int64                 `json:"elapsed_ms"`
}

type conversationRound struct {
	Number        int                     `json:"number"`
	Question      string                  `json:"question"`
	Before        []conversationMessage   `json:"before"`
	BeforeTokens  int                     `json:"before_tokens"`
	HistoryBudget int                     `json:"history_budget"`
	After         []conversationMessage   `json:"after"`
	Recall        []conversationMessage   `json:"recall"`
	AfterTokens   int                     `json:"after_tokens"`
	AgentHistory  []conversationMessage   `json:"agent_history"`
	SummaryBefore string                  `json:"summary_before"`
	SummaryAfter  string                  `json:"summary_after"`
	CoveredBefore int                     `json:"covered_before"`
	CoveredAfter  int                     `json:"covered_after"`
	Memories      []string                `json:"memories"`
	Calls         []conversationModelCall `json:"calls"`
	Events        []dto.StreamEvent       `json:"events"`
	Answer        string                  `json:"answer"`
	Done          int                     `json:"done"`
	Errors        int                     `json:"errors"`
	Persisted     int                     `json:"persisted"`
	Checks        map[string]bool         `json:"checks"`
	AgentErrors   []string                `json:"agent_errors,omitempty"`
	InputCode     int                     `json:"input_code,omitempty"`
	InputError    string                  `json:"input_error,omitempty"`
	ElapsedMS     int64                   `json:"elapsed_ms"`
	mutex         sync.Mutex
	refreshed     chan struct{}
}

type conversationReport struct {
	Model       string               `json:"model"`
	SessionID   string               `json:"session_id"`
	Window      int                  `json:"window"`
	OutputLimit int                  `json:"output_limit"`
	Hard        int                  `json:"hard"`
	Soft        int                  `json:"soft"`
	Started     string               `json:"started"`
	Rounds      []*conversationRound `json:"rounds"`
	Failures    []string             `json:"failures"`
}

func conversationEntityMessages(messages []entity.ChatMessage) []conversationMessage {
	result := make([]conversationMessage, 0, len(messages))
	for _, message := range messages {
		result = append(result, conversationMessage{message.ID, message.Role, message.Content})
	}
	return result
}

func conversationSchemaMessages(messages []*schema.Message) []conversationMessage {
	result := make([]conversationMessage, 0, len(messages))
	for _, message := range messages {
		if message != nil {
			result = append(result, conversationMessage{Role: string(message.Role), Content: message.Content})
		}
	}
	return result
}

func conversationTokens(messages []conversationMessage, name string) int {
	input := make([]*schema.Message, 0, len(messages))
	for _, message := range messages {
		input = append(input, &schema.Message{Role: schema.RoleType(message.Role), Content: message.Content})
	}
	return llm.MessagesTokens(input, name)
}

func conversationError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, llm.ErrContextBudget) {
		return "context_budget"
	}
	return "model_or_io_error"
}

type conversationMeasuredModel struct {
	inner model.ToolCallingChatModel
	round *conversationRound
	name  string
	kind  string
	tools []*schema.ToolInfo
}

func (measured *conversationMeasuredModel) OutputLimit() int {
	return measured.inner.(interface{ OutputLimit() int }).OutputLimit()
}
func (measured *conversationMeasuredModel) InputLimit() int {
	return measured.inner.(interface{ InputLimit() int }).InputLimit()
}
func (measured *conversationMeasuredModel) ContextWindow() int {
	return measured.inner.(interface{ ContextWindow() int }).ContextWindow()
}
func (measured *conversationMeasuredModel) FinalBudgetChecked() bool { return true }
func (measured *conversationMeasuredModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := measured.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	next := *measured
	next.inner = inner
	next.tools = append([]*schema.ToolInfo(nil), tools...)
	return &next, nil
}

func (measured *conversationMeasuredModel) record(messages []*schema.Message, output string, err error, started time.Time, kind string) {
	call := conversationModelCall{Kind: kind, Messages: conversationSchemaMessages(messages), InputTokens: llm.MessagesTokens(messages, measured.name) + llm.ToolsTokens(measured.tools, measured.name), Output: output, Error: conversationError(err), ElapsedMS: time.Since(started).Milliseconds()}
	measured.round.mutex.Lock()
	measured.round.Calls = append(measured.round.Calls, call)
	measured.round.mutex.Unlock()
}

func (measured *conversationMeasuredModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	started := time.Now()
	response, err := measured.inner.Generate(ctx, messages, opts...)
	output := ""
	if response != nil {
		output = response.Content
	}
	kind := measured.kind
	if len(messages) > 0 && strings.HasPrefix(messages[0].Content, "总结已完成交互") {
		kind = "runtime_summary"
	}
	measured.record(messages, output, err, started, kind)
	return response, err
}

func (measured *conversationMeasuredModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	started := time.Now()
	stream, err := measured.inner.Stream(ctx, messages, opts...)
	measured.record(messages, "", err, started, "answer_input")
	return stream, err
}

type conversationMeasuredEngine struct {
	agent.AgentInterface
	name   string
	redact []string
}

func (measured conversationMeasuredEngine) Execute(ctx context.Context, req agent.Request, cm model.ToolCallingChatModel) (<-chan agent.Event, error) {
	round := ctx.Value(conversationRoundKey{}).(*conversationRound)
	round.AgentHistory = conversationEntityMessages(req.History)
	events, err := measured.AgentInterface.Execute(ctx, req, &conversationMeasuredModel{inner: cm, round: round, name: measured.name, kind: "answer_input"})
	if err != nil {
		return nil, err
	}
	forwarded := make(chan agent.Event, 100)
	go func() {
		defer close(forwarded)
		for event := range events {
			if event.Type == agent.EventError {
				detail := event.Error
				for _, value := range measured.redact {
					if value != "" {
						detail = strings.ReplaceAll(detail, value, "[已隐藏]")
					}
				}
				round.AgentErrors = append(round.AgentErrors, string(event.Kind)+": "+detail)
			}
			select {
			case forwarded <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return forwarded, nil
}

type conversationMeasuredContext struct {
	service.ContextServiceInterface
	messages repository.ChatMessageRepo
	name     string
}

func (measured conversationMeasuredContext) BuildContext(ctx context.Context, userID, sessionID, query string, cfg service.BuildContextConfig, cm model.BaseChatModel) (*service.EnhancedContext, error) {
	round := ctx.Value(conversationRoundKey{}).(*conversationRound)
	all, err := measured.messages.FindBySessionIDForContext(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	for _, message := range all {
		if message.ID != cfg.CurrentMessageID {
			round.Before = append(round.Before, conversationMessage{message.ID, message.Role, message.Content})
		}
	}
	round.BeforeTokens = conversationTokens(round.Before, measured.name)
	round.HistoryBudget = cfg.MaxTokens
	after, err := measured.ContextServiceInterface.BuildContext(ctx, userID, sessionID, query, cfg, cm)
	if err == nil && after != nil {
		round.After = conversationEntityMessages(after.History)
		round.Recall = conversationEntityMessages(after.RecallHistory)
		round.AfterTokens = conversationTokens(round.After, measured.name) + conversationTokens(round.Recall, measured.name)
		if after.Summary != nil {
			round.SummaryBefore = after.Summary.Summary
			round.CoveredBefore = after.Summary.CoveredCount
		}
		for _, memory := range after.Memories {
			round.Memories = append(round.Memories, memory.Content)
		}
	}
	return after, err
}

func (measured conversationMeasuredContext) SummarizeSession(ctx context.Context, sessionID string, cm model.BaseChatModel) (*entity.ChatSummary, error) {
	round := ctx.Value(conversationRoundKey{}).(*conversationRound)
	return measured.ContextServiceInterface.SummarizeSession(ctx, sessionID, &conversationMeasuredModel{inner: cm.(model.ToolCallingChatModel), round: round, name: measured.name, kind: "session_summary"})
}

func (measured conversationMeasuredContext) ExtractMemories(ctx context.Context, userID, sessionID string, messages []entity.ChatMessage, cm model.BaseChatModel) ([]entity.UserMemory, error) {
	round := ctx.Value(conversationRoundKey{}).(*conversationRound)
	memories, err := measured.ContextServiceInterface.ExtractMemories(ctx, userID, sessionID, messages, &conversationMeasuredModel{inner: cm.(model.ToolCallingChatModel), round: round, name: measured.name, kind: "memory_extraction"})
	if err == nil {
		select {
		case round.refreshed <- struct{}{}:
		default:
		}
	}
	return memories, err
}

func TestContextConversationLiveHTTP(t *testing.T) {
	if os.Getenv("SOLVIFY_CONTEXT_CONVERSATION_LIVE_TEST") != "1" {
		t.Skip("显式启用真实HTTP单会话端到端测试")
	}
	cfg, err := config.Load("../../../../configs/config.yaml")
	if err != nil {
		t.Fatal("配置加载失败")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	dbConfig := cfg.Database.Postgres
	dsn := url.URL{Scheme: "postgres", User: url.UserPassword(dbConfig.Username, dbConfig.Password), Host: net.JoinHostPort(dbConfig.Host, strconv.Itoa(dbConfig.Port)), Path: dbConfig.Database}
	admin, err := pgx.Connect(ctx, dsn.String())
	if err != nil {
		t.Fatal("数据库连接失败")
	}
	defer admin.Close(context.Background())
	var source entity.Model
	err = admin.QueryRow(ctx, `SELECT model_id,base_url,api_key,provider,max_context_length,config FROM (
		SELECT model_id,base_url,api_key,provider,max_context_length,config,0 AS priority FROM public.models WHERE is_enabled=true
		UNION ALL SELECT model_id,base_url,api_key,api_format AS provider,max_context_length,config,1 AS priority FROM public.user_model_configs
	) AS candidates WHERE model_id IN ('deepseek-flash','deepseek-v4-pro') ORDER BY CASE WHEN model_id='deepseek-flash' THEN 0 ELSE 1 END,priority LIMIT 1`).Scan(&source.ModelID, &source.BaseURL, &source.APIKey, &source.Provider, &source.MaxContextLength, &source.Config)
	if err != nil {
		t.Fatal("未找到已启用DeepSeek模型")
	}
	schemaName := "context_e2e_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schemaName}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal("临时schema创建失败")
	}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if _, cleanupErr := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); cleanupErr != nil {
			t.Error("临时schema清理失败")
		}
	}()
	for _, table := range []string{"chat_sessions", "chat_messages", "chat_summaries", "user_memories", "models", "users"} {
		_, err = admin.Exec(ctx, "CREATE TABLE "+quotedSchema+"."+pgx.Identifier{table}.Sanitize()+" (LIKE public."+pgx.Identifier{table}.Sanitize()+" INCLUDING ALL)")
		if err != nil {
			t.Fatalf("临时表初始化失败：%s", table)
		}
	}
	params := dsn.Query()
	params.Set("search_path", schemaName+",public")
	dsn.RawQuery = params.Encode()
	db, err := gorm.Open(postgres.Open(dsn.String()), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		t.Fatal("测试数据库装配失败")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal("数据库句柄获取失败")
	}
	defer sqlDB.Close()
	redisClient, err := database.OpenRedis(&cfg.Database.Redis)
	if err != nil {
		t.Fatal("Redis连接失败")
	}
	defer redisClient.Close()
	userID := uuid.NewString()
	defer redisClient.Del(context.Background(), schemaName+":"+userID)
	extra := map[string]any{}
	if err = json.Unmarshal(source.Config, &extra); err != nil {
		t.Fatal("模型配置解析失败")
	}
	extra["max_input_tokens"] = 8192
	extra["max_output_tokens"] = 8192
	extra["max_completion_tokens"] = 1024
	extra["output_token_field"] = "max_tokens"
	extra["reasoning_mode"] = "off"
	raw, err := json.Marshal(extra)
	if err != nil {
		t.Fatal(err)
	}
	source.ID = uuid.NewString()
	source.Name = "上下文端到端隔离配置"
	source.MaxContextLength = 8192
	source.Config = datatypes.JSON(raw)
	source.IsEnabled = true
	if err = db.WithContext(ctx).Create(&source).Error; err != nil {
		t.Fatal("隔离模型配置创建失败")
	}
	budget, err := llm.ResolveContextBudget(source.ID, source.ModelID, 8192, 1024, true, config.ContextDefaults())
	if err != nil {
		t.Fatal(err)
	}
	report := conversationReport{Model: source.ModelID, Window: 8192, OutputLimit: 1024, Hard: budget.Hard, Soft: budget.Soft, Started: time.Now().In(time.FixedZone("Asia/Shanghai", 8*3600)).Format(time.RFC3339)}
	outputPath := filepath.Join("..", "..", "..", "..", "tmp", "context-conversation-live.json")
	defer func() {
		encoded, marshalErr := json.MarshalIndent(report, "", "  ")
		if marshalErr != nil {
			t.Error(marshalErr)
			return
		}
		if writeErr := os.WriteFile(outputPath, encoded, 0600); writeErr != nil {
			t.Error(writeErr)
		}
	}()
	messageRepo := repository.NewChatMessageRepository(db)
	summaryRepo := repository.NewSummaryRepository(db)
	contextSvc := conversationMeasuredContext{ContextServiceInterface: service.NewContextService(messageRepo, repository.NewUserMemoryRepository(db), summaryRepo), messages: messageRepo, name: source.ModelID}
	engine := conversationMeasuredEngine{AgentInterface: agent.NewEngine(nil, cfg.Agent), name: source.ModelID, redact: []string{source.APIKey, source.BaseURL, dbConfig.Password}}
	chatSvc := service.NewChatService(repository.NewChatSessionRepository(db), messageRepo, repository.NewTxManager(db), repository.NewModelRepository(db), nil, repository.NewUserRepository(db), cache.New(redisClient, schemaName+":", time.Hour), engine, contextSvc, nil, nil, nil, nil)
	token, err := jwt.GenerateToken(userID, "上下文联调用户", 0)
	if err != nil {
		t.Fatal("测试认证生成失败")
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery(), middleware.Auth(nil))
	router.Use(func(ginCtx *gin.Context) {
		number, _ := strconv.Atoi(ginCtx.GetHeader("X-Context-Test-Round"))
		if number > 0 && number <= len(report.Rounds) {
			ginCtx.Request = ginCtx.Request.WithContext(context.WithValue(ginCtx.Request.Context(), conversationRoundKey{}, report.Rounds[number-1]))
		}
		ginCtx.Next()
	})
	NewController(chatSvc, nil).RegisterRoutes(router.Group("/api/v1"))
	server := httptest.NewServer(router)
	defer server.Close()
	httpClient := &http.Client{Timeout: 180 * time.Second}
	post := func(path string, body any, number int) (*http.Response, error) {
		encoded, encodeErr := json.Marshal(body)
		if encodeErr != nil {
			return nil, encodeErr
		}
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+path, bytes.NewReader(encoded))
		if requestErr != nil {
			return nil, requestErr
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-Context-Test-Round", strconv.Itoa(number))
		return httpClient.Do(request)
	}
	response, err := post("/api/v1/chat/sessions", map[string]any{"title": "长对话上下文实际端到端验收", "model_id": source.ID}, 0)
	if err != nil {
		t.Fatal("HTTP创建会话失败")
	}
	var created struct {
		Code int                 `json:"code"`
		Data dto.SessionResponse `json:"data"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&created)
	response.Body.Close()
	if decodeErr != nil || created.Data.ID == "" {
		t.Fatal("HTTP会话返回无效")
	}
	report.SessionID = created.Data.ID
	questions := []string{
		"我们在做项目海星。请记住：预算4096元，负责人林舟，截止日期2026-11-20，验收口令蓝桥-47，任何删除都需要人工审批。之后我会多次提问，请只按最新约定回答，不要编造。现在用一句话确认。",
		"不看外部资料，复述刚才的项目名、负责人、预算、截止日期和验收口令，用一行回答。",
		"请记录阶段A测试资料，回答仅需‘已记录阶段A’。\n" + strings.Repeat("阶段A只做离线验证，样本编号A17，检查输入格式及取消信号。\n", 35),
		"请记录阶段B测试资料，回答仅需‘已记录阶段B’。\n" + strings.Repeat("阶段B只读访问资料，样本编号B29，检查版本和来源标识。\n", 35),
		"请记录阶段C测试资料，回答仅需‘已记录阶段C’。\n" + strings.Repeat("阶段C检查失败重试，样本编号C31，超时不能声称任务成功。\n", 35),
		"修改之前的约定：预算调整为5120元，截止日期改为2026-12-05；其他事实和人工审批约束保持不变。只确认新预算和新截止日期。",
		"请记录阶段D测试资料，回答仅需‘已记录阶段D’。\n" + strings.Repeat("阶段D检查引用展开，样本编号D43，必须核对范围和哈希。\n", 35),
		"请记录阶段E测试资料，回答仅需‘已记录阶段E’。\n" + strings.Repeat("阶段E检查并发隔离，样本编号E59，不能读取别人的上下文。\n", 35),
		"请记录阶段F测试资料，回答仅需‘已记录阶段F’。\n" + strings.Repeat("阶段F检查最终响应，样本编号F61，必须明确成功与失败终态。\n", 35),
		"只依据这次对话，请给出最新的项目名、负责人、预算、截止日期、验收口令，以及删除是否需要审批。未知就明确说未知，不要猜测，用一行回答。",
		"本轮附较长审查资料。请忽略重复叙述，只依据之前约定，用一行说明最新预算、截止日期、验收口令。\n" + strings.Repeat("审查项：保持当前问题，旧资料按策略摘要与裁剪，任何失败如实说明。\n", 110),
		"下面是不可省略的当前问题，完整读入后只回答收到。\n" + strings.Repeat("当前问题必要数据 alpha=7 budget-check=overflow\n", 1500),
		"刚才那条超长请求已经结束。现在正常继续：我们项目的最新预算、截止日期和验收口令分别是什么？未知请说未知，用一行回答。",
	}
	fail := func(message string) { report.Failures = append(report.Failures, message); t.Error(message) }
	acceptedQuestions := make([]string, 0, len(questions)-1)
	for index, question := range questions {
		round := &conversationRound{Number: index + 1, Question: question, Checks: map[string]bool{}, refreshed: make(chan struct{}, 1)}
		report.Rounds = append(report.Rounds, round)
		started := time.Now()
		response, err = post("/api/v1/chat/sessions/"+report.SessionID+"/messages", map[string]any{"content": question, "knowledge_base_ids": []string{uuid.Nil.String()}, "search_mode": "quick", "model_id": source.ID, "model_type": "system", "mcp_user_config_ids": []string{}}, round.Number)
		if err != nil {
			fail(fmt.Sprintf("第%d轮HTTP请求失败", round.Number))
			break
		}
		if strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
			var rejected envelope
			if decodeErr := json.NewDecoder(response.Body).Decode(&rejected); decodeErr != nil {
				fail(fmt.Sprintf("第%d轮入口错误解析失败", round.Number))
			}
			round.InputCode = rejected.Code
			round.InputError = rejected.Message
			response.Body.Close()
		} else {
			scanner := bufio.NewScanner(response.Body)
			scanner.Buffer(make([]byte, 4096), 2<<20)
			for scanner.Scan() {
				line := scanner.Text()
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				var event dto.StreamEvent
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
					fail(fmt.Sprintf("第%d轮SSE解析失败", round.Number))
					continue
				}
				round.Events = append(round.Events, event)
				if event.Type == "done" {
					round.Done++
					round.Answer = event.Content
				}
				if event.Type == "error" {
					round.Errors++
				}
			}
			scanErr := scanner.Err()
			response.Body.Close()
			if scanErr != nil {
				fail(fmt.Sprintf("第%d轮SSE连接错误", round.Number))
				break
			}
		}
		if index == 11 {
			round.Checks["超长输入在HTTP入口拒绝"] = round.InputCode == apperrors.CodeBadRequest && round.InputError != "" && (requestdto.SendMessageRequest{Content: question}).ValidateContent() != nil
			round.Checks["入口拒绝未启动SSE"] = len(round.Events) == 0 && round.Done == 0 && round.Errors == 0 && round.Answer == ""
			round.Checks["入口拒绝未进入上下文或模型"] = len(round.Before) == 0 && len(round.AgentHistory) == 0 && len(round.Calls) == 0 && len(round.AgentErrors) == 0
		} else {
			acceptedQuestions = append(acceptedQuestions, question)
			round.Checks["唯一成功终态"] = round.Done == 1 && round.Errors == 0 && round.Answer != ""
			if round.Done == 1 {
				select {
				case <-round.refreshed:
				case <-time.After(210 * time.Second):
					fail(fmt.Sprintf("第%d轮后台刷新未在210秒内结束", round.Number))
					return
				}
			}
		}
		persistCtx, stop := context.WithTimeout(ctx, 15*time.Second)
		for {
			stored, readErr := messageRepo.FindBySessionIDForContext(persistCtx, report.SessionID)
			if readErr != nil {
				fail("消息持久化读取失败")
				break
			}
			round.Persisted = len(stored)
			found := round.Done == 0
			for _, message := range stored {
				if message.Role == "assistant" && message.Content == round.Answer {
					found = true
				}
			}
			if found {
				round.Checks["本轮回答已持久化"] = round.Done == 0 || found
				break
			}
			select {
			case <-persistCtx.Done():
				fail("回答未在15秒内落库")
				found = true
			case <-time.After(100 * time.Millisecond):
			}
			if found {
				break
			}
		}
		stop()
		if index == 11 {
			round.Checks["入口拒绝未增加持久消息"] = round.Persisted == report.Rounds[index-1].Persisted
		}
		readRequest, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/chat/sessions/"+report.SessionID+"/messages", nil)
		if requestErr != nil {
			fail("消息HTTP读回构造失败")
			break
		}
		readRequest.Header.Set("Authorization", "Bearer "+token)
		readResponse, readErr := httpClient.Do(readRequest)
		if readErr != nil {
			fail("消息HTTP读回失败")
			break
		}
		var readBack struct {
			Data struct {
				Messages []dto.MessageResponse `json:"messages"`
			} `json:"data"`
		}
		readErr = json.NewDecoder(readResponse.Body).Decode(&readBack)
		readResponse.Body.Close()
		if readErr != nil {
			fail("消息HTTP读回解析失败")
			break
		}
		round.Checks["HTTP历史数量与数据库一致"] = len(readBack.Data.Messages) == round.Persisted
		originalQuestions := make([]string, 0, len(readBack.Data.Messages))
		for _, message := range readBack.Data.Messages {
			if message.Role == "user" {
				originalQuestions = append(originalQuestions, message.Content)
			}
		}
		originalsPreserved := len(originalQuestions) == len(acceptedQuestions)
		for questionIndex, originalQuestion := range originalQuestions {
			if questionIndex >= len(acceptedQuestions) || originalQuestion != acceptedQuestions[questionIndex] {
				originalsPreserved = false
			}
		}
		round.Checks["所有用户原文仍完整持久化"] = originalsPreserved
		if summary, summaryErr := summaryRepo.GetBySessionID(ctx, report.SessionID); summaryErr == nil && summary != nil {
			round.SummaryAfter = summary.Summary
			round.CoveredAfter = summary.CoveredCount
		}
		for _, call := range round.Calls {
			if call.Kind == "answer_input" {
				round.Checks["正文输入未超硬上限"] = call.InputTokens <= budget.Hard
				round.Checks["当前问题未改写"] = len(call.Messages) > 0 && call.Messages[len(call.Messages)-1].Content == question
			}
		}
		if index == 1 {
			round.Checks["早期事实保留"] = strings.Contains(round.Answer, "海星") && strings.Contains(round.Answer, "林舟") && strings.Contains(round.Answer, "4096") && strings.Contains(round.Answer, "2026-11-20") && strings.Contains(round.Answer, "蓝桥-47")
		}
		if index == 9 {
			round.Checks["跨长历史保留与更新"] = strings.Contains(round.Answer, "海星") && strings.Contains(round.Answer, "林舟") && strings.Contains(round.Answer, "5120") && strings.Contains(round.Answer, "2026-12-05") && strings.Contains(round.Answer, "蓝桥-47") && strings.Contains(round.Answer, "审批")
			round.Checks["修改后未沿用旧预算日期"] = !strings.Contains(round.Answer, "4096") && !strings.Contains(round.Answer, "2026-11-20")
		}
		if index == 10 || index == 12 {
			round.Checks["最新事实及口令保留"] = strings.Contains(round.Answer, "5120") && strings.Contains(round.Answer, "2026-12-05") && strings.Contains(round.Answer, "蓝桥-47")
		}
		for label, passed := range round.Checks {
			if !passed {
				fail(fmt.Sprintf("第%d轮：%s失败", round.Number, label))
			}
		}
		round.ElapsedMS = time.Since(started).Milliseconds()
		t.Logf("轮次=%d 历史前=%d条/%dtoken 选取后=%d条/%dtoken 摘要覆盖=%d→%d 成功=%d 错误=%d 耗时=%dms", round.Number, len(round.Before), round.BeforeTokens, len(round.After)+len(round.Recall), round.AfterTokens, round.CoveredBefore, round.CoveredAfter, round.Done, round.Errors, round.ElapsedMS)
	}
	if len(report.Rounds) != len(questions) {
		fail("未完成全部连续提问")
	}
	if len(report.Rounds) > 9 && report.Rounds[9].CoveredBefore == 0 {
		fail("长对话没有取得持久会话摘要")
	}
}
