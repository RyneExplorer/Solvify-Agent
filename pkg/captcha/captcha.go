package captcha

import (
	"github.com/mojocn/base64Captcha"
	"image/color"
	"solvify-agent/pkg/logger"
)

// 使用默认的内存存储
var store = base64Captcha.DefaultMemStore

// GenerateCaptcha 生成图形验证码
// 返回: id, base64图片, 错误
func GenerateCaptcha() (string, string, error) {
	// 配置验证码参数: 高度40, 宽度120, 长度6, 干扰强度0, 噪点0
	// source: 数字+字母
	driver := &base64Captcha.DriverString{
		Height:          40,
		Width:           120,
		NoiseCount:      0,
		ShowLineOptions: 0, // HollowLine | SlimeLine
		Length:          4,
		Source:          "1234567890",
		BgColor: &color.RGBA{
			R: 255, G: 255, B: 255, A: 255,
		},
		Fonts: nil,
	}

	// 创建验证码实例
	c := base64Captcha.NewCaptcha(driver.ConvertFonts(), store)
	// 生成验证码
	id, b64s, err := c.Generate()
	return id, b64s, err
}

// Verify 验证验证码
// id: 验证码ID
// value: 用户输入的验证码
// 返回: 是否验证成功
func Verify(id, value string) bool {
	logger.Debugf("【验证码验证】接收到参数：id=[%s](长度%d), value=[%s](长度%d)",
		id, len(id), value, len(value))

	if id == "" || value == "" {
		logger.Warn("【验证码无效】原因：id或value为空")
		return false
	}

	ok := store.Verify(id, value, true)
	if !ok {
		logger.Warn("【验证码无效】原因：store验证失败（ID不存在/值不匹配/已过期/已被删除）")
	} else {
		logger.Info("【验证码验证】成功！！！")
	}
	return ok
}
