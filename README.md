# Solvify-Agent

Solvify-Agent 是一个基于 Go + Gin + Eino 的可运行 Agent 项目初始化框架，内置知识助理Demo，覆盖用户提问、RAG 模拟检索、Tool 模拟调用、LLM 封装、统一响应、业务错误码、zap 日志和优雅关闭链路。

## 目录结构

```text
.
├── cmd/server/main.go
├── config/config.yaml
├── docs/architecture.md
├── internal
│   ├── agent
│   │   ├── knowledge.go
│   │   └── knowledge_test.go
│   ├── api
│   │   ├── router.go
│   │   └── router_test.go
│   ├── app
│   │   └── app.go
│   ├── llm
│   │   └── client.go
│   ├── middleware
│   │   ├── logger.go
│   │   └── recovery.go
│   ├── rag
│   │   ├── memory.go
│   │   └── memory_test.go
│   ├── service
│   │   └── chat.go
│   └── tool
│       ├── calculator.go
│       ├── calculator_test.go
│       └── tool.go
├── pkg
│   ├── config
│   │   └── config.go
│   ├── errors
│   │   ├── code.go
│   │   ├── errors.go
│   │   └── errors_test.go
│   ├── logger
│   │   └── logger.go
│   └── response
│       ├── response.go
│       └── response_test.go
├── .config.yaml.example
├── .env.example
├── go.mod
└── README.md
```

## 启动链路

1. `cmd/server` 创建 `internal/app.App`
2. `App.Initialize` 加载 `pkg/config.Config`，并把配置保存在 App 结构体中
3. `App` 初始化 zap 日志、RAG、Tool、LLM、Service 和 Gin Router
4. Gin 注册 `/health` 和 `/api/v1/ask`
5. `internal/agent` 编排 RAG、Tool 和 LLM，返回最终答案

## 快速启动

```powershell
go mod tidy
go test ./...
go run ./cmd/server
```

## 接口测试

健康检查：

```powershell
curl http://localhost:8080/health
```

知识助理问答：

```powershell
curl -X POST http://localhost:8080/api/v1/ask `
  -H "Content-Type: application/json" `
  -d "{\"question\":\"工具调用要注意什么？另外计算 1 + 2\",\"use_rag\":true,\"use_tools\":true}"
```

## 统一响应

所有 API 返回统一结构：

```json
{
  "code": 0,
  "message": "成功",
  "data": {}
}
```

错误响应示例：

```json
{
  "code": 2001,
  "message": "问题不能为空"
}
```

## 错误码

| 错误码 | 说明 |
| --- | --- |
| `0` | 成功 |
| `400` | 请求参数错误 |
| `500` | 服务内部错误 |
| `2001` | 问题不能为空 |
| `2002` | 参数格式错误 |
| `3001` | RAG 检索失败 |
| `3002` | RAG 未命中 |
| `4001` | 工具不存在 |
| `4002` | 工具参数错误 |
| `4003` | 工具调用失败 |
| `5001` | LLM 调用失败 |
| `5002` | Agent 执行失败 |
| `5003` | Agent 执行超时 |

## 配置说明

默认配置文件是 `config/config.yaml`。也可以复制根目录示例文件，并通过 `CONFIG_PATH` 指定：

```powershell
Copy-Item .config.yaml.example .config.yaml
$env:CONFIG_PATH=".config.yaml"
go run ./cmd/server
```

当前服务配置统一放在 `server` 下：

```yaml
server:
  host: ""
  port: 8080
  shutdown_timeout_seconds: 10
```

配置优先级：

```text
代码默认值 < 配置文件 < 环境变量
```

常用环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `CONFIG_PATH` | `config/config.yaml` | 配置文件路径 |
| `APP_ENV` | `development` | 应用环境 |
| `APP_MODE` | `release` | Gin 运行模式 |
| `SERVER_HOST` | 空 | HTTP 监听主机，空值表示监听所有地址 |
| `SERVER_PORT` | `8080` | HTTP 监听端口 |
| `SHUTDOWN_TIMEOUT_SECONDS` | `10` | 优雅关闭最长等待秒数 |
| `LOG_LEVEL` | `info` | 日志级别 |
| `LOG_FILENAME` | `logs/solvify-agent.log` | 日志文件路径 |
| `LLM_PROVIDER` | `mock` | 模型提供方 |
| `LLM_MODEL` | `mock-knowledge-assistant` | 模型名称 |
| `RAG_ENABLED` | `true` | RAG 默认开关 |
| `TOOLS_ENABLED` | `true` | Tool 默认开关 |

## 日志

项目使用 `zap + lumberjack`：

- 控制台和文件双输出
- JSON 格式
- 支持 caller 和 error stacktrace
- 支持日志文件轮转
- 首次启动会自动创建 `logs` 目录

## Gin 使用说明

项目路由层统一使用 Gin：

- `internal/api/router.go` 创建 `gin.Engine`
- `internal/middleware/logger.go` 处理请求日志
- `internal/middleware/recovery.go` 处理 panic 恢复

`net/http` 只用于底层 `http.Server` 承载 Gin Engine，以及测试中的 `httptest`。

## 生产化扩展点

- 将 `internal/llm.MockClient` 替换为真实 Eino ChatModel 调用
- 将 `internal/rag.MemoryRetriever` 替换为向量数据库检索
- 将 `internal/tool.Calculator` 扩展为 Tool Registry、权限控制和超时控制
- 在 `internal/service` 增加会话、用户鉴权、配额和审计
- 在 `internal/app` 增加指标、链路追踪和外部依赖生命周期管理
