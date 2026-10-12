#!/usr/bin/env bash
#
# server-bootstrap.sh — 在目标服务器上「一次性」初始化 k3s 集群
#
# 适用：腾讯云 Lighthouse lhins-bw1sw356（Ubuntu/Debian 系，当前仅装了 Docker CE）
# 用法：
#   sudo bash deploy/k3s/server-bootstrap.sh
# 或自定义版本：
#   K3S_VERSION=v1.30.0+k3s1 INSTALL_HELM=false sudo -E bash deploy/k3s/server-bootstrap.sh
#
# 设计说明：
#   - k3s 自带 containerd 运行时，与已装的 Docker CE 共存、互不干扰（两者用不同的
#     网络和运行时，不会抢端口）。
#   - 内置 ServiceLB（klipper-lb）：自动把「节点 IP」绑定到 LoadBalancer 类型 Service，
#     因此前端 Service(type: LoadBalancer, port 18888) 无需额外装 MetalLB 即可对外暴露。
#   - 内置 local-path-provisioner：提供默认 StorageClass "local-path"，
#     清单里 PostgreSQL/Redis/Backend 的 PVC（storageClassName: local-path）可直接动态供给。
#
set -euo pipefail

# ---------- 可配置项 ----------
K3S_VERSION="${K3S_VERSION:-v1.30.0+k3s1}"   # 与 .github/workflows/ci-cd.yml 中 kubectl 版本对齐
INSTALL_HELM="${INSTALL_HELM:-true}"         # 是否顺带安装 helm（可选，部署本身不依赖）
KUBECONFIG_DIR="${KUBECONFIG_DIR:-/opt/solvify-agent}"
# --------------------------------

log() { echo "==> $*"; }

log "[1/5] 检查运行环境"
if [[ $EUID -ne 0 ]]; then
  echo "错误：请使用 root 或 sudo 运行本脚本" >&2
  exit 1
fi

if command -v k3s >/dev/null 2>&1; then
  log "k3s 已安装（$(k3s --version 2>/dev/null | head -1)），跳过安装步骤"
else
  # 若希望复用现有 Docker CE 作为运行时，可改为：
  #   curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION="$K3S_VERSION" sh -s - --docker
  # 默认使用 k3s 自带的 containerd，与 Docker CE 隔离、最稳妥。
  log "[2/5] 安装 k3s ${K3S_VERSION}"
  curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION="$K3S_VERSION" sh -
fi

log "[3/5] 等待 k3s 节点就绪"
for _ in $(seq 1 60); do
  [[ -f /etc/rancher/k3s/k3s.yaml ]] && break
  sleep 2
done
for _ in $(seq 1 60); do
  if kubectl get node 2>/dev/null | grep -q " Ready "; then break; fi
  sleep 3
done
kubectl get node

log "[4/5] 导出一份本地可用的 kubeconfig"
install -d -m 0755 "$KUBECONFIG_DIR"
cp /etc/rancher/k3s/k3s.yaml "$KUBECONFIG_DIR/k3s.yaml"
chmod 600 "$KUBECONFIG_DIR/k3s.yaml"
echo "kubeconfig 已写入：$KUBECONFIG_DIR/k3s.yaml"
echo "节点网络信息："
kubectl get nodes -o wide

if [[ "$INSTALL_HELM" == "true" ]]; then
  log "[5/5] 安装 helm"
  if command -v helm >/dev/null 2>&1; then
    echo "helm 已存在（$(helm version --short 2>/dev/null)），跳过"
  else
    curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
  fi
else
  log "[5/5] 跳过 helm 安装（INSTALL_HELM=false）"
fi

echo
echo "============================================================"
echo " k3s 初始化完成 ✅"
echo "============================================================"
echo " 后续步骤："
echo "   1) 腾讯云防火墙放通入站 TCP 18888（前端 LoadBalancer 端口）"
echo "   2) 首次部署前填写密钥： kubectl -n solvify-agent edit secret solvify-secrets"
echo "   3) 推送 main 触发 GitHub Actions 自动部署，"
echo "      或手动执行： ./deploy/k3s/apply.sh"
echo " 验证前端： http://<节点IP>:18888/   （健康检查 http://<节点IP>:18888/health）"
echo "============================================================"
