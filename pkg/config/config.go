package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/mitchellh/mapstructure"
	"time"
)

const defaultConfigPath = "configs/config.yaml"

// Config 描述应用全局配置
type Config struct {
	App            AppConfig            `mapstructure:"app"`
	Log            LogConfig            `mapstructure:"log"`
	CORS           CORSConfig           `mapstructure:"cors"`
	Agent          AgentConfig          `mapstructure:"agent"`
	LLM            LLMConfig            `mapstructure:"llm"`
	Embedding      EmbeddingConfig      `mapstructure:"embedding"`
	RAG            RAGConfig            `mapstructure:"rag"`
	Tools          ToolsConfig          `mapstructure:"tools"`
	DingTalk       DingTalkConfig       `mapstructure:"dingtalk"`
	DocumentParser DocumentParserConfig `mapstructure:"document_parser"`
	Server         ServerConfig         `mapstructure:"server"`
	Database       DatabaseConfig       `mapstructure:"database"`
	JWT            JWTConfig            `mapstructure:"jwt"`
	Email          EmailConfig          `mapstructure:"email"`
	Observability  ObservabilityConfig  `mapstructure:"observability"`
}

// AppConfig 描述应用基础信息
type AppConfig struct {
	Name    string `mapstructure:"name"`
	Version string `mapstructure:"version"`
	Env     string `mapstructure:"env"`
	Mode    string `mapstructure:"mode"`
}

// LogConfig 描述日志配置
type LogConfig struct {
	Level      string `mapstructure:"level"`
	Filename   string `mapstructure:"filename"`
	MaxSize    int    `mapstructure:"max_size"`
	MaxBackups int    `mapstructure:"max_backups"`
	MaxAge     int    `mapstructure:"max_age"`
	Compress   bool   `mapstructure:"compress"`
}
// CORSConfig 描述跨域资源共享配置
type CORSConfig struct {
	Enabled          bool     `mapstructure:"enabled"`
	AllowOrigins     []string `mapstructure:"allow_origins"`
	AllowMethods     []string `mapstructure:"allow_methods"`
	AllowHeaders     []string `mapstructure:"allow_headers"`
	ExposeHeaders    []string `mapstructure:"expose_headers"`
	AllowCredentials bool     `mapstructure:"allow_credentials"`
	MaxAge           int      `mapstructure:"max_age"`
}

// AgentConfig 描述 Agent 行为开关
type AgentConfig struct {
	EnableDemo              bool    `mapstructure:"enable_demo"`
	MaxIterations           int     `mapstructure:"max_iterations"`
	ScoreThreshold          float64 `mapstructure:"score_threshold"`
	// QuickAgentMaxIterations 指定快速模式（eino ChatModelAgent）最多执行几个
	// ReAct 循环，默认 2（最多 1 次工具调用+出答案）。
	QuickAgentMaxIterations int `mapstructure:"quick_agent_max_iterations"`
}

// LLMConfig 描述模型调用配置
type LLMConfig struct {
	Provider    string  `mapstructure:"provider"`
	APIFormat   string  `mapstructure:"api_format"`
	Model       string  `mapstructure:"model"`
	APIKey      string  `mapstructure:"api_key"`
	BaseURL     string  `mapstructure:"base_url"`
	Temperature float64 `mapstructure:"temperature"`
	MaxTokens   int     `mapstructure:"max_tokens"`
	Timeout     int     `mapstructure:"timeout"`
}

// EmbeddingConfig 描述 Embedding 模型配置
type EmbeddingConfig struct {
	Provider  string `mapstructure:"provider"`
	Model     string `mapstructure:"model"`
	APIKey    string `mapstructure:"api_key"`
	BaseURL   string `mapstructure:"base_url"`
	Dimension int    `mapstructure:"dimension"`
	BatchSize int    `mapstructure:"batch_size"`
	Timeout   int    `mapstructure:"timeout"`
}

// RAGConfig 描述检索增强配置
type RAGConfig struct {
	Enabled        bool    `mapstructure:"enabled"`
	TopK           int     `mapstructure:"top_k"`
	RecallK        int     `mapstructure:"recall_k"` // 混合检索召回量（Rerank 前），默认 TopK*5
	ScoreThreshold float64 `mapstructure:"score_threshold"`
	VectorWeight   float64 `mapstructure:"vector_weight"`
	KeywordWeight  float64 `mapstructure:"keyword_weight"`
	// KeywordScoreThreshold 是「向量侧全灭时」关键词结果的最低匹配比例。
	// 之前这个字段没有配置入口，构造器只能吃硬编码默认值 0.25。
	KeywordScoreThreshold float64        `mapstructure:"keyword_score_threshold"`
	RRFK                  float64        `mapstructure:"rrf_k"`
	// CandidateMultiplier 是检索候选池的放大系数：两侧各取 TopK×N 条候选，
	// 融合 / 过滤后再收敛到 TopK。默认 1（即只取 TopK 条）。
	//
	// 为什么默认 1：2026-09-23 的 A/B 实测（test1/rag_eval/AB-对比结论-20260923.md）
	// 把每侧候选从 6 收到 3，精度 82.1% → 95.7%、含噪率 17.9% → 4.3%，
	// 而 hit@1 / hit@3 / MRR 全部持平（都已满分）。
	// ≤ 0 时由检索器回落到默认值（不会变成 LIMIT 0 恒空）。
	// ⚠️ 与 rag 的 min-max 归一化耦合：归一化把每侧最后一名压成 0、交叉过滤又丢掉
	// 单源且 0 的结果 ⇒ 每侧最后一名必然出局，实际存活 = TopK-1。改归一化前先读报告。
	CandidateMultiplier int `mapstructure:"candidate_multiplier"`
	Reranker              RerankerConfig `mapstructure:"reranker"`
	Expander              ExpanderConfig `mapstructure:"expander"`
}

// RerankerConfig 描述重排序配置
type RerankerConfig struct {
	Enabled        bool    `mapstructure:"enabled"`
	Endpoint       string  `mapstructure:"endpoint"`
	Model          string  `mapstructure:"model"`
	APIKey         string  `mapstructure:"api_key"`
	TopN           int     `mapstructure:"top_n"`
	Timeout        int     `mapstructure:"timeout"`
	ScoreThreshold float64 `mapstructure:"score_threshold"`
}

// ExpanderConfig 描述相邻分块扩展配置
type ExpanderConfig struct {
	Enabled        bool    `mapstructure:"enabled"`
	WindowSize     int     `mapstructure:"window_size"`
	MaxChunkTokens int     `mapstructure:"max_chunk_tokens"`
	DedupThreshold float64 `mapstructure:"dedup_threshold"`
}

// ToolsConfig 描述工具调用配置
type ToolsConfig struct {
	Enabled    bool                   `mapstructure:"enabled"`
	WebSearch  WebSearchConfig        `mapstructure:"web_search"`
	MCPServers []SystemMCPServerConfig `mapstructure:"mcp_servers"`
}

// WebSearchConfig 描述网络搜索工具配置
type WebSearchConfig struct {
	APIKey  string `mapstructure:"api_key"`
	BaseURL string `mapstructure:"base_url"`
}

// SystemMCPServerConfig 系统预置 MCP 服务器配置
type SystemMCPServerConfig struct {
	Name        string            `mapstructure:"name" json:"name"`
	Description string            `mapstructure:"description" json:"description"`
	Transport   string            `mapstructure:"transport" json:"transport"`               // stdio | sse | http
	Command     string            `mapstructure:"command" json:"command,omitempty"`         // stdio
	Args        []string          `mapstructure:"args" json:"args,omitempty"`               // stdio
	Env         map[string]string `mapstructure:"env" json:"env,omitempty"`                 // stdio
	URL         string            `mapstructure:"url" json:"url,omitempty"`                 // sse/http
	Headers     map[string]string `mapstructure:"headers" json:"headers,omitempty"`         // sse/http
	Timeout     int               `mapstructure:"timeout" json:"timeout,omitempty"`         // 默认 30
}

// DingTalkConfig 描述钉钉开放平台配置
type DingTalkConfig struct {
	AppKey           string `mapstructure:"app_key"`
	AppSecret        string `mapstructure:"app_secret"`
	OAuthRedirectURI string `mapstructure:"oauth_redirect_uri"`
}

// DocumentParserConfig 描述文档解析器配置
type DocumentParserConfig struct {
	PythonPath     string `mapstructure:"python_path"`
	ScriptPath     string `mapstructure:"script_path"`
	TimeoutSeconds int    `mapstructure:"timeout_seconds"`
}

// ServerConfig 描述进程关闭配置
type ServerConfig struct {
	Host                   string `mapstructure:"host"`
	Port                   int    `mapstructure:"port"`
	ShutdownTimeoutSeconds int    `mapstructure:"shutdown_timeout_seconds"`
}

// DatabaseConfig 描述数据库和缓存配置
type DatabaseConfig struct {
	Postgres PostgresConfig `mapstructure:"postgres"`
	Redis    RedisConfig    `mapstructure:"redis"`
}

// PostgresConfig 描述 PostgreSQL 数据库配置
type PostgresConfig struct {
	Host                   string `mapstructure:"host"`
	Port                   int    `mapstructure:"port"`
	Username               string `mapstructure:"username"`
	Password               string `mapstructure:"password"`
	Database               string `mapstructure:"database"`
	TimeZone               string `mapstructure:"timezone"`
	MaxIdleConns           int    `mapstructure:"max_idle_conns"`
	MaxOpenConns           int    `mapstructure:"max_open_conns"`
	ConnMaxLifetimeMinutes int    `mapstructure:"conn_max_lifetime_minutes"`
	EnablePGVector         bool   `mapstructure:"enable_pgvector"`
}

// RedisConfig 描述 Redis 配置
type RedisConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
	PoolSize int    `mapstructure:"pool_size"`
}

type JWTConfig struct {
	Secret      string        `mapstructure:"secret"`
	ExpireHours time.Duration `mapstructure:"expire_hours"`
}

type EmailConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

// ObservabilityConfig 描述「eino → Langfuse」这条三方链路的配置。
//
// ⚠️ 本结构体已随自研可观测性模块整体裁剪，只剩下面这些 —— 采样率 / 慢查询阈值 /
// 落库开关 / 指标格式 / sink 批量参数 / 本地 OTel 出口 / 白名单 / 基数上限 全部移除，
// 因为它们描述的是「自研 span 树 + 落库 + Prometheus 出口」，那些已经不存在了。
// 保留下来的键都有唯一消费点：observability.NewTracer（internal/observability）。
type ObservabilityConfig struct {
	// PIIMaskSecret 控制官方 Config.MaskFunc 是否连「密钥 / Token 形态」一起打码。
	// 凭据类必须打，生产上应当为 true。
	PIIMaskSecret bool `mapstructure:"pii_mask_secret"`

	// OTelServiceName 同时作为官方 Config.ServiceName 与 trace 名（langfuse name）。
	OTelServiceName string `mapstructure:"otel_service_name"`

	// ── Langfuse（官方 eino callback v2，走平台 OTLP 入口）──
	//
	// 本项目只有这一条三方链路：官方 eino callback →
	// 平台 /api/public/otel/v1/traces。
	//
	// 官方 v2 走【标准 OTLP/HTTP】，不再用已弃用的 /api/public/ingestion 事件接口
	// （那条通道平台已公告 2026-11-16 关停，此后只收 score-create）。
	//
	// Host / PublicKey / SecretKey 任一为空即视为未配置，启动时跳过官方 callback。
	// 凭据属敏感信息，只放本地 config.yaml（已 gitignore）或环境变量。
	LangfuseHost      string `mapstructure:"langfuse_host"`
	LangfusePublicKey string `mapstructure:"langfuse_public_key"`
	LangfuseSecretKey string `mapstructure:"langfuse_secret_key"`

	// MaxExportBatchSize / BatchTimeoutMs 控制 OTLP 批量导出节奏，
	// 对应官方 Config.MaxExportBatchSize / Config.BatchTimeout。
	LangfuseMaxExportBatchSize int `mapstructure:"langfuse_max_export_batch_size"`
	LangfuseBatchTimeoutMs     int `mapstructure:"langfuse_batch_timeout_ms"`
	// TimeoutMs 是单次 OTLP HTTP 请求超时，对应官方 Config.Timeout（官方默认 10s）。
	LangfuseTimeoutMs int `mapstructure:"langfuse_timeout_ms"`
	// SampleRate 是官方 handler 自己的采样率（官方默认 1.0 = 全采）。
	LangfuseSampleRate float64 `mapstructure:"langfuse_sample_rate"`
	// Release / Tags 用于平台侧按版本、环境筛选。
	//
	// ⚠️ 这两个值必须由 observability.langfuseOptions 经 StartTrace 的选项一并下发：
	// 官方 StartTrace 是「opts 覆盖 handler 默认值」，只配在 handler 上会被顶掉。
	// ⚠️ Tags 只在这里配：官方 WithTags 是 append（不是覆盖），两处都给会在平台上重复。
	LangfuseRelease string   `mapstructure:"langfuse_release"`
	LangfuseTags    []string `mapstructure:"langfuse_tags"`

	// MaxQueueSize 是 OTel BatchSpanProcessor 的本地队列容量。
	// ⚠️ 队列满时事件会被丢弃，但官方 v2 会计入丢弃统计并按 DropLogInterval 汇总告警
	// （v1 是静默丢弃），所以别设太小。
	LangfuseMaxQueueSize int `mapstructure:"langfuse_max_queue_size"`
	// MaxSpanAttributeBytes 是「单个 span 上由本 callback 写入的属性」总量上限，
	// 对应官方 Config.MaxSpanAttributeBytes；超限时按 input → output → metadata
	// 从大到小依次截断（官方默认 4_000_000）。
	LangfuseMaxSpanAttributeBytes int `mapstructure:"langfuse_max_span_attribute_bytes"`
}

// LangfuseEnabled 判断官方 eino → Langfuse callback 是否具备启用条件。
//
// 判据「三项凭据齐全」只在这里定义一次，且只有一处消费点（observability.NewTracer，
// 决定是否建 handler）；判据不成立就返回 nil Tracer，调用点靠 nil 接收者空转。
// 若再在别处写第二个判据，就会出现「handler 没注册、但每条请求都在开 trace」的错位。
func (c ObservabilityConfig) LangfuseEnabled() bool {
	return c.LangfuseHost != "" && c.LangfusePublicKey != "" && c.LangfuseSecretKey != ""
}

var globalConfig *Config

// Load 读取配置文件并应用环境变量覆盖
func Load(configPath string) (*Config, error) {
	if configPath == "" {
		configPath = getEnv("CONFIG_PATH", defaultConfigPath)
	}

	cfg := Default()
	data, err := os.ReadFile(configPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("读取配置文件失败: %w", err)
		}
	} else {
		values := map[string]any{}
		if err := yaml.Unmarshal(data, &values); err != nil {
			return nil, fmt.Errorf("解析配置文件失败: %w", err)
		}
		if err := mapstructure.Decode(values, cfg); err != nil {
			return nil, fmt.Errorf("映射配置结构失败: %w", err)
		}
	}

	loadDotEnv(".env")
	applyEnv(cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	globalConfig = cfg
	return cfg, nil
}

// MustLoad 加载配置并在失败时 panic
func MustLoad(configPath string) *Config {
	cfg, err := Load(configPath)
	if err != nil {
		panic(err)
	}
	return cfg
}

// Get 获取全局配置
func Get() *Config {
	if globalConfig == nil {
		panic("配置未初始化，请先调用 Load")
	}
	return globalConfig
}

// Default 返回可直接启动的默认配置
func Default() *Config {
	return &Config{
		App: AppConfig{
			Name:    "solvify-agent",
			Version: "0.1.0",
			Env:     "development",
			Mode:    "release",
		},
		Log: LogConfig{
			Level:      "info",
			Filename:   "logs/solvify-agent.log",
			MaxSize:    100,
			MaxBackups: 7,
			MaxAge:     30,
			Compress:   true,
		},
		Agent: AgentConfig{
			EnableDemo:              true,
			MaxIterations:           4,
			ScoreThreshold:          0.7,
			QuickAgentMaxIterations: 2,
		},
		LLM: LLMConfig{
			Provider:    "mock",
			Model:       "mock-knowledge-assistant",
			Temperature: 0.7,
			MaxTokens:   2000,
			Timeout:     30,
		},
		Embedding: EmbeddingConfig{
			Provider:  "openai",
			Model:     "text-embedding-v4",
			Dimension: 1024,
			BatchSize: 10,
			Timeout:   15,
		},
		RAG: RAGConfig{
			Enabled:             true,
			TopK:                3,
			RecallK:             20,
			ScoreThreshold:      0.7,
			CandidateMultiplier: 1,
			Reranker: RerankerConfig{
				Enabled:        false,
				TopN:           3,
				Timeout:        5,
				ScoreThreshold: 0.5,
			},
			Expander: ExpanderConfig{
				Enabled:        false,
				WindowSize:     1,
				MaxChunkTokens: 1000,
				DedupThreshold: 0.8,
			},
		},
		Tools: ToolsConfig{
			Enabled: true,
		},
		DingTalk: DingTalkConfig{
			OAuthRedirectURI: "http://localhost:5173/dingtalk/bind",
		},
		DocumentParser: DocumentParserConfig{
			PythonPath:     "python",
			ScriptPath:     "pkg/documentparser/python/parse_document.py",
			TimeoutSeconds: 30,
		},
		Server: ServerConfig{
			Host:                   "",
			Port:                   8080,
			ShutdownTimeoutSeconds: 10,
		},
		Database: DatabaseConfig{
			Postgres: PostgresConfig{
				Host:                   "127.0.0.1",
				Port:                   5432,
				Username:               "postgres",
				Database:               "solvify_agent",
				TimeZone:               "Asia/Shanghai",
				MaxIdleConns:           5,
				MaxOpenConns:           20,
				ConnMaxLifetimeMinutes: 60,
				EnablePGVector:         true,
			},
			Redis: RedisConfig{
				Host:     "127.0.0.1",
				Port:     6379,
				DB:       0,
				PoolSize: 10,
			},
		},
		Observability: ObservabilityConfig{
			PIIMaskSecret:   true,
			OTelServiceName: "solvify-agent",
			// Langfuse 官方 callback（v2 / OTLP）：下列数值与官方默认值一致
			// （队列 2048 / 每批 512 / 批次超时 5s / 单次请求 10s）。
			// ⚠️ BatchTimeoutMs 决定「低流量时最晚多久上报」：本地开发嫌慢就调小它。
			LangfuseMaxExportBatchSize:    512,
			LangfuseBatchTimeoutMs:        5000,
			LangfuseTimeoutMs:             10000,
			LangfuseSampleRate:            1.0,
			LangfuseMaxQueueSize:          2048,
			LangfuseMaxSpanAttributeBytes: 4_000_000,
		},
	}
}

// Validate 校验配置是否满足启动要求
func (c *Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return errors.New("server.port 必须在 1 到 65535 之间")
	}
	if c.LLM.Provider == "" {
		return errors.New("LLM provider 不能为空")
	}
	if c.LLM.Model == "" {
		return errors.New("LLM model 不能为空")
	}
	if c.Server.ShutdownTimeoutSeconds <= 0 {
		return errors.New("服务关闭超时时间必须大于 0")
	}
	if c.Database.Postgres.Host == "" {
		return errors.New("database.postgres.host 不能为空")
	}
	if c.Database.Postgres.Port <= 0 || c.Database.Postgres.Port > 65535 {
		return errors.New("database.postgres.port 必须在 1 到 65535 之间")
	}
	if c.Database.Postgres.Username == "" {
		return errors.New("database.postgres.username 不能为空")
	}
	if c.Database.Postgres.Database == "" {
		return errors.New("database.postgres.database 不能为空")
	}
	if c.Database.Postgres.MaxIdleConns < 0 || c.Database.Postgres.MaxOpenConns < 0 {
		return errors.New("PostgreSQL 连接池数量不能小于 0")
	}
	if c.Database.Postgres.ConnMaxLifetimeMinutes <= 0 {
		return errors.New("PostgreSQL 连接最大生命周期必须大于 0")
	}
	if c.Database.Redis.Host == "" {
		return errors.New("database.redis.host 不能为空")
	}
	if c.Database.Redis.Port <= 0 || c.Database.Redis.Port > 65535 {
		return errors.New("database.redis.port 必须在 1 到 65535 之间")
	}
	if c.Database.Redis.DB < 0 {
		return errors.New("database.redis.db 不能小于 0")
	}
	if c.Database.Redis.PoolSize <= 0 {
		return errors.New("database.redis.pool_size 必须大于 0")
	}
	if c.DocumentParser.TimeoutSeconds <= 0 {
		return errors.New("document_parser.timeout_seconds 必须大于 0")
	}
	if c.Agent.QuickAgentMaxIterations <= 0 {
		return errors.New("agent.quick_agent_max_iterations 必须大于 0")
	}
	if c.Agent.MaxIterations <= 0 {
		return errors.New("agent.max_iterations 必须大于 0")
	}
	return nil
}

// Addr 返回 HTTP Server 监听地址
func (c *ServerConfig) Addr() string {
	if c.Host == "" {
		return fmt.Sprintf(":%d", c.Port)
	}
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

// applyEnv 使用环境变量覆盖配置文件值
func applyEnv(cfg *Config) {
	cfg.App.Env = getEnv("APP_ENV", cfg.App.Env)
	cfg.App.Mode = getEnv("APP_MODE", cfg.App.Mode)
	cfg.Server.Host = getEnv("SERVER_HOST", cfg.Server.Host)
	cfg.Log.Level = getEnv("LOG_LEVEL", cfg.Log.Level)
	cfg.Log.Filename = getEnv("LOG_FILENAME", cfg.Log.Filename)

	// LLM 配置
	cfg.LLM.Provider = getEnv("LLM_PROVIDER", cfg.LLM.Provider)
	cfg.LLM.Model = getEnv("LLM_MODEL", cfg.LLM.Model)
	cfg.LLM.APIKey = getEnv("LLM_API_KEY", cfg.LLM.APIKey)
	cfg.LLM.BaseURL = getEnv("LLM_BASE_URL", cfg.LLM.BaseURL)
	if value := os.Getenv("LLM_TEMPERATURE"); value != "" {
		cfg.LLM.Temperature = parseFloat(value, cfg.LLM.Temperature)
	}
	if value := os.Getenv("LLM_MAX_TOKENS"); value != "" {
		cfg.LLM.MaxTokens = parseInt(value, cfg.LLM.MaxTokens)
	}
	if value := os.Getenv("LLM_TIMEOUT"); value != "" {
		cfg.LLM.Timeout = parseInt(value, cfg.LLM.Timeout)
	}

	// Embedding 配置
	cfg.Embedding.Provider = getEnv("EMBEDDING_PROVIDER", cfg.Embedding.Provider)
	cfg.Embedding.Model = getEnv("EMBEDDING_MODEL", cfg.Embedding.Model)
	cfg.Embedding.APIKey = getEnv("EMBEDDING_API_KEY", cfg.Embedding.APIKey)
	cfg.Embedding.BaseURL = getEnv("EMBEDDING_BASE_URL", cfg.Embedding.BaseURL)
	if cfg.Embedding.APIKey == "" {
		cfg.Embedding.APIKey = getEnv("DASHSCOPE_API_KEY", cfg.Embedding.APIKey)
	}
	if cfg.Embedding.BaseURL == "" {
		cfg.Embedding.BaseURL = getEnv("DASHSCOPE_BASE_URL", cfg.Embedding.BaseURL)
	}
	if value := os.Getenv("EMBEDDING_DIMENSION"); value != "" {
		cfg.Embedding.Dimension = parseInt(value, cfg.Embedding.Dimension)
	}
	if value := os.Getenv("EMBEDDING_BATCH_SIZE"); value != "" {
		cfg.Embedding.BatchSize = parseInt(value, cfg.Embedding.BatchSize)
	}
	if value := os.Getenv("EMBEDDING_TIMEOUT"); value != "" {
		cfg.Embedding.Timeout = parseInt(value, cfg.Embedding.Timeout)
	}

	// 数据库配置
	cfg.Database.Postgres.Host = getEnv("POSTGRES_HOST", cfg.Database.Postgres.Host)
	cfg.Database.Postgres.Username = getEnv("POSTGRES_USERNAME", cfg.Database.Postgres.Username)
	cfg.Database.Postgres.Password = getEnv("POSTGRES_PASSWORD", cfg.Database.Postgres.Password)
	cfg.Database.Postgres.Database = getEnv("POSTGRES_DATABASE", cfg.Database.Postgres.Database)
	cfg.Database.Postgres.TimeZone = getEnv("POSTGRES_TIMEZONE", cfg.Database.Postgres.TimeZone)
	cfg.Database.Redis.Host = getEnv("REDIS_HOST", cfg.Database.Redis.Host)
	cfg.Database.Redis.Password = getEnv("REDIS_PASSWORD", cfg.Database.Redis.Password)

	if value := os.Getenv("RAG_ENABLED"); value != "" {
		cfg.RAG.Enabled = parseBool(value, cfg.RAG.Enabled)
	}
	if value := os.Getenv("RERANKER_ENABLED"); value != "" {
		cfg.RAG.Reranker.Enabled = parseBool(value, cfg.RAG.Reranker.Enabled)
	}
	cfg.RAG.Reranker.Endpoint = getEnv("RERANKER_ENDPOINT", cfg.RAG.Reranker.Endpoint)
	cfg.RAG.Reranker.Model = getEnv("RERANKER_MODEL", cfg.RAG.Reranker.Model)
	cfg.RAG.Reranker.APIKey = getEnv("RERANKER_API_KEY", cfg.RAG.Reranker.APIKey)
	if value := os.Getenv("RERANKER_TOP_N"); value != "" {
		cfg.RAG.Reranker.TopN = parseInt(value, cfg.RAG.Reranker.TopN)
	}
	if value := os.Getenv("RERANKER_TIMEOUT"); value != "" {
		cfg.RAG.Reranker.Timeout = parseInt(value, cfg.RAG.Reranker.Timeout)
	}
	if value := os.Getenv("RERANKER_SCORE_THRESHOLD"); value != "" {
		cfg.RAG.Reranker.ScoreThreshold = parseFloat(value, cfg.RAG.Reranker.ScoreThreshold)
	}
	if value := os.Getenv("EXPANDER_ENABLED"); value != "" {
		cfg.RAG.Expander.Enabled = parseBool(value, cfg.RAG.Expander.Enabled)
	}
	if value := os.Getenv("EXPANDER_WINDOW_SIZE"); value != "" {
		cfg.RAG.Expander.WindowSize = parseInt(value, cfg.RAG.Expander.WindowSize)
	}
	if value := os.Getenv("EXPANDER_MAX_CHUNK_TOKENS"); value != "" {
		cfg.RAG.Expander.MaxChunkTokens = parseInt(value, cfg.RAG.Expander.MaxChunkTokens)
	}
	if value := os.Getenv("EXPANDER_DEDUP_THRESHOLD"); value != "" {
		cfg.RAG.Expander.DedupThreshold = parseFloat(value, cfg.RAG.Expander.DedupThreshold)
	}
	if value := os.Getenv("POSTGRES_ENABLE_PGVECTOR"); value != "" {
		cfg.Database.Postgres.EnablePGVector = parseBool(value, cfg.Database.Postgres.EnablePGVector)
	}
	if value := os.Getenv("TOOLS_ENABLED"); value != "" {
		cfg.Tools.Enabled = parseBool(value, cfg.Tools.Enabled)
	}
	cfg.DingTalk.AppKey = getEnv("DINGTALK_APP_KEY", cfg.DingTalk.AppKey)
	cfg.DingTalk.AppSecret = getEnv("DINGTALK_APP_SECRET", cfg.DingTalk.AppSecret)
	cfg.DingTalk.OAuthRedirectURI = getEnv("DINGTALK_OAUTH_REDIRECT_URI", cfg.DingTalk.OAuthRedirectURI)
	cfg.DocumentParser.PythonPath = getEnv("DOCUMENT_PARSER_PYTHON_PATH", cfg.DocumentParser.PythonPath)
	cfg.DocumentParser.ScriptPath = getEnv("DOCUMENT_PARSER_SCRIPT_PATH", cfg.DocumentParser.ScriptPath)
	if value := os.Getenv("DOCUMENT_PARSER_TIMEOUT_SECONDS"); value != "" {
		cfg.DocumentParser.TimeoutSeconds = parseInt(value, cfg.DocumentParser.TimeoutSeconds)
	}
	if value := os.Getenv("SHUTDOWN_TIMEOUT_SECONDS"); value != "" {
		cfg.Server.ShutdownTimeoutSeconds = parseInt(value, cfg.Server.ShutdownTimeoutSeconds)
	}
	if value := os.Getenv("SERVER_PORT"); value != "" {
		cfg.Server.Port = parseInt(value, cfg.Server.Port)
	}
	if value := os.Getenv("POSTGRES_PORT"); value != "" {
		cfg.Database.Postgres.Port = parseInt(value, cfg.Database.Postgres.Port)
	}
	if value := os.Getenv("POSTGRES_MAX_IDLE_CONNS"); value != "" {
		cfg.Database.Postgres.MaxIdleConns = parseInt(value, cfg.Database.Postgres.MaxIdleConns)
	}
	if value := os.Getenv("POSTGRES_MAX_OPEN_CONNS"); value != "" {
		cfg.Database.Postgres.MaxOpenConns = parseInt(value, cfg.Database.Postgres.MaxOpenConns)
	}
	if value := os.Getenv("POSTGRES_CONN_MAX_LIFETIME_MINUTES"); value != "" {
		cfg.Database.Postgres.ConnMaxLifetimeMinutes = parseInt(value, cfg.Database.Postgres.ConnMaxLifetimeMinutes)
	}
	if value := os.Getenv("REDIS_PORT"); value != "" {
		cfg.Database.Redis.Port = parseInt(value, cfg.Database.Redis.Port)
	}
	if value := os.Getenv("REDIS_DB"); value != "" {
		cfg.Database.Redis.DB = parseInt(value, cfg.Database.Redis.DB)
	}
	if value := os.Getenv("REDIS_POOL_SIZE"); value != "" {
		cfg.Database.Redis.PoolSize = parseInt(value, cfg.Database.Redis.PoolSize)
	}
	if value := os.Getenv("LOG_MAX_SIZE"); value != "" {
		cfg.Log.MaxSize = parseInt(value, cfg.Log.MaxSize)
	}
	if value := os.Getenv("LOG_MAX_BACKUPS"); value != "" {
		cfg.Log.MaxBackups = parseInt(value, cfg.Log.MaxBackups)
	}
	if value := os.Getenv("LOG_MAX_AGE"); value != "" {
		cfg.Log.MaxAge = parseInt(value, cfg.Log.MaxAge)
	}
	if value := os.Getenv("LOG_COMPRESS"); value != "" {
		cfg.Log.Compress = parseBool(value, cfg.Log.Compress)
	}

	// Observability 配置（只剩「eino → Langfuse」这条三方链路用得到的项）
	if value := os.Getenv("OBSERVABILITY_PII_MASK_SECRET"); value != "" {
		cfg.Observability.PIIMaskSecret = parseBool(value, cfg.Observability.PIIMaskSecret)
	}
	cfg.Observability.OTelServiceName = getEnv("OTEL_SERVICE_NAME", cfg.Observability.OTelServiceName)

	// Agent 行为开关
	if value := os.Getenv("AGENT_QUICK_MAX_ITERATIONS"); value != "" {
		cfg.Agent.QuickAgentMaxIterations = parseInt(value, cfg.Agent.QuickAgentMaxIterations)
	}
	if value := os.Getenv("AGENT_MAX_ITERATIONS"); value != "" {
		cfg.Agent.MaxIterations = parseInt(value, cfg.Agent.MaxIterations)
	}
	if value := os.Getenv("AGENT_SCORE_THRESHOLD"); value != "" {
		cfg.Agent.ScoreThreshold = parseFloat(value, cfg.Agent.ScoreThreshold)
	}
}

// getEnv 读取环境变量并在为空时返回默认值
func getEnv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// loadDotEnv 加载本地 .env 文件且不覆盖已有环境变量
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}

// parseBool 解析布尔环境变量并在失败时保留原值
func parseBool(value string, fallback bool) bool {
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// parseInt 解析整数环境变量并在失败时保留原值
func parseInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// parseFloat 解析浮点数环境变量并在失败时保留原值
func parseFloat(value string, fallback float64) float64 {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
