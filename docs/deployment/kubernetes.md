# Kubernetes 部署入口

普通 Kubernetes 的正式交付统一使用 [五步安装指南](install.md)。该方案使用平台自身 HTTPS + NodePort、已有 MySQL 8.0+ 和浏览器初始化，不依赖 Ingress。

本地开发可用 `make dev-backend`、`make dev-frontend` 与 SQLite；开发环境和历史清单不用于新的生产交付。
