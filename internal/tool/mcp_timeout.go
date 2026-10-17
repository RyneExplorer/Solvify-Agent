package tool

import (
	"fmt"
	"time"
)

func (cfg *MCPProviderConfig) ValidateTimeouts() error {
	if cfg.Timeout < 0 || cfg.Timeout > 86400 {
		return fmt.Errorf("MCP旧超时时间须为0～86400秒")
	}
	for _, field := range []struct {
		name    string
		seconds *int
	}{
		{"连接初始化", cfg.ConnectTimeout}, {"单次调用", cfg.CallTimeout},
	} {
		if field.seconds != nil && (*field.seconds < 1 || *field.seconds > 86400) {
			return fmt.Errorf("MCP%s超时时间须为1～86400秒，或留空使用默认值", field.name)
		}
	}
	return nil
}

func (cfg *MCPProviderConfig) ConnectionTimeout() time.Duration {
	return cfg.timeoutValue(cfg.ConnectTimeout, 30)
}

func (cfg *MCPProviderConfig) ToolCallTimeout() time.Duration {
	return cfg.timeoutValue(cfg.CallTimeout, 300)
}

func (cfg *MCPProviderConfig) timeoutValue(explicit *int, fallback int) time.Duration {
	seconds := fallback
	if cfg.Timeout > 0 {
		seconds = cfg.Timeout
	}
	if explicit != nil {
		seconds = *explicit
	}
	return time.Duration(seconds) * time.Second
}
