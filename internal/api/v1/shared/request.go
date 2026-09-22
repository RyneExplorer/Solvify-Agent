// Package shared 提供 v1 各模块 controller 共用的请求解析工具。
//
// 之所以独立成包而不是放进 internal/api：后者的 package api 会导入各 v1 模块，
// 若模块再反向导入它就会形成循环依赖。v1 各模块之间互不导入，放在 v1 下最合适。
package shared

import (
	"github.com/gin-gonic/gin"

	"solvify-agent/internal/middleware"
	"solvify-agent/pkg/response"
)

// UUIDParam 把路径参数 param 校验为 UUID。
// 校验失败时已写好 400 响应，返回 ok=false 表示调用方应立即 return。
// label 用于拼装提示语，最终形如「知识库 ID 格式错误」。
func UUIDParam(c *gin.Context, param, label string) (string, bool) {
	value := c.Param(param)
	if !middleware.IsUUID(value) {
		response.BadRequest(c, label+" ID 格式错误")
		return "", false
	}
	return value, true
}

// UserAndUUIDParam 读取当前登录用户，并把路径参数 param 校验为 UUID。
// 失败时（未登录 / 用户 ID 非法 / 路径参数非 UUID）已写好响应，
// 返回 ok=false 表示调用方应立即 return。
func UserAndUUIDParam(c *gin.Context, param, label string) (userID, value string, ok bool) {
	userID, ok = middleware.CurrentUserID(c)
	if !ok {
		return "", "", false
	}
	value, ok = UUIDParam(c, param, label)
	if !ok {
		return "", "", false
	}
	return userID, value, true
}

// 注意：这里**刻意不提供** BindJSON / BindQuery 之类的绑定包装。
//
// c.ShouldBindJSON 一次调用同时做两件事：① JSON 解码 ② binding:"required,email" 这类字段校验。
// 各接口正是利用这条错误路径给出**业务语义**文案（如「邮箱格式错误!」「标题和内容不能为空」），
// 而这些文案会被前端直接展示（design/vue/src/api/client.ts 读 data.message）。
// 统一成一句「请求参数错误」会把两类失败混为一谈、并抹掉这些提示，
// 而封装本身每处只省 1 行（4 行 → 3 行）。结论：不要包装框架自带的一行惯用法。
