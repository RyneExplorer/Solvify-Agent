#!/usr/bin/env bash
#
# 在已配置好 kubectl（指向目标 k3s）的机器上，一键应用全部清单。
# 用法：
#   ./deploy/k3s/apply.sh
#
# 前置：
#   1) 已经安装 k3s 且 kubectl 可用（KUBECONFIG 指向 /etc/rancher/k3s/k3s.yaml 或本目录同级 k3s.yaml）
#   2) 已填写密钥：kubectl -n solvify-agent edit secret solvify-secrets
#   3)（可选）如需初始化数据库表，先创建 pg-init：
#      kubectl -n solvify-agent create configmap pg-init \
#        --from-file=001-init-schema.sql=scripts/init_knowledge_schema.sql \
#        --dry-run=client -o yaml | kubectl apply -f -
set -euo pipefail

DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
NS="solvify-agent"

echo "==> 应用命名空间"
kubectl apply -f "$DIR/00-namespace.yaml"

echo "==> 应用密钥与配置"
kubectl -n "$NS" apply -f "$DIR/01-secrets.yaml"
kubectl -n "$NS" apply -f "$DIR/02-configmap.yaml"

echo "==> 应用 PostgreSQL / Redis"
kubectl -n "$NS" apply -f "$DIR/03-postgres.yaml"
kubectl -n "$NS" apply -f "$DIR/04-redis.yaml"

echo "==> 应用 backend / frontend"
kubectl -n "$NS" apply -f "$DIR/05-backend.yaml"
kubectl -n "$NS" apply -f "$DIR/06-frontend.yaml"

echo "==> 等待滚动更新完成"
kubectl -n "$NS" rollout status deployment/backend --timeout=180s
kubectl -n "$NS" rollout status deployment/frontend --timeout=180s

echo "==> 部署完成。前端访问地址（LoadBalancer 绑定节点 IP）："
kubectl -n "$NS" get svc frontend -o wide
