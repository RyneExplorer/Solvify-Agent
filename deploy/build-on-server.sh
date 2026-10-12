#!/bin/bash
# 在服务器上直接构建镜像并导入 k3s（全程国内，绕开海外限速）。
# 前提：源码已解压到 /opt/solvify-agent/src（见 README 或部署说明）。
# 用法（root）： bash /opt/solvify-agent/src/deploy/build-on-server.sh
set -euo pipefail

export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
NS="solvify-agent"
SRC_DIR="/opt/solvify-agent/src"
BACKEND_IMG="docker.io/library/solvify-agent-backend:local"
FRONTEND_IMG="docker.io/library/solvify-agent-frontend:local"

cd "$SRC_DIR"

echo "==> [1/4] 构建后端镜像（基础镜像经 DaoCloud 加速，约 5-10 分钟）"
docker build -f deploy/Dockerfile -t "$BACKEND_IMG" .

echo "==> [2/4] 构建前端镜像（约 3-8 分钟）"
docker build -f design/vue/Dockerfile -t "$FRONTEND_IMG" design/vue

echo "==> [3/4] 导入 k3s containerd"
docker save "$BACKEND_IMG"  | k3s ctr -n k8s.io images import -
docker save "$FRONTEND_IMG" | k3s ctr -n k8s.io images import -

echo "==> [4/4] 更新 Deployment 使用本地镜像并等待就绪"
kubectl -n "$NS" set image deployment/backend backend="$BACKEND_IMG"
kubectl -n "$NS" set image deployment/frontend frontend="$FRONTEND_IMG"
kubectl -n "$NS" patch deployment/backend  -p '{"spec":{"template":{"spec":{"containers":[{"name":"backend","imagePullPolicy":"Never"}]}}}}'
kubectl -n "$NS" patch deployment/frontend -p '{"spec":{"template":{"spec":{"containers":[{"name":"frontend","imagePullPolicy":"Never"}]}}}}'
kubectl -n "$NS" rollout restart deployment/backend deployment/frontend

kubectl -n "$NS" rollout status deployment/backend  --timeout=300s
kubectl -n "$NS" rollout status deployment/frontend --timeout=300s

echo "==> 全部就绪！前端访问： http://62.234.218.164:18888/"
