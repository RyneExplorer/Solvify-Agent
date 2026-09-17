package service

// maskAPIKey 对 API Key 做脱敏，供响应 DTO 使用。
//
// 只保留头部 3 位与尾部 4 位，中间固定用 **** 顶替，让用户仍能分辨
// 「配的是哪一把密钥」，同时又无法据此还原出可用凭据。
//
// 注意：这只负责「体面地少暴露一点」，不等于凭据保护。真正的修法是让
// API Key 不再原样透传到响应体里 —— 所以本函数只该出现在响应构造路径上，
// 内部解析凭据（如 chat_service 走 modelRepo 取 entity）绝不能经过它。
func maskAPIKey(key string) string {
	// 空值原样返回：让「未配置」与「配置了但被隐藏」在前端可区分。
	if key == "" {
		return ""
	}
	// 长度不足 8 位时首尾区间会重叠，直接整体隐藏，避免把短密钥整个拼出来。
	if len(key) < 8 {
		return "****"
	}
	return key[:3] + "****" + key[len(key)-4:]
}
