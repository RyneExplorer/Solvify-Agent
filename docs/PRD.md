# Solvify-Agent 企业级知识管理系统 PRD

## 1. 项目背景

Solvify-Agent 是一个基于大语言模型的知识管理框架，旨在解决企业文档分散、知识检索效率低、智能问答能力不足等问题。通过 **RAG 技术（混合检索 + 重排序 + 分块扩展）** 和 **ReAct Agent 编排**，将分散的文档沉淀为可查询、可推理、可持续演进的专属知识资产。

**技术栈：** Go 1.26 + Gin + GORM + Eino（ReAct Agent）+ PostgreSQL（pgvector）+ Redis + Vue 3

---

## 2. 产品目标

**解决的问题：**
- 企业文档分散在多个系统，查找困难
- 传统搜索无法理解语义，检索精度低
- 缺乏基于企业知识的智能问答能力

**目标用户：**
- 企业知识工作者（日常查询、学习）
- 客服人员（快速回答客户问题）
- 管理员（系统配置、用户管理）

---

## 3. 用户角色

| 角色 | 描述 | 权限范围 |
|------|------|----------|
| 超级管理员 | 系统初始化时自动创建 | 所有功能权限 |
| 管理员 | 被授权的管理用户 | 用户管理、模型管理、工具管理、会话管理 |
| 普通用户 | 默认角色 | 知识库创建、文档上传、问答、个人模型配置 |

---

## 4. 功能需求

### FR-001 用户注册与登录

用户通过邮箱注册，邮箱验证码激活。支持 JWT Token + 密码登录，Token 刷新，登出（黑名单）。

**实现方式：**
- 密码 bcrypt 加密
- JWT Token 签发（可配过期时间）
- 登出 TokenID 写入 Redis 黑名单（24h TTL）
- 图片验证码（base64Captcha，4 位数字，5 分钟有效）
- 邮箱验证码（SMTP/QQ 邮箱）
- 密码重置（邮箱验证码 + 新密码）

### FR-002 知识库创建

| 来源类型 | 说明 | 操作权限 |
|----------|------|----------|
| 自建知识库（local） | 用户手动创建，上传文档 | 完全控制 |
| 同步知识库（sync） | 从钉钉平台同步 | 只读，系统定期增量同步 |
| 联网搜索知识库（web_search） | 保存搜索结果 | 只读，由搜索结果自动生成 |

**已实现：** 三种来源类型均支持。自建知识库支持名称、分类、描述。同步知识库显示来源平台标识。

### FR-003 文档上传与处理

上传文档到知识库，系统自动执行处理管线：

```
上传 → 解析 → 分块（Chunk） → 向量化（Embedding，1024 维） → 索引建立
```

- 可解析格式：Txt / Markdown / HTML / CSV / JSON / DOCX / PDF（PDF 仅支持可提取文本）
- 可上传但暂不解析：DOC / Excel / PPT / 图片
- 单文件 ≤ 100MB
- 状态：已上传(1) → 处理中(2) → 已就绪(3) / 失败(4) / 已删除(5)
- 删除保留 30 天
- 支持版本管理：在线编辑保存新版本 → 自动重新分块和向量化

**存储：** 单用户总配额 10GB（storage_quotas 表），上传前检查，不足返回错误。

**处理任务：** document_processing_jobs 表跟踪（parse / chunk / embed / reindex），含状态和错误信息。

### FR-004 智能问答

**问答配置栏：**
- 知识库选择（默认全部）
- 检索模式（快速检索 / 深度模式）
- 模型选择（系统默认 + 用户自定义）

**快速检索（search_mode=quick）：**
```
Query Rewrite（LLM 改写） + RAG 检索 ──并行──→ 合并结果 → LLM 流式生成
```

**深度模式（search_mode=smart-reasoning）：**
```
用户问题 → ReAct Agent 引擎
  ├── Think（分析问题）
  ├── Act（调用 knowledge_search / web_search / 自定义工具）
  ├── Observe（评估结果）
  └── 循环 → 最终答案
```

**来源标注：** `<kb>` 知识库（蓝色），`<web>` 网络（绿色）

**响应方式：** Server-Sent Events (SSE) 流式输出

### FR-005 RAG 检索管线

```
HybridRetriever（核心）
  ├── 向量检索：pgvector cosine 距离 + IVFFlat 索引
  ├── 关键词检索：中文分词 (gse 分词器) + PostgreSQL GIN 索引 (text[])
  └── 融合算法：
       ├── 同源质量检查（向量：cosine ≥ 阈值；关键词：陡度检测）
       ├── Min-Max 归一化
       ├── RRF 融合：score = vector_weight/(k+rank) + keyword_weight/(k+rank)，k=60
       ├── 跨源验证
       └── TopK 截断

RerankRetriever（装饰器，可配置开关）
  └── 调用 Rerank API → 替换分数 → 重排 → 阈值过滤 → TopN

ExpandRetriever（装饰器，可配置开关）
  └── 获取相邻分块 → Jaccard 去重 → 上下文补充
```

**优雅降级：** 向量检索失败 → 关键词检索；Rerank API 失败 → 返回原始结果

### FR-006 ReAct Agent 多步推理

- 引擎：eino ReAct Agent
- Think → Act → Observe 循环，最多 max_iterations 轮
- 自动决定 tool 调用（knowledge_search / web_search / 用户自定义工具）
- 回调事件：thinking / plan / tool_call / tool_result / answer / citation / done
- SSE 流式输出，支持 reasoning steps

### FR-007 模型配置管理

**系统模型（管理员）：**
- CRUD 操作
- 支持所有 OpenAI 兼容厂商
- Redis 缓存 + 连接池复用

**用户自定义模型：**
- 用户自行添加 API Key + Base URL + Model ID
- 仅本人可见和使用
- Redis 缓存

**LLM 客户端工厂：** sync.Map 缓存（按 baseURL+apiKey+modelID+config 哈希），HTTP 连接池（100 总连接，20/host，10 分钟空闲超时）

### FR-008 工具系统

**Provider 注册表模式：**
```
ToolType（数据库定义）→ ToolProvider（HTTP Provider，Template 驱动）
  ├── 模板渲染：{{placeholder}} 替换
  ├── Auth：bearer / api_key / basic
  └── Response Mapping：JSONPath 提取
```

- 管理员：ToolType CRUD + ToolProvider CRUD
- 用户：查看模板 + 创建/管理 UserToolConfig
- Agent：动态加载用户启用的工具 → 注册到 ReAct Agent
- Schema 自动推断：从 ToolType 定义 / ToolProvider 配置 / BodyTemplate 占位符

### FR-009 钉钉集成

- OAuth 登录 / 绑定 / 解绑
- 工作空间列表 / 节点列表
- 文档导出 → 创建同步知识库
- 同步流程：SyncSource → SyncJob（全量/增量）→ SyncItem → 选择性导入

### FR-010 多源同步

- 数据源：SyncSource 绑定平台（钉钉）和知识库
- 任务：SyncJob（全量/增量，success + failure 计数，错误消息）
- 文件项：SyncItem（目录树 + import_status）
- 导入：选中 SyncItem → 创建 Document → 解析/分块/向量化
- 设计预留：飞书、Notion 扩展接口

### FR-011 会话管理

- 会话 CRUD（创建/列表/重命名/删除）
- 消息发送（SSE 流式）
- 历史消息加载（传递完整对话上下文给 LLM）
- 管理员：全局会话列表、删除、清理过期

### FR-012 统一搜索

- `GET /api/v1/search?q=...&type=chat|document|all`
- 搜索范围：chat_messages.content + document_chunks.content
- 全文检索

### FR-013 后台管理

- 用户管理（列表 / 详情 / 状态管理 / 密码重置）
- 会话管理（列表 / 删除）
- 模型管理（CRUD）
- 工具管理（ToolType CRUD / ToolProvider CRUD）

### FR-014 存储配额

- 默认 10GB/用户
- 上传前检查配额
- 配额不足返回错误
- GET 不隐式写库

---

## 5. 业务规则

| 编号 | 规则 |
|------|------|
| BR-001 | 单用户总存储配额 ≤ 10GB，单文件 ≤ 100MB |
| BR-002 | 文档状态必须为"已就绪"(3) 才能参与检索 |
| BR-003 | 多租户数据隔离（user_id 隔离） |
| BR-004 | 超级管理员角色不可删除 |
| BR-005 | 文档删除保留 30 天 |
| BR-006 | 同步知识库为只读 |
| BR-007 | 同步知识库存储不计入用户配额 |
| BR-008 | 请求日志不打印密钥/Token/密码/连接串 |
| BR-009 | JWT 登出 Token 黑名单（Redis，24h TTL） |
| BR-010 | Embedding 缓存（Redis，SHA-256 哈希，24h TTL） |

---

## 6. 边界条件

| 场景 | 处理方式 |
|------|----------|
| 知识库为空 | 提示用户上传文档 |
| 快速检索无结果 | 建议切换到深度模式 |
| 深度模式无结果 | 提示用户调整问题描述 |
| 文件上传幂等 | 相同文件名覆盖旧文件 |
| Rerank API 失败 | 降级返回原始结果 |
| 向量检索失败 | 降级为关键词检索 |
| LLM 调用超时 | 返回部分结果并提示 |
| 搜索工具 API Key 无效 | 提示用户重新配置 |
| 存储空间不足 | 提示用户清理或扩容 |
| 操作他人资源 | 返回权限不足提示 |
| 重复文档上传 | file_hash 去重检查 |

---

## 7. 非功能需求

### 性能
- 快速检索响应 ≤ 3 秒（不含 LLM 生成）
- 深度模式响应 ≤ 10 秒（含多步推理和联网搜索）
- 支持 100+ 并发用户
- LLM 客户端连接池复用
- Embedding / Model 配置 Redis 缓存

### 安全
- 密码 bcrypt 加密
- JWT Token + RefreshToken 机制
- 登出 Token 黑名单
- 多租户数据隔离（user_id）
- 敏感配置不写入日志和文档
- CORS 可配

### 可用性
- 优雅关闭（drain 连接 → 关闭 DB pool）
- 向量检索失败降级
- Rerank API 失败降级
- 日志轮转（lumberjack）

---

## 8. 已实现能力总结

| 能力 | 状态 | 关键文件 |
|------|------|----------|
| 用户注册/登录/登出/刷新 | ✅ | `internal/api/v1/auth/` `internal/service/auth_service.go` |
| JWT 鉴权中间件 | ✅ | `internal/middleware/auth.go` |
| 知识库 CRUD + 软删除 + 统计 | ✅ | `internal/service/knowledge_base_service.go` |
| 文档上传/处理/版本/重建索引 | ✅ | `internal/service/document_service.go` |
| 文档分块 + 向量化 (1024 维) | ✅ | `internal/service/document_chunk_service.go` |
| Hybrid 检索 (向量+关键词+RRF) | ✅ | `internal/rag/hybrid_retriever.go` |
| Rerank 重排序 (Qwen3-Reranker) | ✅ | `internal/rag/rerank_retriever.go` |
| Expand 分块扩展 | ✅ | `internal/rag/expand_retriever.go` |
| Embedding 客户端 + Redis 缓存 | ✅ | `internal/llm/embedding.go` `pkg/cache/` |
| 双模式问答 (快速/深度) | ✅ | `internal/service/chat_service.go` `internal/service/chat_mode.go` |
| ReAct Agent 引擎 | ✅ | `internal/agent/engine.go` `internal/agent/execute.go` |
| SSE 流式输出 | ✅ | `internal/agent/callback.go` |
| 模型管理 (系统+用户自定义) | ✅ | `internal/service/model_service.go` `internal/service/user_model_config_service.go` |
| 工具系统 (Provider+AgentTool) | ✅ | `internal/tool/` |
| 钉钉集成 (OAuth+Wiki+文档) | ✅ | `internal/integration/dingtalk/` |
| 多源同步 (SyncSource+Job+Item) | ✅ | `internal/service/sync_service.go` |
| 会话管理 | ✅ | `internal/service/chat_service.go` |
| 统一搜索 | ✅ | `internal/service/search_service.go` |
| 存储配额 | ✅ | `internal/service/storage_service.go` |
| 后台管理 | ✅ | `internal/api/v1/user/` `internal/api/v1/chat/` |
| 邮箱验证码 | ✅ | `pkg/email/` |
| 图片验证码 | ✅ | `pkg/captcha/` |
| 文件上传 | ✅ | `pkg/upload/` |
| 统一错误码 (50+) | ✅ | `pkg/errors/` |
| 数据库种子 | ✅ | `cmd/seed/main.go` |
