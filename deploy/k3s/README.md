# Solvify-Agent · k3s 部署

用 k3s（单节点 Kubernetes）替代原来的 Docker Compose 来编排整套服务：PostgreSQL(pgvector)、Redis、后端(Go)、前端(Vue/Nginx)。CI/CD 仍由 GitHub Actions 驱动，区别是部署环节改为登录 k3s 节点执行 `kubectl apply`。

## 1. 目标服务器

当前使用的轻量服务器（腾讯云 Lighthouse）：

- 实例：`lhins-bw1sw356`（名称 Docker CE-Mxx9）
- 地域：北京 (ap-beijing-7)，公网 `62.234.218.164`
- 现状：已安装 k3s（实际版本 `v1.36.3+k3s1`，k3s v1.30+ 均兼容本套清单）；早期曾仅装 Docker CE

## 2. 在服务器上准备 k3s（一次性）

推荐直接用仓库提供的脚本（封装了安装、等待就绪、导出 kubeconfig、可选装 helm）：

```bash
# 在目标服务器上以 root/sudo 执行
sudo bash deploy/k3s/server-bootstrap.sh
```

脚本默认安装 `v1.30.0+k3s1`（与 CI 中 `kubectl` 客户端版本一致的保守取值）；实际已装上更新的 `v1.36.3+k3s1`，本套清单使用的是 `apps/v1`、`v1` 等稳定 API，在 k3s v1.30+ 任意版本均可正常 apply。如要复用已装的 Docker CE 作运行时，按脚本内注释加 `--docker` 即可。

如需手动操作：

```bash
# 安装 k3s（自带 containerd + 内置 ServiceLB + local-path 存储）
curl -sfL https://get.k3s.io | sh -

# 让部署账号能读取 kubeconfig（默认 600 属主 root）
sudo cp /etc/rancher/k3s/k3s.yaml /opt/solvify-agent/k3s.yaml
sudo chmod 600 /opt/solvify-agent/k3s.yaml
```

> GitHub Actions 的 deploy 步骤通过 SSH 登录后使用 `sudo KUBECONFIG=/etc/rancher/k3s/k3s.yaml kubectl ...` 执行，前提是部署账号拥有免密 sudo。

## 3. 填写密钥（首次部署前必做，仅一次）

仓库里的 `01-secrets.yaml` 是含 `CHANGE_ME_*` 占位符的模板，**不提交真实密钥**。真实密钥用一个仓库外的本地文件 `01-secrets.local.yaml`（已生成强随机值、已被 `.gitignore` 忽略）一次性 apply：

```bash
# 在本地（kubeconfig 指向目标 k3s）执行，或直接 scp 到服务器跑：
kubectl apply -f deploy/k3s/01-secrets.local.yaml

# 让 postgres / redis / backend 重新加载新密钥
kubectl -n solvify-agent rollout restart statefulset/postgres deployment/redis deployment/backend
```

> CI 的 deploy 步骤已改为「secret 仅在集群中不存在时创建」，所以**重复推送 main 不会把真实密钥冲掉**。若你改了 `01-secrets.local.yaml` 想生效，重新 apply 上面两条命令即可。

替代方案（不想用本地文件）：`kubectl -n solvify-agent edit secret solvify-secrets` 手工改 `POSTGRES_PASSWORD` / `REDIS_PASSWORD` / `config.yaml` 里的 `jwt.secret`（注意 edit 看到的是 base64，需先 `echo -n '值' | base64`）。至少这三项必须设置。

## 4. 镜像来源

后端/前端镜像推送到 **Docker Hub 公开镜像**。国内服务器通过已配置的 DaoCloud 镜像 `docker.m.daocloud.io` 加速拉取（规避 ghcr.io 从国内拉取被限速到 ~100KB/s 的问题）。仓库需为 Public，否则请在 `05-backend.yaml` / `06-frontend.yaml` 增加 `imagePullSecrets`。

镜像标签格式（与 CI/CD 中保持一致）：

- `docker.io/<DOCKERHUB_USERNAME>/solvify-agent-backend:<commit-sha>`
- `docker.io/<DOCKERHUB_USERNAME>/solvify-agent-frontend:<commit-sha>`

## 5. 部署方式

### 方式 A：GitHub Actions（推荐，自动）

推送 `main` 后，Actions 会：构建前后端镜像 → 注入 SHA 标签 → SSH 上传 `deploy/k3s/*` 与初始化 SQL → `kubectl apply`。所需 GitHub Secrets：`DEPLOY_HOST` / `DEPLOY_PORT` / `DEPLOY_USER` / `DEPLOY_SSH_KEY` / `DEPLOY_KNOWN_HOSTS`，以及镜像仓库 `DOCKERHUB_USERNAME` / `DOCKERHUB_TOKEN`。

### 方式 B：手动

```bash
# 在项目根目录，且 kubectl 已指向目标 k3s
# 1) 建库表初始化 ConfigMap（可省，省略则仅不自动建表）
kubectl -n solvify-agent create configmap pg-init \
  --from-file=001-init-schema.sql=scripts/init_knowledge_schema.sql \
  --dry-run=client -o yaml | kubectl apply -f -

# 2) 应用全部清单（注意 backend/frontend 镜像占位符需先替换）
./deploy/k3s/apply.sh
```

## 6. 外部访问

前端 `Service` 为 `LoadBalancer`，k3s 内置 ServiceLB 会把节点 IP 绑定到 `18888`：

- 访问：`http://<节点IP>:18888/`
- 健康检查：`http://<节点IP>:18888/health`

如需固定端口可改用 `type: NodePort`。

## 7. 清单一览

| 文件 | 内容 |
| --- | --- |
| `00-namespace.yaml` | 命名空间 `solvify-agent` |
| `01-secrets.yaml` | Secret：密码/密钥 + `config.yaml` 文件 |
| `02-configmap.yaml` | ConfigMap：非敏感环境变量 |
| `03-postgres.yaml` | PostgreSQL StatefulSet + PVC + Service |
| `04-redis.yaml` | Redis Deployment + PVC + Service |
| `05-backend.yaml` | 后端 Deployment + PVC + Service |
| `06-frontend.yaml` | 前端 Deployment + LoadBalancer Service |
| `apply.sh` | 本地一键应用脚本 |
