package middleware

import (
	"strings"

	"solvify-agent/pkg/jwt"
	"solvify-agent/pkg/response"

	"fmt"

	"github.com/gin-gonic/gin"
)

const (
	// ContextUserID 用户 ID 上下文键
	ContextUserID = "user_id"
	// ContextUsername 用户名上下文键
	ContextUsername = "username"
	// ContextUserRole 用户角色上下文键
	ContextUserRole = "role"
)

// Auth JWT 认证中间件
func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		var token string

		// 1. 尝试从 Header 获取 Authorization
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			// 简单处理：如果是 Bearer 开头，取后面部分；否则直接当作 Token
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && parts[0] == "Bearer" {
				token = parts[1]
			} else if len(parts) == 1 {
				// 有些客户端可能只发 Token 不发 Bearer 前缀
				token = authHeader
			}
		}

		// 2. 尝试从 Query 参数获取 (适配 WebSocket)
		if token == "" {
			token = c.Query("token")
		}

		// 3. 尝试从 Cookie 获取
		if token == "" {
			if cookieToken, err := c.Cookie("ACCESS_TOKEN"); err == nil {
				token = cookieToken
			}
		}

		if token == "" {
			// 如果是 WebSocket 连接，尝试打印一些调试信息
			if c.Request.Header.Get("Upgrade") == "websocket" {
				fmt.Printf("WS Auth Failed. Headers: %v\n", c.Request.Header)
			}
			response.Unauthorized(c, "请提供认证令牌")
			c.Abort()
			return
		}

		// 解析 Token
		claims, err := jwt.ParseToken(token)
		if err != nil {
			response.Unauthorized(c, err.Error())
			c.Abort()
			return
		}

		// 将用户信息存入上下文
		c.Set(ContextUserID, claims.GetUserID())
		c.Set(ContextUsername, claims.GetUsername())
		c.Set(ContextUserRole, claims.GetRole())

		c.Next()
	}
}

// OptionalAuth 可选 JWT 认证中间件
func OptionalAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 请求没有携带 Token 时继续放行，保证公开接口可匿名访问
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.Next()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.Next()
			return
		}

		// 2. 请求携带有效 Token 时写入用户上下文，供列表/详情接口返回真实交互状态
		// 3. 请求携带无效 Token 时也继续放行，由需要强登录的接口继续使用 Auth 严格拦截
		claims, err := jwt.ParseToken(parts[1])
		if err != nil {
			c.Next()
			return
		}

		c.Set(ContextUserID, claims.GetUserID())
		c.Set(ContextUsername, claims.GetUsername())
		c.Set(ContextUserRole, claims.GetRole())
		c.Next()
	}
}

// RequireRole 要求当前登录用户具备指定角色之一
func RequireRole(roles ...int) gin.HandlerFunc {
	allowed := make(map[int]struct{}, len(roles))
	for _, role := range roles {
		allowed[role] = struct{}{}
	}

	return func(c *gin.Context) {
		role := GetUserRole(c)
		if _, ok := allowed[role]; !ok {
			response.Forbidden(c, "无权限访问")
			c.Abort()
			return
		}

		c.Next()
	}
}

// GetUserID 从上下文获取用户 ID
func GetUserID(c *gin.Context) string {
	if userID, exists := c.Get(ContextUserID); exists {
		return userID.(string)
	}
	return ""
}

// GetUsername 从上下文获取用户名
func GetUsername(c *gin.Context) string {
	if username, exists := c.Get(ContextUsername); exists {
		return username.(string)
	}
	return ""
}

// GetUserRole 从上下文获取用户角色
func GetUserRole(c *gin.Context) int {
	if role, exists := c.Get(ContextUserRole); exists {
		return role.(int)
	}
	return -1
}

// RequireAdmin 要求当前登录用户是管理员
func RequireAdmin() gin.HandlerFunc {
	return RequireRole(2)
}
