# MySQL 8.4 x86_64 离线 RPM 交付实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 为 FTC 平台外部数据库提供 EL8/EL9 x86_64 的 MySQL 8.4 LTS 离线 RPM、统一安装入口和生产配置文档。

**Architecture:** 官方 RPM按 EL8/EL9 隔离存放，入口脚本读取 `/etc/os-release` 与 `uname -m` 后选择对应目录。脚本拒绝覆盖已有 MySQL/MariaDB，离线安装后生成生产参数、随机凭据、平台数据库和最小权限账号，并将凭据保存为仅 root 可读文件。

**Tech Stack:** Bash、RPM/DNF/YUM、systemd、MySQL 8.4、SHA-256

---

### Task 1: 准备官方离线 RPM

**Files:**
- Create in delivery: `mysql-rpm/packages/el8/x86_64/*.rpm`
- Create in delivery: `mysql-rpm/packages/el9/x86_64/*.rpm`
- Create in delivery: `mysql-rpm/RPM-GPG-KEY-mysql-2025`
- Create in delivery: `mysql-rpm/SHA256SUMS`

**Steps:**

1. 从 MySQL官方仓库下载固定版本 8.4.9 的 server、client、client-plugins、common、libs、libs-compat 和 icu-data-files。
2. 下载官方 2025 GPG公钥。
3. 生成 SHA-256 清单并逐项验证。

### Task 2: 实现统一安装入口

**Files:**
- Create: `scripts/install-mysql-offline.sh`

**Steps:**

1. 检查 root、x86_64、EL8/EL9、RPM目录和系统依赖。
2. 检测已有 MySQL/MariaDB，存在时停止，禁止自动删除旧数据库。
3. 导入官方 GPG公钥并用本地 RPM离线安装。
4. 按服务器内存生成 `/etc/my.cnf.d/ftc-platform.cnf`。
5. 启动 MySQL，从日志读取一次性 root 密码并完成安全初始化。
6. 随机生成 root/应用密码，创建 `ftc_platform` 和 `ftc@'%'`，保存 0600 凭据文件。
7. 输出平台初始化页面所需参数、防火墙提示和验证命令。
8. 支持 `--dry-run`，用于无 EL目标机时验证检测与文件选择逻辑。

### Task 3: 编写部署和配置文档

**Files:**
- Create: `docs/deployment/MySQL离线RPM部署说明.md`
- Modify: `docs/deployment/install.md`
- Modify: `docs/release/v1.1.0-delivery.md`

**Steps:**

1. 说明支持矩阵、安装前检查、入口命令、配置参数和凭据位置。
2. 说明 MariaDB迁移、端口、防火墙、SELinux、备份、恢复与平台连接方法。
3. 明确单机外部 MySQL不是数据库高可用，生产应由虚拟机/存储/备份体系提供恢复能力。

### Task 4: 验证和交付

**Steps:**

1. 运行 `bash -n scripts/install-mysql-offline.sh`。
2. 用模拟 `/etc/os-release` 参数验证 EL8、EL9、错误架构和已安装数据库路径。
3. 运行 `git diff --check`。
4. 从交付目录执行 RPM SHA-256校验。
5. 提交 Git；RPM二进制只保留在忽略的交付目录，不提交仓库。
