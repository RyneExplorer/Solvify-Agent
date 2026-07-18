<div align="center">

# Solvify-Agent

**面向企业知识管理场景的 RAG 与 ReAct Agent 系统**

通过多源知识接入、混合检索、深度推理与动态工具调用，为团队提供可配置的知识管理和智能问答能力

[![Go Version](https://img.shields.io/badge/Go-1.26.2-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Gin](https://img.shields.io/badge/Gin-1.12.0-008ECF?logo=gin&logoColor=white)](https://gin-gonic.com/)
[![Eino](https://img.shields.io/badge/Eino-0.9.1-6C5CE7)](https://github.com/cloudwego/eino)
[![Vue](https://img.shields.io/badge/Vue-3-4FC08D?logo=vuedotjs&logoColor=white)](https://vuejs.org/)
[![PostgreSQL + pgvector](https://img.shields.io/badge/PostgreSQL_%2B_pgvector-15%2B-4169E1?logo=postgresql&logoColor=white)](https://www.postgresql.org/)
[![Redis](https://img.shields.io/badge/Redis-7%2B-FF4438?logo=redis&logoColor=white)](https://redis.io/)
[![CI](https://github.com/RyneExplorer/Solvify-Agent/actions/workflows/ci.yml/badge.svg)](https://github.com/RyneExplorer/Solvify-Agent/actions/workflows/ci.yml)

[核心特性](#-核心特性) · [功能演示](#-功能演示) · [系统架构](#-系统架构) · [快速开始](#-快速开始) · [项目文档](#-项目文档)

</div>

## ✨ 核心特性

| 核心能力 | 说明 |
| --- | --- |
| 🧠 **智能问答** | 提供快速检索与 ReAct Agent 深度推理两种模式，支持多轮会话和 SSE 流式输出 |
| 🔍 **高级 RAG 管线** | 组合向量检索、关键词检索与 RRF 融合，并可按需启用 Rerank 和相邻分块扩展 |
| 📚 **知识与文档管理** | 支持知识库、文档、版本和处理任务管理，覆盖常见文本、DOCX、PDF、PPTX 等格式 |
| 🔗 **钉钉知识接入** | 支持 OAuth 绑定、工作空间与节点浏览，以及同步项导入本地知识库 |
| ⚙️ **灵活的模型配置** | 支持系统模型和用户模型配置，可接入 OpenAI 兼容的 LLM 与 Embedding 服务 |
| 🧰 **动态工具系统** | 通过可配置的 HTTP Provider 为 Agent 加载联网搜索、天气查询等外部工具 |
| 🛡️ **完整管理能力** | 包含用户认证、存储配额、统一搜索以及用户、会话、模型和工具后台管理 |

## 📸 功能演示

<table>
  <tr>
    <td width="50%" align="center">
      <strong>智能问答</strong><br />
      <img src="screenshot/home.png" alt="Solvify 智能问答界面" />
    </td>
    <td width="50%" align="center">
      <strong>知识库管理</strong><br />
      <img src="screenshot/knowledge.png" alt="Solvify 知识库管理界面" />
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <strong>文档管理</strong><br />
      <img src="screenshot/document.png" alt="Solvify 文档管理界面" />
    </td>
    <td width="50%" align="center">
      <strong>文档编辑与预览</strong><br />
      <img src="screenshot/edit.png" alt="Solvify 文档编辑与预览界面" />
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <strong>统一搜索</strong><br />
      <img src="screenshot/search.png" alt="Solvify 统一搜索界面" />
    </td>
    <td width="50%" align="center">
      <strong>AI 模型配置</strong><br />
      <img src="screenshot/configuration.png" alt="Solvify AI 模型配置界面" />
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <strong>Agent 工具配置</strong><br />
      <img src="screenshot/config.png" alt="Solvify Agent 工具配置界面" />
    </td>
    <td width="50%" align="center">
      <strong>后台管理</strong><br />
      <img src="screenshot/manage.png" alt="Solvify 后台管理界面" />
    </td>
  </tr>
</table>

## 🏢 系统架构

```mermaid
flowchart LR
    UI["Vue 3 Web"] --> API["Gin API"]
    API --> SVC["Service"]
    SVC --> AGENT["Eino ReAct Agent"]
    SVC --> REPO["Repository"]
    SVC --> PARSER["Python 文档解析器"]
    SVC --> DING["钉钉开放平台"]
    AGENT --> RAG["Hybrid RAG"]
    AGENT --> TOOLS["动态工具"]
    RAG --> PG[("PostgreSQL + pgvector")]
    REPO --> PG
    REPO --> REDIS[("Redis")]
```

后端采用 MVC + Service + Agent 能力层组织：Controller 处理 HTTP 边界，Service 编排业务用例，Agent/RAG/Tool 负责智能能力，Repository 负责数据访问。详细边界见[架构说明](docs/architecture.md)

## 🧰 技术栈

| 类别 | 技术选型 |
| --- | --- |
| 后端 | Go 1.26.2、Gin、GORM |
| Agent | Eino、ReAct、动态 Tool Calling |
| 检索 | 向量检索、关键词检索、RRF、可选 Rerank 与分块扩展 |
| 数据 | PostgreSQL 15+、pgvector、Redis 7+ |
| 前端 | Vue 3、Vite 5、Element Plus、Tailwind CSS |
| 文档解析 | Python 3.10+、python-docx、pdfplumber、python-pptx |
| 外部集成 | OpenAI 兼容接口、钉钉开放平台 |

## 🚀 快速开始

### 1. 环境要求

- Go 1.26.2+
- Node.js 18.x 或 20+，npm 8+
- Python 3.10+
- PostgreSQL 15+，并安装 pgvector 扩展
- Redis 7+

### 2. 获取项目

```bash
git clone https://github.com/RyneExplorer/Solvify-Agent.git
cd Solvify-Agent
```

### 3. 初始化 PostgreSQL

确保 PostgreSQL 和 Redis 已启动，然后创建数据库并执行项目脚本：

```bash
createdb -U postgres solvify_agent
psql -U postgres -d solvify_agent -f scripts/init_knowledge_schema.sql
```

SQL 脚本会创建项目表，并检查 `pgcrypto` 与 `vector` 扩展。执行账号需要具有创建扩展的权限

### 4. 创建配置

```bash
cp configs/config.yaml.example configs/config.yaml
```

项目可只使用 `configs/config.yaml`。如需通过环境变量覆盖配置，再复制可选模板：

```bash
cp .env.example .env
```

至少检查以下配置：

- PostgreSQL 与 Redis 连接信息
- `jwt.secret`
- Embedding 服务的模型、Base URL 和 API Key
- 使用钉钉同步时的应用凭证与回调地址
- 使用注册验证码与密码重置时的 SMTP 配置

配置加载优先级如下，后加载的配置会覆盖之前的值：

```text
代码默认值 < configs/config.yaml < .env < 系统环境变量
```

完整字段见 [config.yaml.example](configs/config.yaml.example) 和 [.env.example](.env.example)。请勿提交包含 API Key、Token 或密码的本地配置文件

### 5. 安装后端与文档解析依赖

```bash
go mod download
python -m pip install python-docx==1.1.2 pdfplumber==0.11.4 python-pptx==1.0.2
```

三个 Python 包分别用于解析 DOCX、PDF 和 PPTX；版本与 [`requirements.txt`](pkg/documentparser/python/requirements.txt) 保持一致。也可以通过依赖文件一次安装：

```bash
python -m pip install -r pkg/documentparser/python/requirements.txt
```

### 6. 启动后端

```bash
go run ./cmd/server
```

服务默认监听 `http://localhost:8080`。通过健康检查确认启动成功：

```bash
curl http://localhost:8080/health
```

### 7. 启动前端

打开新的终端：

```bash
cd design/vue
npm ci
npm run dev
```

访问 `http://localhost:5173`。开发服务器会将 `/api` 请求代理到 `http://localhost:8080`

首次使用真实模型问答前，请在后台管理中添加系统模型，或在个人配置中添加 OpenAI 兼容模型。文档入库和 RAG 检索依赖服务端 Embedding 配置

## API 概览

除公开认证接口外，`/api/v1` 下的业务接口均需要 Bearer Token。API 使用统一响应结构：

```json
{
  "code": 0,
  "message": "成功",
  "data": {}
}
```

| 模块 | 主要入口 | 能力 |
| --- | --- | --- |
| 健康检查 | `GET /health` | 服务状态 |
| 认证 | `/api/v1/auth` | 注册、登录、刷新、登出、验证码与密码重置 |
| 知识库与文档 | `/api/v1/knowledge-bases`、`/api/v1/documents` | 知识库、文档、版本、处理任务与分块 |
| 智能问答 | `/api/v1/chat` | 会话管理、快速检索与深度推理 |
| 搜索 | `/api/v1/search` | 会话与知识内容统一搜索 |
| 模型与工具 | `/api/v1/models`、`/api/v1/user/model-configs`、`/api/v1/user/tool-configs` | 系统模型、用户模型和 Agent 工具配置 |
| 数据同步 | `/api/v1/dingtalk`、`/api/v1/sync-sources`、`/api/v1/sync-jobs` | 钉钉绑定、目录浏览与同步任务 |
| 管理后台 | `/api/v1/admin` | 用户、会话与工具类型管理 |

具体路由以 [`internal/api/router.go`](internal/api/router.go) 及各模块 `routes.go` 为准

## 📚 项目文档

- [架构说明](docs/architecture.md)：分层结构、RAG 管线、Chat 双模式与 Tool 系统
- [开发指南](docs/DEVELOPMENT.md)：模块边界、开发流程、配置规范与测试要求
- [产品需求文档](docs/PRD.md)：产品目标、功能需求、业务规则与边界条件
- [配置模板](configs/config.yaml.example)：服务、模型、检索、数据库及集成配置

## 参与贡献

1. Fork 仓库并从 `main` 创建功能分支
2. 只提交与目标相关的修改，并遵循[开发指南](docs/DEVELOPMENT.md)
3. 提交前完成后端测试和前端构建
4. 创建 Pull Request，说明改动范围与验证结果

```bash
go test ./...

cd design/vue
npm run build
```
