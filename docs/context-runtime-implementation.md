# 上下文运行迭代实施说明

日期：2026-10-12。用户授权完成全计划实现，并暂停真实模型/产品验收。

## 数据与运行边界

会话装配继续共用 ContextService 与统一预算。快速运行不增加同步摘要调用；深度专业任务按 run/agent/task 保存独立快照，仅摘要已完成且可压缩的旧完整工具组。最新组、活动组、澄清、审批和明确失败保留。摘要为数据消息，不能变成系统指令或覆盖可信任务状态。

原文在 `context_artifacts` 不可变保存，工具结果只在提交成功后换成预览、ID、SHA-256 与版本。写失败保留原结果，不生成不可用引用；后续预算裁剪/拒绝仍可能触发。原文保存48小时，存在有效 checkpoint 的会话保守保留；没有独立文件存储或任意路径入口。

`read_context_artifact` 按本运行本角色本任务读取。用户/会话归属在每次数据库查询时重新核对；范围按字符计算，整个 JSON 输出也受 token 上限约束。汇总角色不新增工具，程序按成功前置任务的原始范围读取有限原文，标明片段是否完整。产物引用来自真实工具输出，持久化在 TaskResult，模型生成的引用不能扩大读取范围。

快照含版本、角色/任务归属、指令哈希、摘要次数、摘要、覆盖组范围及源哈希、产物引用，保存为 Eino session JSON 字符串。摘要锁外生成，检查源哈希与快照版本且确认输入缩短才发布。失败或无进展不替换原消息；摘要最多2次，输入6000、输出800 token，15秒超时，无业务工具及自动重试。次数随 checkpoint 保存，恢复不重置。

PlanExecute 专业子任务使用私有 JSON 快照，在中断时随 peChildState 的框架恢复数据一起保存；不会在并行执行时写父级 session。该边界避免 Eino v0.9.14 对共享 session 序列化与子任务写入的竞争。普通快速/遗留单 Agent 使用自身 session 快照。摘要引用由程序追加保留，模型省略 ID 不会导致失去回读入口。

checkpoint 包装保存冻结 Request、模型/窗口/输出及预算策略，SHA-256 校验覆盖元数据与框架正文。恢复保留原素材与工具配置标识，重新构建当前可用工具；模型、窗口、策略或指令变化明确失败，源缺失/哈希变化明确失败。旧无包装 checkpoint 字节仍兼容，不删除有效期内恢复入口。新写入使用版本1包装，遗留深度根状态也纳入包装。

## 数据库升级

前提：应用基础库已有 `chat_sessions` 和 `agent_checkpoints`。新部署先执行项目基础建库步骤，再执行本迁移；已有库直接执行本迁移。迁移只创建产物表及索引，不回填历史消息或改写旧 checkpoint。

```powershell
psql <已有连接选项> -v ON_ERROR_STOP=1 -f scripts/migrations/20261012_context_artifacts.sql
```

迁移可以重复执行。启动使用嵌入的同一 SQL，初始化失败阻止启动并释放连接。App 每小时清理一次，每轮最多30秒；关闭时先取消并等待清理，再关闭数据库。配额检查与写入使用 PostgreSQL 用户事务锁，跨进程原子执行；仓库加入调用方已有业务事务。

默认限额：单产物2MiB、单运行20MiB、单用户100MiB。过期但尚未删除的行仍计入配额。读取限制4000字符、1200 token；外置触发1500 token。配置唯一来源为 `pkg/config/runtime_context.go`，示例见 `configs/config.yaml.example`。48小时下限为旧 checkpoint 24小时有效期保留余量。

## 可重复工程验证

```powershell
go test -vet=off -p 1 -count=1 ./...
go vet -p 1 ./...
go test -race -vet=off -p 1 ./internal/agent ./internal/repository ./internal/llm ./internal/service ./pkg/config
```

真实数据库验证单独启用，在唯一临时 schema 内创建最小基础表，测试结束清理；应用业务表不写入。

```powershell
$env:SOLVIFY_CONTEXT_DB_TEST = '1'
go test -vet=off -p 1 -count=1 ./internal/repository -run TestContextArtifactPostgres
Remove-Item Env:SOLVIFY_CONTEXT_DB_TEST
```

测试覆盖重复迁移、12个并发配额写入、幂等、到期、checkpoint 保留、归属隔离、事务失败回滚。独立进程 Eino checkpoint 测试使用临时持久存储，跨 Engine 完整工具链另有生产装配测试。

固定样本离线对比入口：

```powershell
go test -vet=off -p 1 -count=1 -v ./internal/agent -run '^TestContextOfflineComparison$'
```

输出包含样本版本、模型标识、模式、窗口/输出、压缩前后 token、摘要次数、问题/最新组保留、配对合法性及工程耗时。模型为确定性模拟，不能将该耗时当作 DeepSeek 首段/总时延，也不能声称已证明答案质量、引用完整性或实际 usage 改善。

## 效果验收边界

P0—P3 的实现及 P4 的观测/离线入口已完成。按用户要求，本轮不再进行真实模型产品验收、不据此调整默认软水位、不回填生产向量、不执行真实副作用工具。真实效果调优仍需同模型、同样本与配置版本比较，保留为用户后续验收项。

现有服务端副作用的数据库提交与外部执行失败窗口没有因本迭代自动消失；产物与 checkpoint 不构成“恰好执行一次”的证明。保守清理会保留同会话其他未过期 checkpoint 的相关产物，实际存储成本需后续观测。
