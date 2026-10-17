# 工具执行时限

开发约束见[AGENTS.md](../AGENTS.md)。HTTP与MCP时限使用现有供应商编辑框和创建/更新接口保存，没有独立时限策略页面、路由或数据表。Agent整次任务总时限暂未实施；本次不修改模型超时策略。

## 管理入口与配置归属

管理员打开「后台管理 → 工具管理 → 供应商 → 添加/编辑」：

- HTTP配置增加“单次执行超时（秒，可选）”，保存在既有 `tool_providers.provider_config` JSON的 `timeout` 字段，允许1～86400秒整数。留空删除该字段，沿用系统 `tools.default_timeout`。
- MCP分别显示“连接初始化超时”和“单次工具调用超时”，保存到 `provider_config.mcp.connect_timeout`、`call_timeout`，允许1～86400秒整数。连接时限覆盖Start与Initialize握手；调用时限覆盖tools/list和每次tools/call。两项分别计时，连接时限不限制后续工具执行。
- 新建配置留空分别使用30秒、300秒；清空并保存恢复各自默认值。旧JSON的 `timeout` 保留读取兼容：新字段优先，未填写的新字段回退旧timeout，再回退30/300秒。编辑旧配置时两个输入框回显旧值，保存转为新字段且不再写旧timeout，不需要批量迁移。
- 系统预置MCP也可通过原“编辑”按钮调整两个时限，前端只提交超时字段，后端拒绝借此修改地址、凭据、类型及未知配置。清空保存物化为30/300秒，并以MCP JSON内 `admin_timeouts` 标记管理员归属；启动同步优先保留这些时限，其他连接信息继续使用YAML。管理员保存后，后续YAML超时修改不覆盖该记录，可再次通过管理页调整。

不同供应商分别保存各自时限，无需新增API字段；请求仍使用 `provider_config`。接口复用 `POST /api/v1/admin/tool-types/:id/providers` 和 `PUT /api/v1/admin/tool-types/:id/providers/:providerId`。前后端均校验时限，普通用户和模型工具参数不能覆盖管理员值。

内部知识检索、文档工具及 `read_context_artifact` 没有供应商编辑记录，统一使用系统配置：

```yaml
tools:
  default_timeout: 30
```

允许1～86400秒，环境变量 `TOOLS_DEFAULT_TIMEOUT` 优先，修改系统配置后重启生效。默认HTTP也使用此值；填写供应商时限后优先使用供应商值。HTTP和MCP管理保存后，新一轮执行重新从数据库加载供应商关联，不等待用户配置Redis缓存过期；已经构建的在途执行沿用已取得的配置。

## 执行行为

内置工具通过Eino工具调用中间件建立执行context，HTTP工具在Provider入口建立context，覆盖建连、等待响应及完整响应体读取。HTTP固定15秒完整请求上限已移除，TLS局部10秒保护保留。复用现有Eino组件和HTTP Transport。

快速、普通深度、PlanExecute专业子Agent均接入；审批和澄清等待在外层，真正放行执行才计时。更短的父deadline和父取消继续生效。没有新增Agent总deadline，单次工具限制不能保证整次多步任务在固定时间内结束。

超时会取消下游IO并返回失败，即使文档工具把底层错误转成JSON，执行边界仍检查 `ctx.Err()`。不在此层自动重试；副作用超时提示结果需要核实，不能宣称外部操作未发生。下游必须遵守context，没有使用后台goroutine伪装已停止；自动对账、补偿及独立未知结果状态不在本次范围。

已有HTTP/MCP测试入口与管理表单使用同一配置构建逻辑，MCP探测与tools/list也使用调用时限。连接池原固定60秒初始化截断已移除，外层兜底和底层握手共用配置的连接deadline；例如设置65秒，可等待61秒握手成功。

共享初始化由连接池生命周期管理：单个请求取消或更短的父deadline结束该等待者，不取消其他等待者；共享初始化仍受连接时限约束，关闭池会主动取消初始化并清理客户端。修改连接时限会使用新的连接缓存键，避免命中旧初始化/失败缓存；只修改调用时限仍复用连接，并在下一轮构建工具时使用新值。后台预热整体60秒是预热任务预算，取消预热等待不改变共享连接的配置时限；不属于请求路径的固定初始化上限。

## 发布与验证

无需新增数据库表、列或迁移，部署更新后的前后端即可。此前独立策略方案的 `tool_timeout_policies`、迁移SQL、管理组件与GET/PUT策略路由均已从本次代码移除；该方案没有迁移到应用数据库，因此无需执行删表或数据回滚。

测试覆盖现有供应商创建/更新保存HTTP及MCP时限、非法值拒绝、清空恢复默认、管理员权限和原路由错误行为；独立策略路由确认404。前端执行管理页真实表单编解码函数与API函数，验证30秒显式提交、保存回显、旧配置留空及输入校验。ToolFactory验证管理员配置进入实际HTTP参数及MCP调用deadline，新一轮读取更新值。系统预置测试验证仅允许更新超时、清空恢复默认，以及启动配置合并保留管理员时限；使用模拟仓库和配置重建，未对运行数据库进行修改。

本地HTTP服务验证慢响应头、响应体停滞、上游取消/短deadline、管理员覆盖与用户参数不可覆盖；实际16秒响应在25秒供应商时限下成功。真实Eino ToolsNode覆盖内部取消、审批顺序和文档工具流式错误传播。未调用真实供应商、未做浏览器端到端或外部副作用验收，未重启运行服务。

验证命令：

```shell
go test ./...
go vet ./...
go test -race ./internal/agent ./internal/service ./internal/tool/... ./internal/api/v1/tool ./internal/app ./pkg/config -count=1 -timeout 240s
```

MCP专项使用本地JSON-RPC服务和真实SDK验证初始化超时、61秒握手成功、tools/list超时、真实Factory构建后的tools/call超时、父deadline、共享初始化取消隔离与关闭池取消。未调用真实供应商或验证浏览器端到端。

前端在 `design/vue` 运行 `node --test tests/*.test.mjs` 和 `node node_modules/vite/bin/vite.js build`。本环境未安装npm，直接调用对应Node入口。独立分支全量Go测试、go vet和相关全包race通过（Provider90.259秒），前端17项测试及生产构建通过。当前工作区前端26项及构建也通过；构建仅已有依赖PURE注释与大包提示。
