package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMessageEmbeddingPolicyLoadPartialAndLegacy(t *testing.T) {
	for _, tc := range []struct {
		body        string
		concurrency int
	}{
		{"embedding:\n  model: test\n", 2},
		{"embedding:\n  message_jobs:\n    concurrency: 4\n", 4},
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		want := DefaultMessageEmbeddingConfig()
		want.Concurrency = tc.concurrency
		if cfg.Embedding.MessageJobs != want {
			t.Fatalf("配置缺省或覆盖未生效: %+v", cfg.Embedding.MessageJobs)
		}
	}
}

func TestMessageEmbeddingPolicyBounds(t *testing.T) {
	if err := Default().Embedding.MessageJobs.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*MessageEmbeddingConfig){
		func(c *MessageEmbeddingConfig) { c.Concurrency = 0 }, func(c *MessageEmbeddingConfig) { c.Concurrency = 17 },
		func(c *MessageEmbeddingConfig) { c.TimeoutSeconds = 301 }, func(c *MessageEmbeddingConfig) { c.PollSeconds = 0 },
		func(c *MessageEmbeddingConfig) { c.MaxAttempts = 11 }, func(c *MessageEmbeddingConfig) { c.RetrySeconds = 301 },
	} {
		c := DefaultMessageEmbeddingConfig()
		mutate(&c)
		if c.Validate() == nil {
			t.Fatalf("无效配置通过: %+v", c)
		}
	}
}
