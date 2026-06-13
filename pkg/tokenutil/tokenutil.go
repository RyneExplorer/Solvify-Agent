package tokenutil

// Estimate 估算文本的 token 数量
// 中文字符约 1.5 token/字，英文约 0.25 token/字符
// 统一用 ~2 字符/token 估算，对中英混合场景误差可接受
func Estimate(text string) int {
	runes := []rune(text)
	return (len(runes) + 1) / 2
}
