# 工具执行时限

开发约束见[AGENTS.md](../AGENTS.md)。HTTP与MCP时限使用现有供应商编辑框和创建/更新接口保存，没有独立时限策略页面、路由或数据表。Agent整次任务总时限暂未实施；本次不修改模型超时策略。

## 管理入口与配置归属

管理员打开「后台管理 → 工具管理 → 供应商 → 添加/编辑」：

- HTTP配置增加“单次执行超时（秒，可选）”，保存在既有 `tool_providers.provider_config` JSON的 `timeout` 字段，允许1～86400秒整数。留空删除该字段，沿用系统 `tools.default_timeout`。
- MCP配置沿用“连接及调用超时（秒，可选）”，保存为已有 `provider_config.mcp.timeout`。显式填写30秒也会保存为30，修复前端曾将30省略、实际调用回退到300秒的问题。
- MCP留空保持原兼容语义：连接初始化默认30秒，单次工具调用默认300秒；填写后用于两阶段，连接池仍有60秒初始化等待上限。连接与调用拆分属于T5，不在本次修改范围。
- 系统预置MCP继续由YAML启动同步并保护连接配置，已有管理限制保持不变；其超时修改使用系统预置配置。此表单适用于管理员创建的可编辑供应商。

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

已有HTTP工具测试入口使用相同Provider配置与执行保护；管理表单的保存和测试共用配置构建函数。MCP保留原工具调用包装和连接池。

## 发布与验证

无需新增数据库表、列或迁移，部署更新后的前后端即可。此前独立策略方案的 `tool_timeout_policies`、迁移SQL、管理组件与GET/PUT策略路由均已从本次代码移除；该方案没有迁移到应用数据库，因此无需执行删表或数据回滚。

测试覆盖现有供应商创建/更新保存HTTP及MCP时限、非法值拒绝、清空恢复默认、管理员权限和原路由错误行为；独立策略路由确认404。前端执行管理页真实表单编解码函数与API函数，验证30秒显式提交、保存回显、旧配置留空及输入校验。ToolFactory验证管理员配置进入实际HTTP参数及MCP调用deadline，新一轮读取更新值。

本地HTTP服务验证慢响应头、响应体停滞、上游取消/短deadline、管理员覆盖与用户参数不可覆盖；实际16秒响应在25秒供应商时限下成功。真实Eino ToolsNode覆盖内部取消、审批顺序和文档工具流式错误传播。未调用真实供应商、未做浏览器端到端或外部副作用验收，未重启运行服务。

验证命令：

```shell
go test ./...
go vet ./...
go test -race ./internal/agent ./internal/service ./internal/tool/... ./internal/api/v1/tool ./pkg/config -count=1 -timeout 240s
```

前端在 `design/vue` 运行 `node --test tests/*.test.mjs` 和 `node node_modules/vite/bin/vite.js build`。本环境未安装npm，直接调用对应Node入口；构建仅有现有依赖PURE注释和大包提示。仅含本次工具改动的独立分支已通过全量Go测试、vet、上述全部相关包race、前端16项测试和生产构建；Agent race耗时131.954秒。未验收真实供应商或浏览器端到端。
