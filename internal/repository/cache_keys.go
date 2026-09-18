package repository

import "fmt"

// ─── 仓库层 Redis 缓存 key 的**唯一**来源（P1-5）─────────────────────────────
//
// 本文件只写 key 的「形态」部分；**实体前缀由 app.go 的 cache.New 给定**
// （`model:` / `user:model:config:` / `tool:type:` / `tool:config:`），
// 不要在这里手拼实体前缀 —— 曾经出现过 `user:model:user:model:<id>` 这种双重前缀。
//
// 为什么要把它们收在一个文件里：
//
//	同一个 key 以前在「读」和「每一个失效点」各手写一遍（tool_type 一个文件里就写了 7 遍）。
//	key 格式改一处、漏一处，被漏掉的那条索引会一直返回脏值直到 TTL 过期，**而且不报错**——
//	这正是 P0-5「改名后旧 tool_key 索引没删」的失效模式，也是 d57b01b「缓存 key 必须从
//	创建对象的入参派生」要治的同一类问题：**同一个东西有两个来源**。
//
//	现在读写两侧都只能调下面的函数；cache_key_guard_test.go 会扫描本包的**非测试文件**，
//	除本文件外出现 `id:` / `key:` / `user:` 形状的字符串字面量即红灯。
//
// ⚠️ 改这里的返回值 = 改线上 Redis 的 key 命名空间：老 key 会在 TTL（10 分钟 / 24 小时）
// 内自然过期，期间表现为一次 cache miss（落库回填），不会产生正确性问题，但必须是有意为之。
// 命名空间现状由 TestCacheKeyShapes 钉住。
//
// ⚠️ 这个文件治的是「**格式漂移**」：读用 A 形态、失效删 B 形态，或两处各抄一份 key 拼法。
// 它治不了「**入参拿错**」——比如 Update 里用 model.ModelID 去失效、而读路径用的是 model.ID：
// 两侧都调同一个构造函数，格式完全一致，但删的是另一个 key。那种偏离由
// cache_key_single_source_test.go 的行为用例兜（「读进去的 key，写出来必须删得掉」）。
// 两条防线各管一类，缺一个都会留下盲区。

// modelCacheKeyByID 系统模型：按主键查
func modelCacheKeyByID(id string) string { return "id:" + id }

// userModelConfigCacheKeyByID 用户模型配置：按（主键, 归属用户）查
func userModelConfigCacheKeyByID(id, userID string) string {
	return fmt.Sprintf("id:%s:%s", id, userID)
}

// toolTypeCacheKeyByID 工具类型：按主键查
func toolTypeCacheKeyByID(id string) string { return "id:" + id }

// toolTypeCacheKeyByToolKey 工具类型：按 tool_key 查（独立索引）。
// ⚠️ 它是一份**独立**索引，所以任何写操作都必须同时失效本形态与 toolTypeCacheKeyByID。
func toolTypeCacheKeyByToolKey(toolKey string) string { return "key:" + toolKey }

// userToolConfigCacheKeyByID 用户工具配置：按主键查
func userToolConfigCacheKeyByID(id string) string { return "id:" + id }

// userToolConfigCacheKeyByUser 用户工具配置：按用户查「已启用列表」。
// ⚠️ 按用户维度的读最容易被漏失效：只要任何一个用户的配置行发生增删改，就必须删这个 key。
func userToolConfigCacheKeyByUser(userID string) string { return "user:" + userID }
