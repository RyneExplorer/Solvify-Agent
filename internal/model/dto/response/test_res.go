package response

// TestResult 测试结果
type TestResult struct {
	Success                  bool   `json:"success"`
	Message                  string `json:"message"`
	Error                    string `json:"error,omitempty"`
	ResponseTime             int64  `json:"response_time_ms"`
	Details                  string `json:"details,omitempty"`
	DetectedMaxContextLength int    `json:"detected_max_context_length,omitempty"`
}
