package v1

import (
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"solvify-agent/internal/middleware"
	"solvify-agent/pkg/response"
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// CurrentUserID 从 JWT 认证上下文中读取当前用户 ID
func CurrentUserID(c *gin.Context) (string, bool) {
	userID := middleware.GetUserID(c)
	if userID == "" {
		response.Unauthorized(c, "未登录或 Token 已过期")
		return "", false
	}
	if !IsUUID(userID) {
		response.BadRequest(c, "用户 ID 格式错误")
		return "", false
	}
	return userID, true
}

// IsUUID 判断字符串是否为 UUID 格式
func IsUUID(value string) bool {
	return uuidPattern.MatchString(strings.TrimSpace(value))
}
