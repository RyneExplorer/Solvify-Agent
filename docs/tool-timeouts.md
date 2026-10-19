# 工具执行时限

开发约束见[AGENTS.md](../AGENTS.md)，MCP配置与升级步骤见[MCP配置](mcp-configuration.md)。通用工具供应商框架已移除；外部能力统一通过MCP配置。

## 配置归属

管理员在「后台管理 → MCP预置 → 添加/编辑」设置连接初始化超时和单次调用超时，保存于`mcp_templates.connection` 的 `connect_timeout`、`call_timeout`。个人HTTP MCP在用户设置的高级配置中保存于 `mcp_connections.connection`；用户不能覆盖所绑定预置的管理员时限。

两项均为1～86400秒整数，留空分别使用30秒、300秒。连接时限覆盖Start与Initialize握手；调用时限覆盖tools/list与每次tools/call，分别计时。旧 `timeout` 只兼容读取：新字段优先，再回退旧值，最后使用默认值；编辑旧配置回显旧值，新保存转为两个独立字段。

部署预置由YAML维护连接地址、认证模板及字段。后台允许仅调整超时，后端拒绝混入连接信息；清空后保存明确的30/300秒，以 `admin_timeouts` 标记管理员归属。启动同步保留该超时，也保留停用状态。

内部知识检索、文档工具及 `read_context_artifact` 由程序注册，统一使用系统配置：

```yaml
tools:
  default_timeout: 30
```

允许1～86400秒，环境变量 `TOOLS_DEFAULT_TIMEOUT` 优先，修改后重启生效。上述时限均不替代Agent运行预算，也不修改模型调用策略。

## 执行行为

内置工具通过Eino中间件建立执行context，MCP适配器为真实工具调用建立context。审批与澄清位于外层，通过后才消耗工具执行时限；更短父deadline和父取消优先。即使底层把超时错误转换成JSON，执行边界仍检查context，不把超时当成功。

不在执行适配层自动重试副作用请求；超时可能发生在外部已经执行之后，不能据此宣称操作未发生。依赖下游遵守context，不以后台goroutine伪装强制停止。

共享初始化由连接池生命周期管理：单个等待者取消不取消其他等待者，初始化仍受配置时限约束；关闭池取消并清理初始化。配置的连接时限不再受固定60秒截断。移除了无用户绑定的共享模板启动预热，避免无凭据握手及无法复用的会话。

连接/凭据修改在下一次装配时进入新配置，实际调用前重新检查连接及模板状态；已构建的旧工具若配置变化会明确拒绝并要求重新发起。只修改调用时限可以复用底层连接，但新的工具实例使用新时限。

## 验证

```shell
go test ./...
go vet ./...
go test -race ./internal/agent ./internal/service ./internal/tool/... ./internal/api/v1/mcp ./internal/repository ./internal/app ./pkg/config -count=1 -timeout 300s
```

本地JSON-RPC服务器与真实MCP SDK验证初始化、发现及调用认证，61秒握手在65秒配置下成功，单次调用超时、父deadline、共享取消隔离和关闭池清理；Agent验证审批边界。前端在 `design/vue` 运行 `node --test tests/*.test.mjs` 和 `node node_modules/vite/bin/vite.js build`，覆盖MCP表单保存、真实凭据探测、超时回显、字段schema保留与API路由。认证测试使用本地受控服务；第三方只读MCP与浏览器验收见[MCP配置](mcp-configuration.md)，未执行真实外部副作用。
