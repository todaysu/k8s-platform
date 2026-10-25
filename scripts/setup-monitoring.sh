#!/usr/bin/env bash
# 在 kind 集群部署 Prometheus + Alertmanager（含 node-exporter、kube-state-metrics）。
#
# 用法：
#   ./scripts/setup-monitoring.sh                      # 默认集群 ftc-manual-v1
#   CLUSTER=my-cluster ./scripts/setup-monitoring.sh
#
# 脚本做三件事，顺序不能颠倒：
#   1. 把本地 docker 镜像导入 kind 节点（kind 节点看不到宿主机的镜像库）
#   2. 从 control-plane 节点导出 etcd 客户端证书，创建 Secret
#   3. 应用清单
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cluster="${CLUSTER:-ftc-manual-v1}"
namespace="monitoring"
control_plane="${cluster}-control-plane"

prometheus_image="${PROMETHEUS_IMAGE:-quay.io/prometheus/prometheus:v3.11.1}"
alertmanager_image="${ALERTMANAGER_IMAGE:-quay.io/prometheus/alertmanager:v0.31.1}"
node_exporter_image="${NODE_EXPORTER_IMAGE:-quay.io/prometheus/node-exporter:v1.11.0}"
ksm_image="${KSM_IMAGE:-registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.18.0}"

echo "==> 目标集群：${cluster}"
if ! kind get clusters | grep -qx "${cluster}"; then
  echo "未找到 kind 集群 ${cluster}" >&2
  exit 1
fi

echo "==> [1/3] 导入镜像到节点"
for image in "${prometheus_image}" "${alertmanager_image}" "${node_exporter_image}" "${ksm_image}"; do
  if docker exec "${control_plane}" crictl images 2>/dev/null | grep -qF "$(echo "${image}" | sed 's|.*/||')"; then
    echo "    已存在：${image}"
    continue
  fi
  if ! docker image inspect "${image}" >/dev/null 2>&1; then
    echo "    宿主机缺少镜像，尝试拉取：${image}"
    docker pull "${image}"
  fi
  echo "    导入：${image}"
  kind load docker-image "${image}" --name "${cluster}"
done

echo "==> [2/3] 导出 etcd 客户端证书"
# etcd 只监听回环与节点 IP，必须带客户端证书才能抓取指标。
# 证书来自 kind 节点内的 /etc/kubernetes/pki/etcd/。
work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT

for item in "ca.crt:ca.crt" "healthcheck-client.crt:client.crt" "healthcheck-client.key:client.key"; do
  src="${item%%:*}"
  dst="${item##*:}"
  docker exec "${control_plane}" cat "/etc/kubernetes/pki/etcd/${src}" > "${work_dir}/${dst}"
done

kubectl create namespace "${namespace}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "${namespace}" create secret generic etcd-client-certs \
  --from-file=ca.crt="${work_dir}/ca.crt" \
  --from-file=client.crt="${work_dir}/client.crt" \
  --from-file=client.key="${work_dir}/client.key" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null
echo "    Secret etcd-client-certs 已就绪"

echo "==> [3/3] 应用清单"
kubectl apply -f "${repo_root}/deploy/monitoring/manifests.yaml"

echo
echo "等待工作负载就绪..."
kubectl -n "${namespace}" rollout status deployment/prometheus --timeout=5m
kubectl -n "${namespace}" rollout status deployment/alertmanager --timeout=5m
kubectl -n "${namespace}" rollout status deployment/kube-state-metrics --timeout=5m
kubectl -n "${namespace}" rollout status daemonset/node-exporter --timeout=5m

echo
kubectl -n "${namespace}" get pod,svc
echo
echo "本机访问（kind 在 macOS 上无法直连 NodePort，用端口转发）："
echo "  kubectl -n ${namespace} port-forward svc/prometheus 9090:9090"
echo "  kubectl -n ${namespace} port-forward svc/alertmanager 9093:9093"
echo
echo "平台侧：监控页应能自动发现 monitoring/prometheus 数据源。"
