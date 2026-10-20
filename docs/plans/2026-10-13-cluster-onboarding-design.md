# Kubernetes 多集群平台：集群纳管功能设计

> 2026-10-16 决策更新：新集群采用单步纳管。点击“纳管集群”后，平台一次完成解析、连接验证、凭据加密和持久化，不再创建待确认草稿。具体实现以 `2026-10-16-direct-cluster-onboarding.md` 为准。

## 1. 背景

平台面向 Kubernetes 集群运维人员，目标是维护多集群稳定性，不承担业务发布、流量管理和有状态业务编排。平台采用轻量设计，一次只实现一个完整功能。

首个功能是“集群纳管”：管理员通过集群名称和 Kubeconfig，将一套现有 Kubernetes 集群接入平台。

技术架构参考 CNCF Headlamp，但不直接 Fork Headlamp。平台采用 React + TypeScript 前端、Go 单体后端和 Kubernetes 官方 `client-go`。第一版不引入 Headlamp 的 Electron、插件、Helm、通用资源编辑和桌面应用能力。

```text
浏览器（React + TypeScript + MUI）
              ↓ JSON API
Go 单体后端（认证、集群纳管、Kubernetes 访问）
        ↓                          ↓
    SQLite/PostgreSQL          多集群 API Server
```

生产构建时，Go 服务同时托管编译后的前端静态文件，最终仍然只有一个容器和一个服务。

## 2. 用户与范围

第一版只有一个管理员账号，不开放注册，也不实现角色管理。

本功能包括：

- 管理员登录。
- 上传 Kubeconfig 文件或粘贴 Kubeconfig 文本。
- 自动选择 Kubeconfig 的 `current-context`。
- 解析 API Server、CA 证书和内嵌认证凭据。
- 测试连接并获取 Kubernetes 版本。
- 确认纳管。
- 查看集群基础信息。
- 修改集群显示名称。
- 更新 Kubeconfig。
- 取消纳管。
- 记录关键操作。

本功能不包括节点、Pod、Event、健康评分、告警、容器 Exec 和多用户权限。

## 3. 核心流程

### 3.1 添加集群

1. 管理员进入集群列表，点击“添加集群”。
2. 输入唯一的集群显示名称。
3. 选择上传文件或粘贴内容。
4. 点击“测试连接”。
5. 后端解析 `current-context`，找到关联的 cluster 和 user。
6. 后端提取 API Server、内嵌 CA，以及 Bearer Token 或客户端证书凭据。
7. 后端使用 TLS 校验访问 Kubernetes `/version`。
8. 测试成功后，创建十分钟有效的临时导入记录。
9. 页面展示 API Server、Context、Kubernetes 版本和认证方式，不展示任何凭据。
10. 管理员点击“确认纳管”。
11. 后端重新检查临时记录有效性和重复集群，将其转为正式集群记录，并删除临时记录。

### 3.2 更新凭据

1. 管理员从集群详情进入“更新 Kubeconfig”。
2. 上传或粘贴新配置并测试。
3. 测试失败时保留旧凭据。
4. 新 API Server 与原地址不一致时显示警告。
5. 管理员确认后，原子替换 CA、Token、Context 和版本信息。
6. 删除旧的加密凭据，并记录操作日志。

### 3.3 取消纳管

管理员必须再次输入集群名称确认。操作仅删除平台记录、临时记录和平台保存的凭据，不访问或删除真实 Kubernetes 集群。

## 4. Kubeconfig规则

- 文件上传和文本粘贴二选一。
- 支持 `.yaml`、`.yml` 和无扩展名文件。
- 最大文件大小为 1 MiB。
- 必须存在有效的 `current-context`，不自动猜测其他 Context。
- 第一版支持直接内嵌的 Bearer Token，或内嵌的客户端证书与私钥。
- 第一版只支持 `certificate-authority-data`，不支持本地 CA 文件路径。
- 不支持 `exec`、`auth-provider`、`tokenFile`、用户名密码，以及引用本地文件路径的证书认证。
- API Server 必须使用 HTTPS。
- 原始 Kubeconfig 不长期保存。

## 5. 页面

### 5.1 登录页

- 用户名和密码。
- 不提供注册入口。
- 未登录用户访问业务页面时跳转登录页。

### 5.2 集群列表

- 集群名称。
- Kubernetes 版本。
- 连接状态。
- 录入时间。
- “添加集群”和“查看”入口。
- 没有集群时展示引导空状态。

### 5.3 添加和更新页面

- 集群名称。
- “上传文件 / 粘贴内容”切换。
- 测试连接按钮。
- 测试中的加载状态和防重复提交。
- 测试结果摘要。
- 测试成功后显示确认按钮。

### 5.4 集群基础详情

- 集群名称、连接状态、Kubernetes 版本。
- API Server、默认 Context。
- 录入时间、凭据更新时间、最后检查时间。
- 重新测试连接、修改名称、更新 Kubeconfig、取消纳管。
- 永不展示 Token、CA 内容和原始 Kubeconfig。

## 6. 数据模型

### 6.1 Cluster

- `id`: UUID 主键。
- `name`: 唯一显示名称。
- `api_server`: HTTPS 地址。
- `context_name`: 默认 Context 名称。
- `ca_certificate`: CA PEM 内容。
- `encrypted_credentials`: 加密后的 Token，或客户端证书与私钥凭据包。
- `kubernetes_version`: 探测到的版本。
- `fingerprint`: API Server 与 CA 的 SHA-256 指纹，唯一。
- `connection_status`: connected、unreachable、authentication_failed、tls_failed。
- `last_error_code`: 最近错误类型，不保存敏感详情。
- `last_checked_at`: 最后检测时间。
- `credentials_updated_at`: 凭据更新时间。
- `created_at`、`updated_at`: 审计时间。

### 6.2 ClusterImportDraft

- `id`: UUID，同时作为不透明的 `draft_id`。
- `cluster`: 更新凭据时关联的现有集群，可为空。
- `name`: 待纳管名称。
- 经过解析的连接字段，敏感字段加密。
- `fingerprint`: 重复检查使用。
- `expires_at`: 创建十分钟后过期。
- `created_at`: 创建时间。

正式确认后立即删除；过期记录由清理命令删除。

### 6.3 OperationLog

- `action`: validate、create、rename、credential_update、connection_check、delete。
- `cluster_id` 和操作时的集群名称快照。
- `result`: success 或 failed。
- `error_code`: 可为空。
- `created_at`。

不记录请求正文、Token、CA 或 Kubeconfig。

## 7. 接口

- `POST /api/v1/clusters/validate`：解析并测试新集群，返回非敏感摘要和 `draft_id`。
- `POST /api/v1/clusters/confirm`：确认临时记录并创建集群。
- `GET /api/v1/clusters`：集群列表。
- `GET /api/v1/clusters/{id}`：基础详情。
- `PATCH /api/v1/clusters/{id}`：修改显示名称。
- `POST /api/v1/clusters/{id}/validate`：测试更新凭据。
- `POST /api/v1/clusters/{id}/confirm`：确认替换凭据。
- `POST /api/v1/clusters/{id}/check`：重新测试当前凭据。
- `DELETE /api/v1/clusters/{id}`：取消纳管，要求提交确认名称。

接口由 Go 后端提供 JSON 响应。React 前端使用 TanStack Query 调用接口，并使用 React Router 管理页面路由。接口统一返回：

```json
{
  "data": {},
  "error": null
}
```

失败响应只返回稳定错误码和中文安全提示，不返回 Go 内部错误文本。

## 8. 安全要求

- Token、客户端证书和私钥使用应用层认证加密保存，加密密钥从环境变量读取，不能进入 Git 或以明文进入数据库。
- 凭据加密密钥必须独立管理，不能复用管理员密码哈希或会话随机值。
- 测试连接超时五秒。
- 不跟随 API Server HTTP 跳转。
- 限制响应大小。
- 不允许关闭 TLS 校验。
- 错误页不显示堆栈和底层敏感异常。
- 日志禁止记录 Kubeconfig、Token、CA 和完整请求正文。
- 所有业务页面和接口要求管理员登录。
- 单管理员密码使用安全密码哈希保存。
- 登录成功后使用服务端会话和 `HttpOnly`、`SameSite=Strict` Cookie。
- 所有状态修改接口校验 CSRF Token。
- 前后端生产环境保持同源部署，不开放任意跨域访问。

## 9. 错误类型

- `NAME_REQUIRED`、`NAME_DUPLICATE`
- `KUBECONFIG_REQUIRED`、`KUBECONFIG_TOO_LARGE`、`KUBECONFIG_INVALID`
- `CURRENT_CONTEXT_MISSING`、`CURRENT_CONTEXT_INVALID`
- `TOKEN_AUTH_REQUIRED`、`INLINE_CA_REQUIRED`
- `HTTPS_REQUIRED`
- `CLUSTER_DUPLICATE`
- `CONNECTION_TIMEOUT`、`CONNECTION_FAILED`
- `TLS_VERIFICATION_FAILED`
- `AUTHENTICATION_FAILED`、`AUTHORIZATION_FAILED`
- `DRAFT_EXPIRED`
- `ENCRYPTION_FAILED`

页面把错误码转换为清晰的中文提示，不直接显示底层异常文本。

## 10. 验收标准

1. 文件上传和文本粘贴均可完成纳管。
2. 多 Context 配置正确使用 `current-context`。
3. 无默认 Context、错误 Token、错误客户端证书、错误 CA 和网络超时均有明确提示。
4. 同一集群更换显示名称后仍不能重复纳管。
5. 测试成功后十分钟内可以确认，过期后必须重新测试。
6. 凭据更新失败不影响旧凭据，成功后旧凭据不再保留。
7. 取消纳管不影响真实 Kubernetes 集群。
8. 未登录请求被拒绝。
9. 页面、应用日志和操作日志不出现明文 Token、客户端证书或私钥。
10. 原始 Kubeconfig 无下载和查看入口。

## 11. 架构决策

### ADR-001：采用 Headlamp 风格的 Go + React 单体部署

前端使用 React、TypeScript 和 Material UI；后端使用 Go，并通过 Kubernetes 官方 `client-go` 访问集群。该组合比 Django 更贴近 Kubernetes 生态，后续实现资源 Watch、日志流和容器 Exec 时无需更换后端技术。

前后端在开发时独立运行，生产时将前端编译产物嵌入 Go 二进制或随同镜像发布，由同一个 Go 服务托管，因此不演变为微服务架构。

### ADR-002：不长期保存原始 Kubeconfig

只保存连接所需字段，减少无关凭据和 Context 的暴露面。代价是未来需要支持其他认证方式时，必须扩展数据模型。

### ADR-003：测试与确认使用临时记录

避免把 Token 放入浏览器隐藏字段，也避免文件上传后要求用户再次选择。代价是增加一张临时表和过期清理任务。

### ADR-004：第一版只支持单管理员

先验证核心功能，不提前构建复杂 RBAC。容器 Exec 上线前必须重新设计用户、环境和操作权限。

### ADR-005：参考 Headlamp，但不直接 Fork

借鉴 Headlamp 的多集群代理、资源列表、详情页和权限感知设计，但不继承其桌面端、插件系统、Helm、通用资源编辑及其他当前不需要的复杂能力。这样既保持 Kubernetes 原生技术路线，又维持产品边界。
