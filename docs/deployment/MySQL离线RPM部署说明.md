# FTC 平台外部 MySQL 8.4 离线 RPM 部署说明

## 1. 适用范围

本交付用于在独立服务器上离线安装 FTC 平台所需的 MySQL Community 8.4.9。

| 操作系统 | CPU 架构 | 支持状态 |
| --- | --- | --- |
| RHEL、Rocky、AlmaLinux、Oracle Linux 8 | x86_64 | 支持 |
| RHEL、Rocky、AlmaLinux、Oracle Linux 9 | x86_64 | 支持 |
| CentOS/RHEL 7 | x86_64 | 不支持，应先升级操作系统 |
| EL8/EL9 | ARM64/aarch64 | 本交付暂不包含 |

RPM来自 MySQL官方 Community仓库，固定为 8.4.9。入口脚本不联网、不添加 Yum仓库，也不会下载任何软件。

## 2. 交付目录

```text
mysql-rpm/
├── install-mysql.sh
├── RPM-GPG-KEY-mysql-2025
├── SHA256SUMS
└── packages/
    ├── el8/x86_64/*.rpm
    └── el9/x86_64/*.rpm
```

每套包含 server、client、client-plugins、common、libs、libs-compat 和 icu-data-files，不包含 debug、test、source RPM。

## 3. 生产前提

承载纳管 60+ 集群时，数据库服务器建议从以下资源起步：

- 8–16 vCPU；
- 16–32 GiB 内存；
- 500 GiB SSD/NVMe；
- 数据盘保留至少 30% 可用空间；
- Kubernetes节点/平台 Pod 到数据库 TCP 3306 网络可达；
- 时间同步正常，系统时区可任意，但 MySQL使用 UTC；
- 已准备独立备份位置和恢复演练计划。

入口脚本安装的是一台外部 MySQL服务器，不会自动构建数据库高可用。生产环境必须在上线前选择企业虚拟机 HA、存储快照、MySQL主备或托管数据库等方案，并完成故障恢复演练。

## 4. 安装前检查

在数据库服务器执行：

```bash
cat /etc/os-release
uname -m
free -h
df -h /var/lib
rpm -qa | grep -Ei 'mysql|mariadb'
```

如果已有 MariaDB/MySQL Server 或 `/var/lib/mysql` 存在数据，脚本会停止。不要为了通过检查直接删除旧软件或数据目录；先备份、导出并评审迁移方案。

目标系统需由操作系统安装介质提供 MySQL RPM所依赖的基础包。若离线安装报告缺少 `libaio`、`openssl-libs`、`perl`、`numactl-libs` 等依赖，应从相同 EL大版本的系统 ISO/内部源补齐，不能混用 EL7/EL8/EL9 RPM。

## 5. 校验交付文件

```bash
cd mysql-rpm
sha256sum -c SHA256SUMS
```

全部显示 `OK` 后再安装。

## 6. 一键安装与初始化

```bash
cd mysql-rpm
chmod +x install-mysql.sh
sudo ./install-mysql.sh
```

脚本自动完成：

1. 识别 EL8/EL9 和 x86_64；
2. 检查已有数据库和数据目录；
3. 导入 MySQL官方 GPG公钥并校验 RPM签名；
4. 使用本地 RPM离线安装 MySQL 8.4.9；
5. 根据物理内存设置 InnoDB Buffer Pool；
6. 启动并设置 `mysqld` 开机启动；
7. 随机生成 root 与平台账号密码；
8. 创建 `ftc_platform` 数据库和 `ftc@'%'`；
9. 保存凭据并验证平台账号连接。

自定义监听地址或端口：

```bash
sudo ./install-mysql.sh --bind-address 10.20.30.50 --port 3306
```

自动化环境确认风险后使用：

```bash
sudo ./install-mysql.sh --yes
```

只检查当前交付内容，不安装：

```bash
./install-mysql.sh --dry-run
```

## 7. 凭据与平台配置

默认凭据保存在：

```text
/root/ftc-mysql-credentials.txt
```

文件权限为 `0600`，内容包括数据库地址候选、端口、数据库、平台用户名、平台密码和 root 密码。读取后应立即录入企业密码管理系统，不得放入 Git、工单、聊天记录或普通共享目录。

平台初始化页面填写：

| 字段 | 值 |
| --- | --- |
| 地址 | 数据库服务器对 Kubernetes节点/Pod 可达的 IP 或 DNS |
| 端口 | 默认 `3306` |
| 数据库 | `ftc_platform` |
| 用户名 | `ftc` |
| 密码 | 凭据文件中的“平台密码” |
| TLS | 未配置企业证书时选择关闭；生产建议完成证书配置后选择必须 |

平台 Pod 中不能使用 `127.0.0.1` 连接外部数据库。

## 8. 默认生产参数

配置文件位置：

```text
/etc/my.cnf.d/ftc-platform.cnf
```

入口脚本写入：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `character-set-server` | `utf8mb4` | 平台中文与 Kubernetes资源数据 |
| `collation-server` | `utf8mb4_0900_ai_ci` | 与平台建库规则一致 |
| `default-time-zone` | `+00:00` | 平台统一使用 UTC |
| `max_connections` | `300` | API、Collector、Worker和升级余量 |
| `max_allowed_packet` | `64M` | 大批量事件与报告数据 |
| `innodb_buffer_pool_size` | 物理内存 60%，上限 24 GiB | 为连接和系统缓存保留空间 |
| `innodb_redo_log_capacity` | `4G` | 写入突发缓冲 |
| `sync_binlog` | `1` | 降低宕机后的 binlog丢失风险 |
| `binlog_expire_logs_seconds` | `604800` | binlog保留 7 天 |
| `slow_query_log` | `ON` | 支持容量与性能排障 |
| `long_query_time` | `1` | 超过 1 秒记录慢查询 |

修改参数后先校验，再重启：

```bash
sudo mysqld --validate-config
sudo systemctl restart mysqld
sudo systemctl status mysqld --no-pager
```

## 9. 防火墙与网络

脚本不会自动开放 3306。仅允许 Kubernetes节点或平台 Pod出口网段访问，禁止向办公网和公网全量开放。

使用 firewalld 的示例，网段必须替换为实际平台出口网段：

```bash
sudo firewall-cmd --permanent \
  --add-rich-rule='rule family="ipv4" source address="10.20.0.0/16" port protocol="tcp" port="3306" accept'
sudo firewall-cmd --reload
```

从 Kubernetes中的临时诊断 Pod验证网络时，不要把密码写进命令历史；优先在初始化页面使用“测试数据库连接”。

## 10. 运行检查

```bash
sudo systemctl status mysqld --no-pager
sudo journalctl -u mysqld --since '30 minutes ago'
sudo ss -lntp | grep 3306
sudo mysqladmin -uroot -p ping
```

数据库内部检查：

```sql
SELECT VERSION();
SHOW VARIABLES WHERE Variable_name IN
  ('character_set_server','collation_server','time_zone','max_connections','innodb_buffer_pool_size');
SHOW DATABASES LIKE 'ftc_platform';
SHOW GRANTS FOR 'ftc'@'%';
```

## 11. 备份与恢复

上线前至少完成一次全量备份和恢复演练。基础逻辑备份示例：

```bash
mysqldump --single-transaction --routines --triggers \
  -uroot -p ftc_platform | gzip > ftc_platform-$(date +%F-%H%M%S).sql.gz
```

恢复必须在隔离环境先验证：

```bash
gunzip -c ftc_platform-YYYY-MM-DD-HHMMSS.sql.gz \
  | mysql -uroot -p ftc_platform
```

60+ 集群生产环境不应只依赖服务器本地的单份 SQL文件。备份需要复制到独立故障域，并监控备份结果、文件大小、保留期和定期恢复成功率。

## 12. 卸载限制

交付包不提供自动卸载命令，因为删除 RPM、`/var/lib/mysql` 或凭据可能造成不可恢复的数据丢失。确需卸载时，先完成平台停机、全量备份、恢复验证和变更审批，再由 DBA按企业流程处理。
