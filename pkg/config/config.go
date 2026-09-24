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

// otel_exporter 的合法取值。
//
// 为什么定义在配置层而不是 observability 的实现层：这个值有两个消费者 ——
// Validate（决定启动能不能过）与 observability.buildOTelExporter（决定构造哪个 exporter）。
// 以前两边各写一份字符串字面量，结果 OTLP 出口删了、Validate 却还认 otlp：
// 配置写成 otlp 时校验通过、构造失败，再被降级成 noop ⇒ 三方链路悄悄没了，只留一条 WARN。
// 收敛成常量后，加/删合法取值只有一处要改，不会再出现「校验说合法、实现说不认识」。
const (
	// OTelExporterNoop 不挂任何 SpanExporter。span 仍会生成（trace_id 照常有，
	// 双轨对齐与日志串查都要用它），只是不往任何地方导出。
	OTelExporterNoop = "noop"
	// OTelExporterStdout 把 span 以 JSON 打到 stdout，仅本地调试看 span 结构用。
	OTelExporterStdout = "stdout"
)

// ObservabilityConfig 描述可观测配置
type ObservabilityConfig struct {
	Enabled              bool     `mapstructure:"enabled"`
	SamplingRate         float64  `mapstructure:"sampling_rate"`
	ErrorAlwaysSample    bool     `mapstructure:"error_always_sample"`
	SlowThresholdMs      int      `mapstructure:"slow_threshold_ms"`
	FeedbackAlwaysSample bool     `mapstructure:"feedback_always_sample"`
	TraceTableEnabled    bool     `mapstructure:"trace_table_enabled"`
	ExportLogEnabled     bool     `mapstructure:"export_log_enabled"`
	MetricsFormat        string   `mapstructure:"metrics_format"`
	SinkBufferSize       int      `mapstructure:"sink_buffer_size"`
	SinkBatchSize        int      `mapstructure:"sink_batch_size"`
	SinkFlushIntervalMs  int      `mapstructure:"sink_flush_interval_ms"`
	PIIContentMaxChars   int      `mapstructure:"pii_content_max_chars"`
	PIIMaskSecret        bool     `mapstructure:"pii_mask_secret"`
	FeedbackEnabled      bool     `mapstructure:"feedback_enabled"`
	WhiteListUserIDs     []string `mapstructure:"whitelist_user_ids"`
	MaxCardinalityLabels int      `mapstructure:"max_cardinality_labels"`

	// OTel 配置：Trace 仍走 OpenTelemetry SDK，但【只服务于本地调试】。
	//
	// 三方追踪已改为官方 eino → Langfuse callback 直连平台（见本结构体的 langfuse_* 段）。
	// 平台那条链路是【官方 callback 自己】说标准 OTLP/HTTP，不再需要我们这边的 OTLP 出口
	// 与 Collector 垫片，所以 OTLP 出口连同只有它才用的三个配置项
	// （otel_otlp_endpoint / otel_insecure / otel_headers）一并移除。
	//
	// 合法取值只有下面的 OTelExporterNoop / OTelExporterStdout；
	// 写成别的值（包括老配置里残留的 otlp）会在 Validate 里直接报错、服务起不来。
	OTelExporter    string `mapstructure:"otel_exporter"`
	OTelServiceName string `mapstructure:"otel_service_name"`
	// OTelSamplingRate 头采样概率 0~1，0 = 不采样，1 = 全采样
	OTelSamplingRate float64 `mapstructure:"otel_sampling_rate"`
	// OTelBizRoutes 是「无条件保留」的业务路由模板列表：命中它的请求不受
	// OTelSamplingRate 影响，一定导出到三方平台。
	//
	// 为什么判据是「路由模板」而不是「请求路径」：模板取自 gin 的 c.FullPath()，
	// 形如 /api/v1/chat/sessions/:id/messages —— 同一条路由下不同 session id 会归一
	// 成同一个值，配置里不会出现「每个 id 一条」的基数爆炸。
	//
	// 为什么必须精确相等、不能用前缀：实测噪声第一名是
	// /api/v1/chat/sessions/:id/traces（追踪页自身轮询，占全部入口 span 的 29%），
	// 它与业务路由 /api/v1/chat/sessions/:id/messages 共享前缀 /api/v1/chat/sessions
	// —— 只要允许前缀匹配，一条配置就能把最大的噪声源放回来。
	// 没有 route 属性的根 span（后台任务）不受影响，仍按 OTelSamplingRate 采样。
	OTelBizRoutes []string `mapstructure:"otel_biz_routes"`

	// ── Langfuse（官方 eino callback v2，走平台 OTLP 入口）──
	//
	// 与上面的 OTel 段是两条互斥通路，别同时开：
	//   OTel 段     = 自研 span → 本进程 noop/stdout 出口（只服务本地调试）
	//   Langfuse 段 = 官方 eino callback → 平台 /api/public/otel/v1/traces
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
	// SampleRate 是官方 handler 自己的采样率，与自研轨的 SamplingRate 相互独立。
	LangfuseSampleRate float64 `mapstructure:"langfuse_sample_rate"`
	// Release / Tags 用于平台侧按版本、环境筛选。
	//
	// ⚠️ 这两个值必须由 WithTraceRoot 经 StartTrace 的选项一并下发：
	// 官方 StartTrace 是「opts 覆盖 handler 默认值」，只配在 handler 上会被顶掉。
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
// 判据「三项凭据齐全」只在这里定义一次 —— app.initLangfuseHandler（决定是否注册 handler）
// 与 recorder.WithTraceRoot（决定是否往 ctx 写 SetTrace 选项）必须用同一个判据，
// 否则会出现「handler 没注册、但每条请求都在写选项」或反过来的错位。
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
			Enabled:        true,
			TopK:           3,
			RecallK:        20,
			ScoreThreshold: 0.7,
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
			Enabled:              true,
			SamplingRate:         0.2,
			ErrorAlwaysSample:    true,
			SlowThresholdMs:      5000,
			FeedbackAlwaysSample: true,
			TraceTableEnabled:    true,
			ExportLogEnabled:     true,
			MetricsFormat:        "json",
			SinkBufferSize:       1024,
			SinkBatchSize:        50,
			SinkFlushIntervalMs:  200,
			PIIContentMaxChars:   200,
			PIIMaskSecret:        true,
			FeedbackEnabled:      true,
			MaxCardinalityLabels: 500,
			// OTel 默认值：noop 不打印 span，开发期可改 stdout 看 span 结构。
			// 这里没有 otlp 可选 —— 接三方平台请配下方的 langfuse_*。
			OTelExporter:     OTelExporterNoop,
			OTelServiceName:  "solvify-agent",
			OTelSamplingRate: 1.0,
			// 业务链路必留：这三条是「带完整 RAG / LLM 子树、用户真正会去查」的写接口。
			//
			// 为什么不把列表接口一起留下：实测 3 天 634 条 HTTP 入口 span 里，
			// 纯轮询类（追踪页 184 + 消息列表 55 + 会话列表 25 + 各类配置读取）
			// 占了七成以上，它们子树浅、数量大，是三方平台「trace 列表被刷屏」的唯一成因。
			// 留这三条即可把必留比例从 100% 压到约 22%，而问答链路一条不少。
			OTelBizRoutes: []string{
				"/api/v1/chat/sessions/:id/messages",
				"/api/v1/documents/:id/reindex",
				"/api/v1/chat/messages/:message_id/feedback",
			},
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
	// otel_exporter 的取值校验刻意放在 Enabled 之外：这个键决定的是「span 往哪儿去」，
	// 与自研轨开关无关。⚠️ 这里必须拒绝 otlp 而不是放行后降级 —— OTLP 出口已随
	// 「改用官方 callback」一并移除，老配置里残留的 otel_exporter: otlp 若只是
	// WARN + 回退 noop，表现就是「平台上一个 trace 都没有、进程里也不报错」。
	// 让启动直接失败，才是唯一看得见的方式。
	switch c.Observability.OTelExporter {
	case OTelExporterNoop, OTelExporterStdout, "":
	default:
		return fmt.Errorf("observability.otel_exporter 只支持 %s/%s（收到 %q）；"+
			"OTLP 出口已移除，三方链路请改用 langfuse_host / langfuse_public_key / langfuse_secret_key",
			OTelExporterNoop, OTelExporterStdout, c.Observability.OTelExporter)
	}

	if c.Observability.Enabled {
		if c.Observability.SamplingRate < 0 || c.Observability.SamplingRate > 1 {
			return errors.New("observability.sampling_rate 必须在 0 到 1 之间")
		}
		if c.Observability.SinkBufferSize <= 0 {
			return errors.New("observability.sink_buffer_size 必须大于 0")
		}
		if c.Observability.SinkBatchSize <= 0 {
			return errors.New("observability.sink_batch_size 必须大于 0")
		}
		if c.Observability.SinkFlushIntervalMs <= 0 {
			return errors.New("observability.sink_flush_interval_ms 必须大于 0")
		}
		if c.Observability.PIIContentMaxChars < 0 {
			return errors.New("observability.pii_content_max_chars 不能小于 0")
		}
		if c.Observability.MaxCardinalityLabels <= 0 {
			return errors.New("observability.max_cardinality_labels 必须大于 0")
		}
		switch c.Observability.MetricsFormat {
		case "json", "prometheus", "both", "none":
		default:
			return errors.New("observability.metrics_format 只支持 json/prometheus/both/none")
		}
		if c.Observability.OTelSamplingRate < 0 || c.Observability.OTelSamplingRate > 1 {
			return errors.New("observability.otel_sampling_rate 必须在 0 到 1 之间")
		}
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

	// Observability 配置
	if value := os.Getenv("OBSERVABILITY_ENABLED"); value != "" {
		cfg.Observability.Enabled = parseBool(value, cfg.Observability.Enabled)
	}
	if value := os.Getenv("OBSERVABILITY_SAMPLING_RATE"); value != "" {
		cfg.Observability.SamplingRate = parseFloat(value, cfg.Observability.SamplingRate)
	}
	if value := os.Getenv("OBSERVABILITY_ERROR_ALWAYS_SAMPLE"); value != "" {
		cfg.Observability.ErrorAlwaysSample = parseBool(value, cfg.Observability.ErrorAlwaysSample)
	}
	if value := os.Getenv("OBSERVABILITY_SLOW_THRESHOLD_MS"); value != "" {
		cfg.Observability.SlowThresholdMs = parseInt(value, cfg.Observability.SlowThresholdMs)
	}
	if value := os.Getenv("OBSERVABILITY_FEEDBACK_ALWAYS_SAMPLE"); value != "" {
		cfg.Observability.FeedbackAlwaysSample = parseBool(value, cfg.Observability.FeedbackAlwaysSample)
	}
	if value := os.Getenv("OBSERVABILITY_TRACE_TABLE_ENABLED"); value != "" {
		cfg.Observability.TraceTableEnabled = parseBool(value, cfg.Observability.TraceTableEnabled)
	}
	if value := os.Getenv("OBSERVABILITY_EXPORT_LOG_ENABLED"); value != "" {
		cfg.Observability.ExportLogEnabled = parseBool(value, cfg.Observability.ExportLogEnabled)
	}
	if v := os.Getenv("OBSERVABILITY_METRICS_FORMAT"); v != "" {
		cfg.Observability.MetricsFormat = v
	}
	if value := os.Getenv("OBSERVABILITY_SINK_BUFFER_SIZE"); value != "" {
		cfg.Observability.SinkBufferSize = parseInt(value, cfg.Observability.SinkBufferSize)
	}
	if value := os.Getenv("OBSERVABILITY_SINK_BATCH_SIZE"); value != "" {
		cfg.Observability.SinkBatchSize = parseInt(value, cfg.Observability.SinkBatchSize)
	}
	if value := os.Getenv("OBSERVABILITY_SINK_FLUSH_INTERVAL_MS"); value != "" {
		cfg.Observability.SinkFlushIntervalMs = parseInt(value, cfg.Observability.SinkFlushIntervalMs)
	}
	if value := os.Getenv("OBSERVABILITY_PII_CONTENT_MAX_CHARS"); value != "" {
		cfg.Observability.PIIContentMaxChars = parseInt(value, cfg.Observability.PIIContentMaxChars)
	}
	if value := os.Getenv("OBSERVABILITY_PII_MASK_SECRET"); value != "" {
		cfg.Observability.PIIMaskSecret = parseBool(value, cfg.Observability.PIIMaskSecret)
	}
	if value := os.Getenv("OBSERVABILITY_FEEDBACK_ENABLED"); value != "" {
		cfg.Observability.FeedbackEnabled = parseBool(value, cfg.Observability.FeedbackEnabled)
	}
	if value := os.Getenv("OBSERVABILITY_MAX_CARDINALITY_LABELS"); value != "" {
		cfg.Observability.MaxCardinalityLabels = parseInt(value, cfg.Observability.MaxCardinalityLabels)
	}

	// OTel 配置
	cfg.Observability.OTelExporter = getEnv("OTEL_EXPORTER", cfg.Observability.OTelExporter)
	cfg.Observability.OTelServiceName = getEnv("OTEL_SERVICE_NAME", cfg.Observability.OTelServiceName)
	if value := os.Getenv("OTEL_SAMPLING_RATE"); value != "" {
		cfg.Observability.OTelSamplingRate = parseFloat(value, cfg.Observability.OTelSamplingRate)
	}

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
