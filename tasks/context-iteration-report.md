# 上下文迭代交付记录（2026-10-12）

最新范围：用户已授权完成全计划实现并暂停真实模型/产品验收。P0—P3 及 P4 观测/离线入口已实现；真实模型调优仍待用户后续验收。下文早期阶段记录保留当时范围，最新结果见文末与 docs/context-runtime-implementation.md。

## 基线与推进顺序

基线 HEAD：`ebc7091`。工作区原有未提交改动保留，未提交或推送本轮修改。原有全量测试先通过，再实施 P0；P0 行为、全量、vet 通过后实施 P1。checkpoint 验证在 P0/P1 后完成，未严格按“P0 早期”执行，结果仅用于后续 P2/P3 的设计门槛。

固定 gpt-4o 文本样本覆盖 8192/64000 窗口和 quick/deep。旧口径样本：system 304、消息压缩前 4320、工具定义 13、工具截断后消息 1820 token。新预算包含实际请求消息和工具定义，不能以新旧水位数值直接推断收益。计数属于估算，尚未与真实 provider usage 校准。

## P0

- `agent.context` 提供统一默认策略；窗口、输出、裕量构成同源硬预算。未知模型需声明窗口。
- 生产 OpenAI 模型在序列化请求后、发送 HTTP 前核对最终消息、工具定义与实际输出选项，覆盖 Generate、Stream、WithTools 和调用级选项。
- 本地预算错误与 provider 上下文错误分开，避免本地拒绝错误地缩小运行时窗口。
- 可选旧对话先减少，当前问题、系统指令与工具调用对应关系保留；仍超硬预算明确拒绝。深度压缩调用副本，快速中间件保存图状态，不修改原可信执行结果。
- 显式零预算保持零，避免回填默认额度和无效召回；摘要/记忆调用使用受保护客户端及明确输出上限。

测试覆盖超长问题/指令、工具参数/定义、输出覆盖、未知窗口、运行时窗口修正；HTTP 模拟服务验证超限零请求及正常 Generate/SSE Stream。

## P1

- 向量候选保持距离顺序，关键词候选保留查询排序；合并排名后选择，超长候选跳过，展示按时间和 ID 全序。
- 当前消息和近期历史在选择前排除重复；计入片段包装开销；空召回预算回归近期历史。
- Replanner 去掉 ExecutedSteps 重复正文，投影计划与结果，保留失败/拒绝/重试及证据标识；完整结果仍在可信状态，汇总结果保留全文。
- 请求取消可结束召回等待，结果通道缓冲避免发送阻塞。

## 持久化验证

使用实际 Eino ChatModelAgent 中断、文件 checkpoint store 和测试进程的独立 save/restore 子进程。逐字段核对 session 字符串里的摘要样例、产物 ID 样例、版本和源哈希，并验证恢复后的真实澄清响应。

此结果证明该序列化位置可用，不证明数据库原子发布、产物可读性、权限、配额、清理或完整运行快照已经实现。

## 验证证据

- 改动前：`context-baseline-test.log`、`context-baseline-budget.log`。
- P0：`context-p0-test.log`、`context-p0-full.log`、`context-p0-vet.log`。
- P1：`context-p1-test.log`、`context-p1-full.log`、`context-p1-race.log`。
- 跨进程：`context-checkpoint-probe.log`。
- 最终：`context-final-full.log`、`context-final-behavior.log`、`context-final-race.log`、`context-final-vet.log`。

全量命令为 `go test -vet=off -p 1 -count=1 ./...`；另行 `go vet -p 1 ./...`；相关模块运行 `go test -race -vet=off -p 1 -count=1`。进程退出码作为完成依据，日志仅作证据。

最终全量、vet 和三模块 race 均已确认退出码 0；新增连续用户消息保留及实际输出边界回归包含在最终 race 中。gofmt 与限定 diff 检查通过，新增 Go 文件已统一 LF，临时编辑脚本已清理。

## 验收边界与后续

### 继续验收结果

- 当前使用已有自定义配置 `deepseek-v4-pro`，声明窗口 64000；没有修改用户的模型设置。早期 GLM 和 DeepSeek 蒸馏模型的限流结果不能代表此目标模型。
- 两条合成样本通过：算术正确、事实及 `doc-ctx/chunk-1` 引用正确。耗时 1942/2732 ms，prompt usage 105/127，本地估算 48/94；估算偏低 57/33 token，需扩大样本校准，不能宣称估算精确。当前硬保护依赖估算和安全裕量，并非 provider 实际 token 的数学保证。
- bge-m3 当前实际调用返回 1024 维，与配置和表字段一致。最新只读快照 997 条消息、993 条向量；556 条用户消息中 555 条有向量。已有向量全部 1024 维，尚缺 1 条用户向量，未执行回填，也未证明全部历史向量属于同一模型空间。
- 浏览器注入预算错误 SSE，复现新会话路由加载覆盖错误卡片；已修复同一活动会话重复选择。新会话和已有会话均显示标题/详情、无重试按钮、输入可用，无运行时异常。模拟 API，不等于运行中后端端到端验收。
- 前端 5 项测试及修复后构建通过。证据：`context-accept-deepseek-pro.log`、`context-accept-model-inventory.log`、`context-accept-browser.json`、`context-accept-browser-new.json`、`context-accept-browser.png`、`context-accept-frontend-test.log`、`context-accept-frontend-build.log`。

剩余：在线召回内容/排序验证、历史向量空间一致性和缺失原因、quick/deep 多轮完整用例、较长输入 token 偏差与质量/耗时对比。两条样本不用于推断整体完成率。

### 后续实测

- 合成文本通过实际 bge-m3 和 PostgreSQL pgvector 排序：预算问题对应证据排名第一，余弦距离 0.306761；备份/午餐无关样本距离 0.519736/0.540826，均超过当前 0.5 阈值。使用 SQL VALUES 临时样本，无业务写入；未覆盖 ContextService 整链路和旧向量空间兼容。
- DeepSeek 较长样本：中文估算 3552、实际 prompt 2503，耗时 1866 ms；英文混合估算 17756、实际 prompt 19306，耗时 4012 ms。事实回答均正确。英文样本低估 1550 token（相对估算约 8.73%）；本次 64000 窗口默认 5120 裕量可覆盖该差额，但无法外推到接近窗口上限。计数校准门槛仍未通过，不依据两条样本直接调整全局系数。
- 实际 Engine＋DeepSeek 多轮历史：quick 3178 ms、deep 20874 ms，均 done=true、无 error，均保留“海星/4096”事实；deep 产生计划。使用合成用户/会话与历史，未注册业务、外部工具，无业务数据库写入。不覆盖 HTTP/SSE 后端、检索工具、审批或恢复。
- 缺失向量快照：三条 assistant 正文为空，现有逻辑跳过空正文，属预期；一条 user 正文非空，关联日志确认在 2026-10-12 15:35:53 因 deadline exceeded 计算失败。现有后台计算总超时 5 秒、失败不重试，解释该消息未落向量；未进行回填或扩大为后台任务治理。

证据：`context-accept-extended.log`、`context-accept-agent-live.log`、`context-accept-missing-diagnosis.log`。本轮没有修改业务 Go 代码，不重复全量/race/vet；延续此前通过的证据。真实工具/恢复与接近上限的计数测试仍待补充。

### 窗口边界与审批验收

改动前边界输入估算 57581、实际 prompt 62506，低估 4925，已接近默认 5120 裕量。定位到未知模型名的编码回退缺陷：注释约定回退 cl100k_base，代码却直接使用字符估算。已修复 pkg/tokenutil，并添加未知模型计数和截断使用同一 BPE 的回归测试。

修复后 DeepSeek 安全范围长输入估算 56041、实际 56106，差额 65，耗时 3748 ms，事实回答正确；原来的 3900 行边界样本现在本地拒绝，保留输出及安全裕量。默认 BPE 并非 DeepSeek 专用 tokenizer，不能宣称所有输入精确；编码加载失败时原有字符估算仍为降级路径，生产应保证默认 BPE 可用，后续用多种文本校准。

真实 DeepSeek＋深度 Engine＋内存假删除工具：同意分支中断前零调用，恢复完成后恰好调用一次，不操作真实文档；拒绝分支首次观察零调用但缺少完成事件，复核遇 timeout；最终复核成功进入审批，拒绝恢复达到 90 秒请求上限，工具仍零调用、无重新中断。真实模型拒绝后的收尾验收未通过，不能把未执行危险动作等同于正常完成。确定性批准/拒绝、新 Engine 恢复与跨进程序列化回归属于独立证据。后续需按模型调用耗时与重规划次数定位耗时来源，当前证据无法断言仅为服务超时或仅为编排缺陷。

新增证据：`context-accept-boundary.log`、`context-accept-boundary-bpe.log`、`context-accept-approval-live.log`、`context-accept-denial-live.log`、`context-accept-denial-recheck.log`；修复全量 `context-bpe-full.log` 和 vet `context-bpe-vet.log` 已确认退出码 0，race 结果待进程结束后确认。

最终 race `context-bpe-race.log` 四模块均通过，退出码 0：tokenutil、Agent、Service、LLM。gofmt 与限定 diff 检查通过。拒绝恢复的真实模型收尾仍未通过，不继续无限重试。

### 拒绝恢复定位与修复

按模型调用计时复现（`context-denial-timed.log`、`context-denial-correction.log`）证实：拒绝结果已记为 TaskError，工具零执行。Replanner 返回包含失败操作 t1 依赖的计划，校验报“任务依赖已失败或取消”，触发一次计划纠正。改前复现重规划/纠正分别耗时 36085/39941 ms，随后汇总阶段耗尽90秒。此前探针根 context 90秒也会挤占恢复时限，现已改为每次运行独立90秒，整体240秒；旧超时证据不能当作独立90秒恢复证据。

修复仅补齐 Replanner 输入规则：待执行任务不可依赖失败/取消任务；拒绝后取消旧的失败依赖汇总，保留成功研究，只安排汇总；无成功研究时依赖为空，明确说明操作未执行，不重新安排操作或替代研究。没有放宽计划校验或改变审批语义。

新增行为测试 `TestDeniedPlanCanSummarizeWithoutFailedDependency` 验证失败依赖仍拒绝、合法汇总可执行、旧等待任务取消及拒绝事实保留。全量 `context-denial-fix-full.log`、vet `context-denial-fix-vet.log`、定向 race `context-denial-fix-race.log` 均退出码0。

修复后真实 DeepSeek 复核 `context-denial-fixed-live.log`：中断前零调用、拒绝恢复后零调用、未再次中断，正常 answer/done，无错误；没有计划纠正调用。恢复阶段7次模型调用合计48307 ms，重规划17889 ms、专业汇总12718 ms、最终 respond 9248 ms。这证明该合成用例收尾通过，不保证所有真实请求时延；仍需多样本评估。探针仅内存假工具并聚合模型流作计时，不覆盖生产HTTP展示、真实删除权限或跨进程应用恢复。

尚未执行真实模型质量、引用、完成率、usage/耗时对比，真实数据库向量覆盖/维度检查与回填，以及浏览器预算错误展示核对。P0/P1 已通过离线工程验证，不等于生产效果验收全部完成。

当前仅支持文本预算，多模态请求明确拒绝。最终保护占用 OpenAI request payload modifier 扩展点；目前生产调用未使用自定义 modifier，后续新增时需组合后再检查，不能覆盖最终保护。角色治理本轮主要为 Replanner 投影，没有实现完整运行摘要或产物读取。

复盘：保留一个预算算法和一个生产最终请求检查，分别适配持久状态与调用副本；新产物系统不能仅交付引用替换。后续先完成真实效果验收，再依据跨进程证据单独规划 P2/P3。

## 全计划实现补充（用户暂停真实验收后）

P2：新增不可变 PostgreSQL 产物、共享升级/启动迁移 SQL、用户事务锁保护配额、五维归属读取、输出包装预算、保守清理及 App 取消/等待。工具保存成功才换引用，审批/澄清/失败事实不外置，写失败原文保留。TaskResult 保存真实产物引用；汇总由程序装配成功依赖的原文片段，不新增汇总工具。

P3：专业任务 session 快照记录摘要、次数、覆盖范围、源哈希与引用；独立限额、超时与最多2次调用。只有源/版本一致且输入缩短才发布，失败/无进展/超时保留原状态。快速不增加同步摘要。checkpoint 版本1包装冻结 Request、模型及策略，校验包含元数据和框架字节。旧格式兼容保留；旧深度 runID 由可信用户/会话/计划确定，恢复不重新生成不一致标识。

P4：中文日志增加运行/角色/任务、摘要版本和前后占用；TestContextOfflineComparison 输出固定样本 JSON 成本与协议保留报告。默认软水位未据模拟耗时调整，未新增真实模型调用。

工程证据：context-artifact-postgres-final.log 通过，覆盖重复迁移、12并发配额、幂等、归属、有效 checkpoint 保留与过期清理、真实事务回滚；context-p3-regression.log 验证 Eino 独立进程字节恢复；context-final-behavior.log 验证产物→澄清→跨 Engine 恢复→分段读→回答，工具只执行一次；context-legacy-compatibility.log 验证旧深度批准/拒绝兼容。最终全量 context-all-final-full.log 退出码0；vet/race结果完成后补充。

范围限制：不宣称已证明真实答案质量、usage 或生产时延；没有生产向量回填/数据迁移执行或真实删除。临时数据库测试写入隔离 schema，并已清理。原有工作区和暂存区保留，未提交/推送；构建缓存及临时探针不属于交付。

最终 race 曾发现 TestPlanExecuteMultipleApprovalsResumeOneByOne 中子任务写父 session 与 Eino gob 序列化竞争；修复为子任务私有 JSON 快照随 peChildState 中断保存，不扩大锁到摘要调用，也不修改依赖源码。最终结果以修复后的回归日志为准，不能把前一次失败记录当作通过。

修复后 context-private-snapshot-race.log：完整 Agent race 退出码0。此前 context-all-final-race.log 中 Repository/LLM/Service/config 四模块 race 均通过；其中 Agent 失败已由新的完整 Agent race 验证解决。最终 context-all-final-vet.log 退出码0，git diff --check 通过；新增 Go 文件均为 LF，既有修改保持原行尾。最终全量重跑结果以 context-all-final-full.log 及进程退出码为准。

最终 context-all-final-full.log 已确认进程退出码0，全量工程回归通过。剩余计划实现项全部勾选；P4真实模型效果和参数调优按用户要求留待后续验收，不标为实测完成。
