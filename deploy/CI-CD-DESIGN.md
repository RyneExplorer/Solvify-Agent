# Solvify-Agent CI/CD 设计

## 1. 背景

当前仓库只有基础 Go 测试工作流和一份不可直接运行的 Compose 示例，缺少前后端生产镜像、完整服务编排、远程部署、健康检查和失败回滚能力。本设计面向一台 Linux 生产服务器，将 Vue 前端、Go 后端、PostgreSQL（pgvector）和 Redis 全部交由 Docker Compose 管理，并在代码进入 `main` 后自动部署。

## 2. 目标

- Pull Request 合入前验证 Go 和 Vue 项目
- `main` 分支更新后构建并发布前后端生产镜像
- 通过 SSH 将指定提交版本部署到单台 Linux 服务器
- 使用 Docker Compose 编排前端、后端、PostgreSQL 和 Redis
- 持久化数据库、缓存、上传文件和日志
- 部署完成后自动执行健康检查，失败时恢复上一应用版本
- 所有密钥通过 GitHub Secrets 或服务器本地受限文件注入

## 3. 非目标

- 不配置域名、HTTPS、CDN 或负载均衡
- 不实现多服务器集群、Docker Swarm 或 Kubernetes
- 不承诺零停机更新
- 不自动回滚数据库结构
- 不向生产数据库导入 `scripts/seed_interface_data.sql` 中的接口测试数据

## 4. 最终架构

生产服务器只对外开放 TCP `80`。Compose 内部使用独立网络连接以下服务：

| 服务 | 镜像 | 职责 | 对外端口 |
| --- | --- | --- | --- |
| `frontend` | GHCR 前端镜像 | Nginx 托管 Vue 静态资源，并反向代理 `/api/` 和 `/health` | `80` |
| `backend` | GHCR 后端镜像 | 运行 Go API，并提供 Python 文档解析能力 | 无 |
| `postgres` | pgvector PostgreSQL 官方镜像 | 持久化业务数据和向量索引 | 无 |
| `redis` | Redis 官方镜像 | 缓存、验证码和 Token 黑名单 | 无 |
| `migrate` | pgvector PostgreSQL 官方镜像 | 部署时一次性执行幂等数据库脚本 | 无 |

`migrate` 是一次性任务，不作为常驻服务计入运行实例。后端依赖 PostgreSQL 和 Redis 健康；前端依赖后端健康。

## 5. 镜像设计

### 5.1 后端镜像

后端使用多阶段构建：

1. Go 构建阶段下载模块并编译 `cmd/server`
2. Python 运行阶段安装 `pkg/documentparser/python/requirements.txt`
3. 最终镜像只保留 Go 可执行文件、Python 运行时、解析脚本和必要系统证书
4. 容器以非 root 用户运行，通过服务器预创建并授权的目录写入上传文件和日志

运行时固定使用以下路径：

- `/app/solvify-agent`：后端可执行文件
- `/app/configs/config.yaml`：生产配置
- `/app/pkg/documentparser/python/parse_document.py`：解析脚本
- `/app/data/uploads`：上传文件
- `/app/logs`：日志

### 5.2 前端镜像

前端同样使用多阶段构建：

1. Node 构建阶段执行 `npm ci` 和 `npm run build`
2. Nginx 运行阶段只复制 `dist` 和生产 Nginx 配置
3. Nginx 对 Vue Router 使用 `try_files` 回退到 `index.html`
4. `/api/` 和 `/health` 转发至 `backend:8080`，保留 SSE 所需的流式代理设置

### 5.3 镜像命名与版本

镜像名称基于转为小写的 `${GITHUB_REPOSITORY}`：

- `ghcr.io/<repository>-backend:<commit-sha>`
- `ghcr.io/<repository>-frontend:<commit-sha>`

每次 `main` 发布同时更新 `latest` 标签。生产 Compose 始终使用不可变的提交 SHA 标签，`latest` 只用于人工查看和临时测试。

## 6. CI/CD 工作流

现有 `.github/workflows/ci.yml` 将由 `.github/workflows/ci-cd.yml` 取代，避免同一提交重复执行 Go 测试。

### 6.1 触发条件

- `pull_request` 指向 `main`：执行 CI，不发布和部署
- `push` 到 `main`：执行 CI、构建发布镜像并部署
- `workflow_dispatch`：允许人工触发所选分支或标签对应的流程

### 6.2 CI 作业

Go 作业执行：

- 恢复 Go 模块缓存
- `go test ./...`
- `go vet ./...`

Vue 作业执行：

- 使用 `design/vue/package-lock.json` 恢复 npm 缓存
- `npm ci`
- `npm run build`

Compose 与脚本作业执行：

- 使用示例配置渲染 `docker compose config`
- 使用 `bash -n` 检查 Shell 脚本语法
- 构建前后端 Docker 镜像，确保 Dockerfile 在 Pull Request 阶段即可验证

### 6.3 镜像发布作业

镜像发布仅在 `main` 的 `push` 或人工触发时执行，并依赖全部 CI 作业成功：

- 使用工作流 `GITHUB_TOKEN` 登录 GHCR
- 权限限制为 `contents: read` 和 `packages: write`
- 构建 `linux/amd64` 前后端镜像
- 推送提交 SHA 和 `latest` 两组标签
- 构建结果通过 job outputs 传递给部署作业

### 6.4 部署作业

部署作业绑定 GitHub `production` Environment，并使用固定 concurrency group，保证同一时间只有一个生产部署。部署步骤为：

1. 配置 SSH 私钥和已验证的 `known_hosts`
2. 将 Compose、数据库脚本和部署脚本同步到 `/opt/solvify-agent/releases/<commit-sha>`
3. 通过标准输入让服务器临时登录 GHCR
4. 调用服务器上的部署脚本并传入两个 SHA 镜像地址
5. 部署完成后退出 GHCR 登录
6. 从 GitHub Runner 请求 `http://<DEPLOY_HOST>/health` 做外部检查

## 7. 生产配置与密钥

### 7.1 GitHub production Environment Secrets

| Secret | 用途 |
| --- | --- |
| `DEPLOY_HOST` | 生产服务器地址 |
| `DEPLOY_PORT` | SSH 端口 |
| `DEPLOY_USER` | 部署用户 |
| `DEPLOY_SSH_KEY` | 部署专用 SSH 私钥 |
| `DEPLOY_KNOWN_HOSTS` | 预先核验的服务器主机公钥记录 |
| `GHCR_USERNAME` | GHCR 拉取账号 |
| `GHCR_PULL_TOKEN` | 仅授予 `read:packages` 的 GHCR Token |

### 7.2 服务器本地配置

`server-bootstrap.sh` 在服务器创建以下目录：

```text
/opt/solvify-agent/
├── current
├── releases/
└── shared/
    ├── .env
    ├── config.yaml
    ├── data/
    └── logs/
```

`shared/.env` 保存数据库、Redis、LLM、Embedding 和第三方集成环境变量；`shared/config.yaml` 保存 JWT、邮件和 Agent 等当前项目只能从 YAML 读取的配置。两个文件权限为 `0600`，部署流程不会覆盖它们。

仓库只提供无真实凭据的示例文件。镜像、Compose、Actions 日志和 Git 历史中不写入生产密码、Token、密钥或完整连接串。

## 8. 数据与数据库更新

- PostgreSQL、Redis 使用 Docker named volume 持久化
- 后端上传文件和日志绑定到 `/opt/solvify-agent/shared/data` 与 `shared/logs`
- PostgreSQL 首次创建数据卷时执行 `init_knowledge_schema.sql`
- 每次部署在更新应用前运行 `migrate` 一次性服务，再次执行同一幂等脚本
- `seed_interface_data.sql` 只包含接口测试数据，不进入生产初始化流程
- 后续数据库变更必须保持向前兼容，并显式使用 `ALTER ... IF NOT EXISTS` 等幂等语句

## 9. 部署与回滚

服务器部署脚本执行以下流程：

1. 检查 Docker Engine、Compose v2、生产配置和镜像变量
2. 使用 `flock` 获取服务器部署锁
3. 将当前 `.release.env` 备份为 `.release.env.previous`
4. 写入本次提交对应的新 `.release.env`
5. 拉取前后端 SHA 镜像
6. 启动 PostgreSQL 和 Redis并等待健康
7. 运行 `migrate` 服务
8. 更新后端和前端
9. 等待后端容器健康，并通过前端代理请求 `/health`
10. 成功后更新 `current` 软链接并清理无引用的本地镜像

任一步骤失败后：

- 有上一版本时恢复 `.release.env.previous`，重新启动上一版本前后端并验证健康
- 首次部署没有上一版本时停止异常应用容器，但保留 PostgreSQL、Redis 和全部数据卷
- 数据库只执行向前兼容变更，不自动执行结构降级
- 脚本以非零状态退出，使 GitHub Actions 明确标记部署失败

## 10. 健康检查与日志

- PostgreSQL 使用 `pg_isready`
- Redis 使用带密码的 `redis-cli ping`
- 后端请求容器内 `http://127.0.0.1:8080/health`
- 前端请求 Nginx 的 `/health`，同时验证反向代理和后端
- 部署超时或失败时输出 `docker compose ps` 和后端、前端的有限尾部日志
- 不输出 `.env`、`config.yaml` 或 Docker Registry 登录信息

## 11. 交付文件

```text
.github/workflows/ci-cd.yml
Dockerfile
.dockerignore
design/vue/Dockerfile
design/vue/.dockerignore
design/vue/nginx.conf
deploy/compose.prod.yaml
deploy/deploy.sh
deploy/server-bootstrap.sh
deploy/.env.production.example
deploy/config.production.yaml.example
deploy/README.md
tasks/todo.md
```

现有 `.github/workflows/ci.yml` 和无效的 `deploy/compose.yaml.example` 将被对应生产文件取代。`tasks/todo.md` 是本地实施跟踪文件，继续遵循当前 `.gitignore`，不纳入 Git。

## 12. 验证与成功标准

实现完成前必须取得以下证据：

- `go test ./...` 成功
- `go vet ./...` 成功
- `npm ci` 和 `npm run build` 成功
- 前后端 Docker 镜像构建成功
- 生产 Compose 使用示例变量执行 `docker compose config` 成功
- `bash -n deploy/deploy.sh deploy/server-bootstrap.sh` 成功
- Docker 可用时，本地启动完整 Compose 并通过 `/health` 冒烟测试
- 最终 Git diff 不包含用户当前正在修改的文档编辑功能文件

完成标准是：Pull Request 只运行 CI；`main` 更新在 CI 成功后发布两个 SHA 镜像，并能在一台已完成初始化的 Linux 服务器上自动部署、通过健康检查，或在应用更新失败时恢复上一版本。

## 13. 前置假设

- 生产服务器为 `linux/amd64`
- 服务器已安装 Docker Engine、Docker Compose v2、OpenSSH 和 `flock`
- 部署用户可执行 Docker 命令并可写 `/opt/solvify-agent`
- 防火墙允许 SSH 端口和 TCP `80`
- GHCR Token 对两个私有镜像拥有读取权限
- 当前阶段直接通过服务器地址访问，不配置 HTTPS
