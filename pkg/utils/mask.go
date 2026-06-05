package utils

// MaskAPIKey 对 APIKey 进行脱敏处理
func MaskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "***"
	}
	return key[:4] + "***" + key[len(key)-4:]
}

// IsMaskedAPIKey 判断是否是脱敏格式的 APIKey
func IsMaskedAPIKey(key string) bool {
	return len(key) >= 3 && key[len(key)-3:] == "***" && len(key) > 3
}
