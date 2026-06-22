package middleware

import (
	"context"
	"regexp"
	"strings"

	"solvify-agent/pkg/jwt"
	"solvify-agent/pkg/response"

	"fmt"

	"github.com/gin-gonic/gin"
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const (
	// ContextUserID 用户 ID 上下文键
	ContextUserID = "user_id"
	// ContextUsername 用户名上下文键
	ContextUsername = "username"
	// ContextUserRole 用户角色上下文键
	ContextUserRole = "role"
	// ContextToken 当前请求认证令牌上下文键
	ContextToken = "token"
)

// TokenRevoker 校验令牌是否已经失效
type TokenRevoker interface {
	IsTokenRevoked(ctx context.Context, token string) (bool, error)
}

// Auth JWT 认证中间件
func Auth(revoker TokenRevoker) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := ExtractToken(c)

		if token == "" {
			// 如果是 WebSocket 连接，尝试打印一些调试信息
			if c.Request.Header.Get("Upgrade") == "websocket" {
				fmt.Printf("WS Auth Failed. Headers: %v\n", c.Request.Header)
			}
			response.Unauthorized(c, "请提供认证令牌")
			c.Abort()
			return
		}

		if revoker != nil {
			revoked, err := revoker.IsTokenRevoked(c.Request.Context(), token)
			if err != nil {
				response.InternalError(c, "认证状态校验失败")
				c.Abort()
				return
			}
			if revoked {
				response.Unauthorized(c, "token 已失效")
				c.Abort()
				return
			}
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
		c.Set(ContextToken, token)

		c.Next()
	}
}

// OptionalAuth 可选 JWT 认证中间件
func OptionalAuth(revoker TokenRevoker) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := ExtractToken(c)
		if token == "" {
			c.Next()
			return
		}

		// 2. 请求携带有效 Token 时写入用户上下文，供列表/详情接口返回真实交互状态
		// 3. 请求携带无效 Token 时也继续放行，由需要强登录的接口继续使用 Auth 严格拦截
		if revoker != nil {
			revoked, err := revoker.IsTokenRevoked(c.Request.Context(), token)
			if err != nil || revoked {
				c.Next()
				return
			}
		}

		claims, err := jwt.ParseToken(token)
		if err != nil {
			c.Next()
			return
		}

		c.Set(ContextUserID, claims.GetUserID())
		c.Set(ContextUsername, claims.GetUsername())
		c.Set(ContextUserRole, claims.GetRole())
		c.Set(ContextToken, token)
		c.Next()
	}
}

// ExtractToken 从请求中读取认证令牌
func ExtractToken(c *gin.Context) string {
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && parts[0] == "Bearer" {
			return strings.TrimSpace(parts[1])
		}
		if len(parts) == 1 {
			return strings.TrimSpace(authHeader)
		}
	}

	if token := strings.TrimSpace(c.Query("token")); token != "" {
		return token
	}

	if cookieToken, err := c.Cookie("ACCESS_TOKEN"); err == nil {
		return strings.TrimSpace(cookieToken)
	}

	return ""
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

// GetToken 从上下文获取当前认证令牌
func GetToken(c *gin.Context) string {
	if token, exists := c.Get(ContextToken); exists {
		return token.(string)
	}
	return ""
}

// CurrentUserID 从 JWT 认证上下文中读取当前用户 ID
func CurrentUserID(c *gin.Context) (string, bool) {
	userID := GetUserID(c)
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
