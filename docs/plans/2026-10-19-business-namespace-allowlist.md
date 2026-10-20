# 全局业务 Namespace 清单 Implementation Plan

**Goal:** 只维护全局业务 Namespace 清单，其他 Namespace 自动统计为平台；保留交付后新增平台 Namespace 提醒和人工更新基线。

**Architecture:** 沿用 Kubernetes/OpenShift 按 Namespace 汇总 Requests、Limits、实际用量的资源模型，简化为业务清单与非业务补集。旧分类版本和基线保留，读取时忽略旧公共/技术栈映射，新版本仅存业务清单。新增平台范围通过每个集群当前平台 Namespace 与其基线名单作差识别，不自动改变基线。

**Tech Stack:** Go/client-go/SQLite，React/PatternFly/TypeScript。

参考：https://docs.redhat.com/en/documentation/openshift_container_platform/4.20/html/building_applications/quotas

## Task 1 后端口径与兼容
- 修改 internal/cluster/connection.go：业务优先，其余全部平台；平台名单从 Namespace 及 Pod 补集生成，Prometheus 使用相同划分。
- 修改 internal/cluster/onboarding.go、model.go：忽略旧技术栈规则、清单规范化与 Kubernetes 名称校验；新增平台 Namespace 与基线比较，保留显式基线确认。
- 修改 connection_test.go、onboarding_test.go：覆盖非业务自动平台、空 NS、旧规则、非法名称、新增空平台 NS、基线不自动改变。
- 运行 /opt/homebrew/bin/go test ./...。

## Task 2 单一配置入口
- 重写 web/src/components/CapacityClassificationDialog.tsx：业务清单编辑（支持尚未出现的 NS），展示发现的业务/平台范围；去掉技术栈/公共组件分类。默认只显示待加入的非业务 NS，已有业务可修改清单移除。
- 修改 web/src/api/clusters.ts 与两级容量页：去掉未分类分类项和旧文案，新增平台提醒，不重复扣减资源。
- 更新前端集成/组件测试覆盖清单增删、未发现名称、跨技术栈去重、校验、原有规则兼容。
- 运行 npm test -- --run、npm run build、npm run lint。

## Task 3 文档、运行与核验
- 更新部署说明和 ADR，解释空业务清单意味着全部平台，新 Namespace 提示但不更新交付基线。
- 保留原配置构建并重启本地后端；浏览器检查发现的 myapp 与实际 API 的业务/平台分类，保留原业务配置，不为测试写入用户规则。
