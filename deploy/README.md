# Docker Compose 生产部署

本目录用于将 Solvify-Agent 部署到一台 Linux 服务器。生产环境包含 Vue/Nginx 前端、Go 后端、PostgreSQL/pgvector、Redis。发布脚本在 PostgreSQL 容器内执行备份和一次性增量升级。

## 默认约定

- GitHub 只使用 `main` 分支
- Pull Request 执行 CI，不部署
- `main` Push 在 CI 成功后发布两个公开 GHCR 镜像并部署
- 宿主机 `18888` 映射前端容器 `80`
- 后端、PostgreSQL 和 Redis 不发布宿主机端口
- 默认部署目录为 `/opt/solvify-agent`
- 默认目标平台为 `linux/amd64`

## 1. 准备服务器

服务器需要安装：

- Docker Engine
- Docker Compose v2
- OpenSSH Server
- `flock`

检查命令：

```bash
docker --version
docker compose version
docker ps
```

第一次部署前，将本目录复制到服务器，然后以 root 权限初始化目录。`DEPLOY_USER` 是服务器上的 Linux 用户：

```bash
sudo DEPLOY_USER=deploy bash server-bootstrap.sh
```

如果使用云服务器默认用户：

```bash
sudo DEPLOY_USER=ubuntu bash server-bootstrap.sh
```

重新登录服务器后编辑：

```text
/opt/solvify-agent/shared/.env
/opt/solvify-agent/shared/config.yaml
```

必须替换所有 `CHANGE_ME`，并按需填写 LLM、Embedding、邮件和钉钉配置。

## 2. 配置 SSH

为部署创建专用 SSH 密钥，将公钥追加到服务器部署用户的：

```text
~/.ssh/authorized_keys
```

在 GitHub 仓库的 `Settings → Environments → production` 中创建以下 Secrets：

| 名称 | 内容 |
| --- | --- |
| `DEPLOY_HOST` | 服务器 IP 或主机名 |
| `DEPLOY_PORT` | SSH 端口，通常为 `22` |
| `DEPLOY_USER` | 服务器 Linux 部署用户 |
| `DEPLOY_SSH_KEY` | 部署专用 SSH 私钥 |
| `DEPLOY_KNOWN_HOSTS` | 已人工核验的服务器主机公钥记录 |

生成 `known_hosts` 候选记录：

```bash
ssh-keyscan -p 22 服务器地址
```

保存前需要通过云控制台或服务器管理渠道核验主机密钥指纹，不能只信任当前网络返回结果。

## 3. 发布 GHCR 镜像

工作流使用仓库自带的 `GITHUB_TOKEN` 发布：

```text
ghcr.io/<owner>/<repo>-backend:<commit-sha>
ghcr.io/<owner>/<repo>-frontend:<commit-sha>
```

第一次发布后，进入两个 Package 的设置，将可见性改为 `Public`。公开镜像允许服务器匿名拉取，因此服务器不保存 GHCR Token。

## 4. 首次部署

服务器初始化和生产配置填写完成后，推送或合并代码到 `main`。工作流会：

1. 运行 Go 测试和静态检查
2. 构建 Vue 前端
3. 验证 Shell、Compose 和 Dockerfile
4. 发布 SHA 与 `latest` 标签
5. 上传部署包并调用 `deploy.sh`
6. 数据服务就绪后备份数据库、校验归档，按 `migrations.list` 升级已有库
7. 更新后端和前端，检查 `http://<DEPLOY_HOST>:18888/health`

如果第一次发布因 GHCR Package 仍为私有而失败，将 Package 改为公开后，在 Actions 页面重新运行失败的工作流。

## 5. 回滚

部署脚本将当前镜像记录保存在：

```text
/opt/solvify-agent/shared/.release.env
```

每次更新前备份为：

```text
/opt/solvify-agent/shared/.release.env.previous
```

新版本健康检查失败时，脚本会恢复上一组提交 SHA 镜像。备份或迁移失败时尚未替换应用，保留原应用容器并恢复发布记录。数据库脚本只允许向前兼容的幂等变更，不执行数据库结构降级，也不自动恢复旧备份覆盖在线数据。

### 数据库升级与备份

空数据卷首次启动仍使用 `postgres/init/001-init-schema.sql`；已有数据卷不会重新初始化。每次发布都执行以下步骤：

1. 在部署锁内，使用数据库容器的 `POSTGRES_USER`、`POSTGRES_DB` 创建 `pg_dump --format=custom` 一致性备份。
2. 校验归档目录并解码全部归档数据，完成后原子改名，保存 SHA256 文件；任一步失败都不迁移、不更新应用。
3. 单个 PostgreSQL 连接持有升级会话锁，按清单执行未应用的版本。每个版本的 SQL 与 `app_schema_migrations` 记录在同一事务内提交；失败版本回滚，已成功版本保留，重试跳过已应用版本，避免重复覆盖后续模型/MCP配置编辑。
4. 核验消息向量任务的六个字段、MCP 连接/模板和上下文产物表后，才更新后端。

备份位于 `/opt/solvify-agent/shared/backups/<UTC时间>-<提交SHA>.<随机后缀>.dump`，目录权限 `0700`，文件权限 `0600`。同时保留 `.sha256` 和私有 `.migration.log`；数据库错误不直接写入 Actions 日志。备份包含凭据等业务数据，不提交 Git，也不上传到 CI。备份不会自动清理，请根据磁盘容量设置独立保留策略，并将重要备份复制到受控的异机存储。它只包含数据库，不包含上传文件、Redis或服务器配置。

校验通过表示归档可完整读取；生产发布不会自动创建恢复库。CI 使用隔离容器验证备份实际恢复、新库初始化、旧库升级、重复升级、失败事务回滚。需要恢复时，先停止相关写入、核对目标数据库和备份时间，在独立空库演练并核对数据后再切换；不要直接向在线库执行覆盖恢复。

新增迁移需加入 `deploy/migrations.list`，文件存放于 `scripts/migrations/`。SQL仅包含一个独立外层 `BEGIN;` / `COMMIT;` 事务，不能嵌套事务或使用 `CREATE INDEX CONCURRENTLY` 等事务外语句。脚本在发布时统一封装版本记录；历史已应用版本不重复执行。迁移事务锁等待最多30秒、每条语句最多5分钟。需要长时间或破坏性升级时单独设计维护步骤，不能直接加入自动发布。

## 6. 常用运维命令

```bash
cd /opt/solvify-agent/current

docker compose \
  --project-name solvify-agent \
  --env-file /opt/solvify-agent/shared/.env \
  --env-file /opt/solvify-agent/shared/.release.env \
  --file compose.prod.yaml \
  ps
```

查看应用日志：

```bash
docker compose \
  --project-name solvify-agent \
  --env-file /opt/solvify-agent/shared/.env \
  --env-file /opt/solvify-agent/shared/.release.env \
  --file compose.prod.yaml \
  logs --tail=100 backend frontend
```

