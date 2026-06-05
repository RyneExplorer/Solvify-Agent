package middleware

import (
	"github.com/gin-gonic/gin"
)

// FakeUser 返回一个注入假 user_id 的中间件（开发用）
// 优先从 X-User-ID 请求头读取，未传则默认 "1"
func FakeUser() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		userID := ctx.GetHeader("X-User-ID")
		if userID == "" {
			userID = "550e8400-e29b-41d4-a716-446655440000"
		}
		ctx.Set("user_id", userID)
		ctx.Next()
	}
}
