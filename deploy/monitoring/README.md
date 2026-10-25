# 轻量监控栈（Prometheus + Alertmanager）

面向 kind / 小型自建集群，不含 Prometheus Operator 与 Grafana。

## 组成

| 组件 | 作用 | 资源请求 |
|---|---|---|
| Prometheus v3.11.1 | 指标采集、查询、规则评估 | 200m / 512Mi |
| Alertmanager v0.31.1 | 告警分组、抑制、通知 | 50m / 64Mi |
| node-exporter v1.11.0 | 节点 CPU/内存/磁盘/网络（DaemonSet） | 30m / 32Mi |
| kube-state-metrics v2.18.0 | K8s 对象状态（Deployment/Pod/Node） | 50m / 64Mi |

存储：Prometheus 10Gi、Alertmanager 2Gi，使用集群默认 StorageClass。

## 部署

```bash
./scripts/setup-monitoring.sh                    # 默认集群 ftc-manual-v1
CLUSTER=my-cluster ./scripts/setup-monitoring.sh
```

脚本按顺序做三件事（顺序不可颠倒）：

1. **导入镜像** —— kind 节点看不到宿主机的 docker 镜像库，必须 `kind load`。
2. **导出 etcd 客户端证书** —— etcd 只监听回环与节点 IP 且要求 mTLS。
3. **应用清单**。

## 与平台监控页的对接

平台的 `discoverPrometheusServices` 会扫描所有 Service，判定规则：

- `namespace/name/labels[app]/labels[app.kubernetes.io/name]` 拼接后**包含 `prometheus`**
- 端口为 `9090`，或端口名包含 `web` / `http`

本清单刻意满足该规则且取得最高优先级评分：

```
namespace = monitoring      (+5)
name      = prometheus      (+10)
port      = 9090, name=web  (+3)
```

平台通过 `services/proxy` 访问 `http:prometheus:web/api/v1/query`，因此**纳管凭据必须拥有 `services/proxy` 权限**，否则监控页会报 `MONITORING_FORBIDDEN`。

## 采集目标（14 个）

| job | 数量 | 说明 |
|---|---|---|
| prometheus | 1 | 自身 |
| alertmanager | 1 | 告警组件 |
| node-exporter | 3 | 每节点一个（含 control-plane） |
| kube-state-metrics | 1 | 对象状态 |
| kubelet | 3 | kubelet 自身指标 |
| kubelet-cadvisor | 3 | 容器级 CPU/内存/磁盘 |
| apiserver | 1 | 请求量、延迟、在途 |
| etcd | 1 | Leader、提案、WAL 延迟 |

etcd 目标通过节点发现自动定位 `*-control-plane` 并改写端口为 2379，**集群重建后 IP 变化不需要改配置**。

## 本地访问

macOS 上 kind 无法直连 NodePort，用端口转发：

```bash
kubectl -n monitoring port-forward svc/prometheus 9090:9090
kubectl -n monitoring port-forward svc/alertmanager 9093:9093
```

浏览器打开 <http://127.0.0.1:9090>（Prometheus）或 <http://127.0.0.1:9093>（Alertmanager）。

## 配置通知渠道

默认 Alertmanager 只保存告警不外发。要接企业微信/钉钉/邮件，编辑 `alertmanager-config` ConfigMap 的 `receivers`：

```yaml
receivers:
  - name: default
    webhook_configs:
      - url: https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=<KEY>
        send_resolved: true
```

改完执行：

```bash
kubectl apply -f deploy/monitoring/manifests.yaml
kubectl -n monitoring rollout restart deployment/alertmanager
```

## 已内置的告警规则

| 分组 | 规则 |
|---|---|
| kubernetes-node | NodeDown、NodeHighCPU、NodeLowMemory |
| kubernetes-workload | PodCrashLooping、PodNotReady |
| kubernetes-control-plane | APIServerHighLatency、EtcdNoLeader |

阈值偏保守，避免测试环境频繁抖动，按实际环境调整 `prometheus-rules` ConfigMap。

## 注意事项

- **单副本 + RWO 卷**：Prometheus 与 Alertmanager 都用 `Recreate` 策略，滚动更新会因卷占用卡住。
- **数据保留** 7 天或 8GB，取先到者。kind 节点磁盘有限，长期运行需关注。
- **node-exporter 使用 hostNetwork + hostPID**，需要 `privileged` 级别的主机访问（hostPath 挂载 /proc、/sys）。
- 若集群启用了 PodSecurity `restricted`，需要为 `monitoring` 命名空间放宽策略。
