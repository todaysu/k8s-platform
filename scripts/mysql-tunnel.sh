#!/usr/bin/env bash
# 把 kind 集群里的 MySQL 隧道到本机 127.0.0.1，供 Navicat / DBeaver / mysql 客户端连接。
#
# 为什么需要隧道：
#   macOS 的 Docker Desktop 把 kind 节点跑在虚拟机内的 docker 网桥里
#   （本集群为 172.18.0.0/16）。宿主机没有到该网段的路由，连接会被发到
#   默认网关并丢弃，因此直接连 <节点IP>:<NodePort> 一定失败。
#   port-forward 走的是 Kubernetes API 的隧道，不受此限制。
#
# 用法：
#   ./scripts/mysql-tunnel.sh                  # 默认 127.0.0.1:33066
#   LOCAL_PORT=13306 ./scripts/mysql-tunnel.sh # 自定义本机端口
#   NAMESPACE=ftc-database SERVICE=mysql ./scripts/mysql-tunnel.sh
#
# 保持本终端打开；Ctrl-C 结束隧道。
set -euo pipefail

namespace="${NAMESPACE:-ftc-database}"
service="${SERVICE:-mysql}"
local_port="${LOCAL_PORT:-33066}"
remote_port="${REMOTE_PORT:-3306}"

echo "隧道：127.0.0.1:${local_port} -> ${namespace}/${service}:${remote_port}"
echo "客户端填写：主机 127.0.0.1  端口 ${local_port}"
echo "按 Ctrl-C 结束。"
echo

exec kubectl -n "${namespace}" port-forward --address 127.0.0.1 "svc/${service}" "${local_port}:${remote_port}"
