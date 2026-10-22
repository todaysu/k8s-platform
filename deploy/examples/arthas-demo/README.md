# Arthas 测试应用

本地 test01（kind-k8s-test）已部署到 myapp：Deployment arthas-demo，容器 java。
镜像 ftc-arthas-demo:3，JDK8，以 UID2000 / GID5000 运行；当前测试节点 ARM64。
不创建 NodePort / Ingress。Java 类和字节码与 CPU 架构无关，基础 JDK 镜像应选择目标节点对应的 amd64/arm64 版本。

## 重新构建和部署

```sh
docker build -t ftc-arthas-demo:3 deploy/examples/arthas-demo
kind load docker-image ftc-arthas-demo:3 --name k8s-test
kubectl --context kind-k8s-test apply -f deploy/examples/arthas-demo/deployment.yaml
```

## 在平台验证

运维工具 → Arthas 诊断 → test01 → myapp → arthas-demo-* → java。
确认后启动，在进程列表选择 demo.DiagnosticApp 对应编号（应用 PID 为 1）。

- JVM / memory / thread：可直接查看。
- 慢调用：完整类名 demo.DiagnosticApp，方法 slowOperation，阈值100ms。每约5秒自动触发一次350ms慢调用。
- 异常：同类，方法 failingOperation，每约5秒自动触发一条明确标记的测试异常。
- 发布验证：sc / jad，类名 demo.DiagnosticApp。
- 方法统计：monitor，同类，slowOperation。
- CPU：/cpu 只运行500ms计算，不默认持续消耗CPU。

HTTP端点：/health（200）、/slow（约350ms）、/error（有意500）、/cpu（500ms有限计算）。测试异常由程序内部捕获，不引发 Pod 重启；本应用不制造无限内存增长或永久死锁。

排查结束点击中断采样、结束会话。移除测试部署：kubectl --context kind-k8s-test -n myapp delete deployment arthas-demo。

权限与生产约定对齐：容器默认用户为 2000:5000，Arthas 经 Exec 以同一用户运行，不需要 root 或新增 capabilities。镜像包含对应 passwd/group 条目，保证 Java 进程发现。目标容器的 /tmp 和 JVM Attach 临时文件必须对该用户可写。平台不为所有应用强制覆盖成 2000:5000；其他应用仍跟随各自容器用户。
