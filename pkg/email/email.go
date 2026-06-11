package email

import (
	"go.uber.org/zap"
	"gopkg.in/gomail.v2"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// SendEmail 发送邮件
func SendEmail(to, subject, body string) error {
	cfg := config.Get().Email
	m := gomail.NewMessage()
	// 发件人
	m.SetHeader("From", cfg.Username)
	// 收件人
	m.SetHeader("To", to)
	// 邮件主题
	m.SetHeader("Subject", subject)
	// 邮件内容格式: HTML
	m.SetBody("text/html", body)

	dialer := gomail.NewDialer(cfg.Host, cfg.Port, cfg.Username, cfg.Password)
	err := dialer.DialAndSend(m)
	if err != nil {
		logger.Error("发送验证码失败:", zap.Error(err))
	}
	return err
}
