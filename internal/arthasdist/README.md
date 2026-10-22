# 内置 Arthas 4.3.5

检查日期：2026-10-20。GitHub latest 与 Maven Central 最新版本均为 4.3.5。

- 上游发布：https://github.com/alibaba/arthas/releases/tag/arthas-all-4.3.5
- 完整包：https://repo.maven.apache.org/maven2/com/taobao/arthas/arthas-packaging/4.3.5/arthas-packaging-4.3.5-bin.zip
- 上游 SHA-256：`2f75ea954ead5d02c4a743ecd783fd92f546a44ef3aee437ea5066eb30ad07bf`
- SHA 来源：同一制品 URL 加 `.sha256`，下载后已逐字校验。
- Apache-2.0 许可证见 LICENSE.txt；本目录保存原始完整发行 zip，无源代码修改。发行包包含 async-profiler 等上游分发的第三方组件，其许可证仍适用。

通过 go:embed 随平台二进制分发。启动会话时先校验本地包 SHA，再转换成 tar.gz，经 Kubernetes Exec（关闭 TTY）传给容器，随后指定 --arthas-home / --use-version 启动，不从公网下载第二阶段依赖。目标需 Linux、JDK 8+、sh、tar、可写 /tmp，并与目标 JVM 同用户运行。支持 x86_64 与 ARM64；zip 包中同时保留两个架构的 JNI 和 profiler 库，由 JVM/工具选择。当前实测为 test01 ARM64 / JDK8，x86_64 尚未运行本次集成测试。

升级需同时更新 zip、版本常量、SHA、前端版本提示和测试；不要在运行时追踪浮动 latest。发行包约 17MiB，增加平台二进制体积，首次传入有相应开销。按版本及摘要缓存于容器 /tmp，Pod 重建后重新传入。

生产 Java 容器常用 UID2000 / GID5000：测试应用已按此用户部署。Exec 默认跟随容器用户，不执行 su 或切换为 root；与 JVM 的 UID 一致是 Attach 的必要条件，GID 和目录权限也需允许读写。
