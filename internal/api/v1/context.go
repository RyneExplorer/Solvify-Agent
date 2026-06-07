package v1

import (
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"solvify-agent/pkg/response"
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// CurrentUserID 从请求头读取当前用户 ID
func CurrentUserID(c *gin.Context) (string, bool) {
	userID := strings.TrimSpace(c.GetHeader("X-User-ID"))
	if userID == "" {
		response.BadRequest(c, "X-User-ID 不能为空")
		return "", false
	}
	if !IsUUID(userID) {
		response.BadRequest(c, "X-User-ID 格式错误")
		return "", false
	}
	return userID, true
}

// IsUUID 判断字符串是否为 UUID 格式
func IsUUID(value string) bool {
	return uuidPattern.MatchString(strings.TrimSpace(value))
}
