# FTC 平台简明安装步骤

按照 [普通 Kubernetes 五步安装指南](install.md) 操作：生成私有清单、修改唯一镜像地址、`kubectl apply`、在 `https://<节点IP>:30080` 完成初始化，然后通过 `https://<域名>:30443` 使用平台。

需要已有 MySQL 8.0+、一个可解析到集群节点的域名，以及 Kubernetes 安装权限。页面会自动生成正式 HTTPS 证书并引导下载 CA。
