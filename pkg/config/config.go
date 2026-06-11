package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/goccy/go-yaml"
	"github.com/mitchellh/mapstructure"
)

const defaultConfigPath = "configs/config.yaml"

// Config 描述应用全局配置
type Config struct {
	App       AppConfig       `mapstructure:"app"`
	Log       LogConfig       `mapstructure:"log"`
	Agent     AgentConfig     `mapstructure:"agent"`
	LLM       LLMConfig       `mapstructure:"llm"`
	Embedding EmbeddingConfig `mapstructure:"embedding"`
	RAG       RAGConfig       `mapstructure:"rag"`
	Tools     ToolsConfig     `mapstructure:"tools"`
	Server    ServerConfig    `mapstructure:"server"`
	Database  DatabaseConfig  `mapstructure:"database"`
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

// AgentConfig 描述 Agent 行为开关
type AgentConfig struct {
	EnableDemo     bool    `mapstructure:"enable_demo"`
	MaxIterations  int     `mapstructure:"max_iterations"`
	ScoreThreshold float64 `mapstructure:"score_threshold"`
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
}

// RAGConfig 描述检索增强配置
type RAGConfig struct {
	Enabled        bool    `mapstructure:"enabled"`
	TopK           int     `mapstructure:"top_k"`
	ScoreThreshold float64 `mapstructure:"score_threshold"`
	VectorWeight   float64 `mapstructure:"vector_weight"`
	KeywordWeight  float64 `mapstructure:"keyword_weight"`
	RRFK           float64 `mapstructure:"rrf_k"`
}

// ToolsConfig 描述工具调用配置
type ToolsConfig struct {
	Enabled bool `mapstructure:"enabled"`
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
			EnableDemo:     true,
			MaxIterations:  10,
			ScoreThreshold: 0.7,
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
			Model:     "text-embedding-3-small",
			Dimension: 1536,
		},
		RAG: RAGConfig{
			Enabled:        true,
			TopK:           3,
			ScoreThreshold: 0.7,
		},
		Tools: ToolsConfig{
			Enabled: true,
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
	if value := os.Getenv("EMBEDDING_DIMENSION"); value != "" {
		cfg.Embedding.Dimension = parseInt(value, cfg.Embedding.Dimension)
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
	if value := os.Getenv("POSTGRES_ENABLE_PGVECTOR"); value != "" {
		cfg.Database.Postgres.EnablePGVector = parseBool(value, cfg.Database.Postgres.EnablePGVector)
	}
	if value := os.Getenv("TOOLS_ENABLED"); value != "" {
		cfg.Tools.Enabled = parseBool(value, cfg.Tools.Enabled)
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
}

// getEnv 读取环境变量并在为空时返回默认值
func getEnv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
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
