package llm

// ManagedOutputLimit 是项目调用策略，不是服务商能力默认值。
// 留出至少四分之三窗口供指令、工具和输入；缺少能力时仍拒绝调用。
func ManagedOutputLimit(window, maxOutput int) int {
	if window <= 0 || maxOutput <= 0 {
		return 0
	}
	return min(8192, maxOutput, window/4)
}
