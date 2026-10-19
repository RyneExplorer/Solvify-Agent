package config

import "fmt"

// MessageEmbeddingConfig 限制后台消息向量任务；enable_pgvector决定是否登记及运行。
type MessageEmbeddingConfig struct {
	Concurrency    int `mapstructure:"concurrency"`
	PollSeconds    int `mapstructure:"poll_seconds"`
	TimeoutSeconds int `mapstructure:"timeout_seconds"`
	MaxAttempts    int `mapstructure:"max_attempts"`
	RetrySeconds   int `mapstructure:"retry_seconds"`
}

func DefaultMessageEmbeddingConfig() MessageEmbeddingConfig {
	return MessageEmbeddingConfig{Concurrency: 2, PollSeconds: 2, TimeoutSeconds: 10, MaxAttempts: 3, RetrySeconds: 15}
}

func (c MessageEmbeddingConfig) Validate() error {
	if c.Concurrency < 1 || c.Concurrency > 16 || c.PollSeconds < 1 || c.PollSeconds > 60 || c.TimeoutSeconds < 1 || c.TimeoutSeconds > 300 || c.MaxAttempts < 1 || c.MaxAttempts > 10 || c.RetrySeconds < 1 || c.RetrySeconds > 300 {
		return fmt.Errorf("消息向量任务配置无效：并发1–16，轮询1–60秒，超时1–300秒，尝试1–10次，退避1–300秒")
	}
	return nil
}
