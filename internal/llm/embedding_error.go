package llm

// EmbeddingError 暴露脱敏原因和重试判据，不保留可能带密钥的上游错误字符串。
type EmbeddingError struct {
	Code      string
	Message   string
	Temporary bool
}

func (e *EmbeddingError) Error() string   { return e.Message }
func (e *EmbeddingError) Retryable() bool { return e.Temporary }
