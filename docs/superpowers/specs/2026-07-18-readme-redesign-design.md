# Solvify-Agent README 重构设计

## 目标

将根目录 README 重构为兼顾产品展示与本地开发的 GitHub 项目首页，让首次访问者能够快速理解项目定位、核心能力、运行依赖和启动方式，同时避免继续堆叠配置表与路由细节

## 受众

- 希望了解 Solvify-Agent 产品能力的使用者
- 准备在本地运行前后端的开发者
- 需要快速定位架构、开发规范和产品文档的贡献者

## 内容结构

README 按以下顺序组织：

1. Hero：项目名称、一句话定位、Go 与 CI 徽章、页内导航
2. 核心特性：知识库、RAG、ReAct Agent、文档处理、模型与工具配置、钉钉同步
3. 功能演示：完整展示 `screenshot/` 目录下的八张项目截图
4. 架构与技术栈：使用一张简洁流程图说明前端、API、Service、Agent/RAG 与基础设施关系，并使用表格列出主要技术
5. 快速开始：覆盖 PostgreSQL、Redis、Python 解析依赖、Go 后端和 Vue 前端的完整启动流程
6. 配置说明：只说明配置优先级、配置模板和必须关注的配置项
7. API 与文档：保留模块级 API 入口，并链接现有架构、开发和产品文档
8. 开发与贡献：给出后端测试、前端构建及提交 PR 的最小流程

## 功能演示布局

- 展示智能问答、知识库管理、文档管理、文档编辑、统一搜索、模型配置、工具配置和后台管理全部八张截图
- 图片文件为 `home.png`、`knowledge.png`、`document.png`、`edit.png`、`search.png`、`configuration.png`、`config.png` 和 `manage.png`
- 使用双列图集控制页面长度，不将任何截图放入折叠区域
- 使用仓库内 `screenshot/` 的相对路径，保证 GitHub 页面可直接渲染
- 每张图片提供简短、准确的说明，不重复解释界面中已经可见的内容

## 快速开始设计

快速开始使用仓库当前实际存在的命令和文件：

- Go 版本以 `go.mod` 的 `1.26.2` 为准
- Python 解析依赖明确列出 `python-docx==1.1.2`、`pdfplumber==0.11.4` 和 `python-pptx==1.0.2`
- 数据库使用 `scripts/init_knowledge_schema.sql` 初始化，不引用仓库中不存在的 `cmd/seed`
- 后端使用 `go mod download` 和 `go run ./cmd/server` 启动
- 前端进入 `design/vue`，使用 `npm ci` 和 `npm run dev` 启动
- 分别给出后端健康检查地址和前端访问地址

## 精简原则

- 删除完整环境变量对照表，改为链接 `.env.example` 与 `configs/config.yaml.example`
- API 只保留模块级入口，不在 README 罗列所有方法
- 不展开完整目录树，架构边界交由 `docs/architecture.md` 说明
- 不复制开发规范，统一链接 `docs/DEVELOPMENT.md`
- 不介绍尚不能直接运行的 Compose 示例

## 事实边界

- 仓库当前没有 `LICENSE` 文件，因此不添加许可证徽章，也不宣称采用 MIT 或其他协议
- 仓库当前没有独立 API 参考文档，因此只提供 API 概览，不添加不存在的链接
- 前端位于 `design/vue`，README 将其作为可运行界面纳入快速开始
- README 只改写项目说明，不修改业务代码、配置模板、截图或现有文档

## 验证方式

- 检查 README 中所有相对链接和截图路径确实存在
- 检查启动命令引用的目录、脚本和依赖文件确实存在
- 检查 Python 依赖版本与 `pkg/documentparser/python/requirements.txt` 一致
- 检查 Go 版本与 `go.mod` 一致
- 运行 Markdown 结构检查，并检查最终差异中没有无关改动
- README 仅包含文档变更，不要求运行 Go 测试；前端命令通过现有 `package.json` 和 Vite 配置进行静态核对
