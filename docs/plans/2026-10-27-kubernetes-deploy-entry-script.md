# 普通 Kubernetes 自动架构部署入口实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 提供一个只面向普通 Kubernetes 的 Shell 部署入口，自动识别节点 CPU 架构、提示用户提前导入对应镜像，并通过临时清单部署平台初始化服务。

**Architecture:** 脚本读取当前 Kubernetes Context 和 Ready 节点的 `kubernetes.io/arch` 标签，只接受同构的 `amd64` 或 `arm64` 集群。脚本不修改原始 `deploy/install.yaml`，而是在临时目录中替换唯一的平台镜像引用，完成客户端校验后再应用，并等待 Setup Deployment 就绪。

**Tech Stack:** Bash、kubectl、awk、sed、Kubernetes YAML

---

### Task 1: 实现部署入口脚本

**Files:**
- Create: `scripts/deploy-platform.sh`

**Step 1: 实现参数与前置检查**

- 支持 `--context`、`--version`、`--arch`、`--yes`、`--dry-run` 和 `--help`。
- 检查 `kubectl`、安装清单、Context 和集群连通性。

**Step 2: 实现架构检测与镜像提示**

- 从 Ready 节点读取架构。
- 自动选择 `ftc-platform:<版本>-amd64` 或 `ftc-platform:<版本>-arm64`。
- 混合架构或不支持的架构必须停止。
- 用中文提示用户把对应镜像导入每个可能调度平台 Pod 的节点。

**Step 3: 渲染和应用临时清单**

- 验证原始清单只包含一个平台镜像占位。
- 使用 `mktemp` 创建临时文件并注册退出清理。
- 执行 `kubectl apply --dry-run=client` 校验。
- 非 dry-run 模式应用清单并等待 Setup Deployment。
- 失败时输出 Pod、Service 和最近事件。

**Step 4: 验证脚本**

Run:

```bash
bash -n scripts/deploy-platform.sh
scripts/deploy-platform.sh --help
scripts/deploy-platform.sh --dry-run --yes
```

Expected: 语法检查通过；帮助内容为中文；dry-run 正确检测架构并显示最终镜像和渲染清单位置，不修改集群。

### Task 2: 更新安装文档

**Files:**
- Modify: `docs/deployment/install.md`

**Step 1: 增加脚本部署方式**

- 将脚本作为推荐部署入口。
- 说明脚本不负责镜像导入和 MySQL 部署。
- 给出镜像命名、执行示例和混合架构限制。

**Step 2: 校验文档和 Git 差异**

Run:

```bash
git diff --check
```

Expected: 无空白错误，脚本和文档内容一致。
