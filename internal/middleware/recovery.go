package middleware

import (
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"solvify-agent/pkg/response"
)

// Recovery 创建 Gin panic 恢复中间件
func Recovery(log *zap.Logger) gin.HandlerFunc {
	if log == nil {
		log = zap.NewNop()
	}

	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				log.Error("请求发生 panic",
					zap.Any("error", err),
					zap.String("stack", string(debug.Stack())),
					zap.String("path", c.Request.URL.Path),
					zap.String("method", c.Request.Method),
					zap.String("ip", c.ClientIP()),
				)

				response.InternalError(c, "服务内部错误")
				c.Abort()
			}
		}()

		c.Next()
	}
}
