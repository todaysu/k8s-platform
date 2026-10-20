import { useState } from 'react'
import { buildMethodCommand } from './commands'
import { communityCases } from './cases'
import { Button, Card, CardBody, Title } from '@patternfly/react-core'

type Step = { title: string; command: string; read: string; next: string; example: string; method?: boolean; classOnly?: boolean }
type Scenario = { id: string; title: string; summary: string; steps: Step[] }
const scenarios: Scenario[] = [
  { id: 'cpu', title: 'CPU 持续偏高', summary: '找出忙线程，定位到具体代码。', steps: [
    { title: '采样最忙的线程', command: 'thread -n 3 -i 1000', read: '看 cpuUsage 较高的线程及其下方 at 开头的调用栈，找到业务包名、类名和行号。', next: '间隔几秒重复采样。若多次停在相同业务代码，交给开发核查循环、计算或重试；若主要是 GC 线程，切到“内存上涨 / GC 频繁”。', example: '"http-worker-8" Id=42 cpuUsage=86.00% RUNNABLE\n    at com.example.OrderService.calculate(OrderService.java:128)' },
    { title: '核对 JVM 整体状态', command: 'dashboard -n 1', read: '同时看线程 CPU、堆占用和 GC 次数/耗时。一次采样不能证明持续高负载。', next: '结合监控中的 Pod CPU 趋势和故障时间，记录重复出现的线程、业务行号和采样时间。', example: 'THREAD / CPU / MEMORY / GC（实际字段以终端输出为准）' },
  ] },
  { id: 'memory', title: '内存上涨 / GC 频繁', summary: '区分堆、非堆和堆外内存。', steps: [
    { title: '查看内存分布', command: 'memory', read: '看 heap、metaspace、direct 的 used / max。Pod 内存还包含线程栈等，不能只看 heap。', next: '若 heap 高，继续观察 GC；若 metaspace 持续上涨，检查类加载。单次占用高不能直接判定内存泄漏。', example: 'Memory       used     total    max\nheap         420M     512M     512M\ndirect       16M      16M      -' },
    { title: '比较 GC 前后变化', command: 'dashboard -n 3 -i 1000', read: '比较这 3 次采样的 GC 累计次数、累计耗时及堆占用变化。累计值大不等于当前频繁 GC。', next: '如果 GC 次数持续增长且回收后堆仍很高，保留故障时间和采样结果，再与开发评估堆快照；此流程不会自动导出大文件。', example: 'gc.*.count 和 gc.*.time 为累计值，重点比较相邻采样的增量。' },
    { title: '查看类加载器', command: 'classloader -l', read: '看 ClassLoader 的数量及各自 loadedClassCount，关注重复的业务类加载器。', next: '需多次比较并结合热部署、动态代理等使用情况判断；数量多本身不代表泄漏。', example: 'name                     loadedClassCount\nAppClassLoader           5230' },
  ] },
  { id: 'slow', title: '接口响应慢', summary: '已知业务类与方法时，观察慢调用。', steps: [
    { title: '先排查线程阻塞', command: 'thread -b', read: '若输出 but blocks N other threads，查看持锁线程的业务代码位置。', next: '没有输出只表示未找到此命令支持的锁阻塞；仍可能是网络、数据库、线程池等待。继续跟踪具体方法。', example: 'locked java.lang.Object@… <---- but blocks 4 other threads!' },
    { title: '定位慢调用的耗时层级', command: 'trace', method: true, read: '看调用树里各节点的毫秒耗时，定位耗时集中的下游调用。只展示大于所设阈值的调用，最多输出 5 次。', next: '保持会话，在业务侧触发一次真实请求。若无输出，核对类名、方法名、是否命中该 Pod 以及耗时是否超过阈值。trace 只跟踪一层，需逐层定位。', example: '`---[150.2ms] com.example.OrderService:submit()\n    `---[143.0ms] com.example.Repository:save()' },
  ] },
  { id: 'error', title: '接口报错 / 偶发异常', summary: '捕获指定方法抛出的异常。', steps: [
    { title: '观察方法异常', command: 'watch', method: true, read: '只在方法抛出异常时显示异常对象，最多输出 5 次。此预设不采集请求入参和返回值。', next: '触发问题请求后，看异常类型、message 和 cause。若无输出，可能异常已在方法内部捕获，或请求没有落到此 Pod；结合应用日志继续排查。', example: 'throwExp = java.lang.IllegalStateException: connection unavailable' },
  ] },
  { id: 'blocked', title: '应用卡住 / 线程等待', summary: '区分锁阻塞和正常等待。', steps: [
    { title: '查找持锁线程', command: 'thread -b', read: '看是否有阻塞其他线程的持锁者，以及对应的调用栈。此命令不能覆盖所有锁类型，也不是完整死锁检测。', next: '记录持锁者和业务代码位置；没有结果时继续看线程状态，不要直接判断没有阻塞。', example: '"worker-1" Id=27\n    - locked java.lang.Object@… <---- but blocks 4 other threads!' },
    { title: '查看线程分布', command: 'thread', read: '看 BLOCKED、WAITING、TIMED_WAITING 的数量。线程池空闲线程出现 WAITING 很常见，需结合业务是否卡住判断。', next: '从输出中找到可疑线程 ID，在高级命令输入 thread 加该 ID 查看完整调用栈，区分锁等待、网络读取或线程池任务等待。', example: 'ID   NAME          STATE\n42   http-worker   WAITING\n示例：thread 42（请替换为实际线程 ID）' },
  ] },

  { id: 'release', title: '发布后代码未生效', summary: '核对实际加载的包与运行代码。', steps: [
    { title: '确认类来自哪个包', command: 'sc', classOnly: true, read: '看 code-source 是否指向预期版本的 jar，同时记录 classLoaderHash。多条结果表示存在多个加载来源，需要逐个核对。', next: '先确认当前 Pod 是否为本次发布的实例。若包路径不对，检查镜像和启动 classpath；路径正确但行为不对，再查看运行代码。', example: 'code-source /app/releases/order-service.jar\nclassLoaderHash 18b4aac2' },
    { title: '核对 JVM 实际运行代码', command: 'jad', classOnly: true, read: '对比反编译代码与预期发布逻辑；反编译结果不一定与源码逐字相同。若提示存在多个 ClassLoader，先依据 sc 结果区分来源。', next: '确认旧包、错误镜像或加载来源后走正常发布流程。此向导只查看代码，不会执行线上热更新。多个加载器时可在高级命令中使用 jad -c 加实际 hash 和类名。', example: 'ClassLoader: ...\nLocation: /app/releases/order-service.jar\n// 以下为反编译代码' },
  ] },
  { id: 'class', title: '找不到类 / 依赖冲突', summary: '确认已加载的类和 ClassLoader。', steps: [
    { title: '检查目标类是否已加载', command: 'sc', classOnly: true, read: '核对类名、code-source 与 classLoaderHash。无结果表示当前 JVM 没找到匹配的已加载类，不等于 jar 中一定不存在该类。', next: '核对异常中的完整类名及目标 Pod。ClassNotFoundException 检查依赖与加载路径；NoClassDefFoundError 还需查看更早的初始化失败异常。', example: 'Affect(row-cnt:0) 表示本次搜索没有匹配到已加载的类。' },
    { title: '检查加载器结构', command: 'classloader -t', read: '查看加载器的父子关系，结合上一阶段的 hash 判断类是否由不同加载器加载。', next: '同名类来自不同加载器可能导致类型不兼容。结合依赖版本和原始异常确认，不能只凭加载器数量下结论。', example: '+-BootstrapClassLoader\n  +-PlatformClassLoader\n    +-AppClassLoader' },
  ] },
  { id: 'rate', title: '方法偶发失败 / 耗时波动', summary: '短时统计次数、失败率与平均耗时。', steps: [
    { title: '统计指定方法的调用', command: 'monitor', method: true, read: '每 5 秒输出一组调用次数、成功/失败次数、平均耗时和失败率，最多输出 3 组。平均值会掩盖少量慢请求。', next: '等待业务请求触发；若失败持续出现，转到异常场景；均值正常但用户反馈偶发慢，转到慢调用场景。此统计只覆盖当前 JVM，不代表所有副本。', example: 'total  success  fail  avg-rt(ms)  fail-rate\n20     19       1     32.8        5.00%' },
  ] },
  { id: 'nomatch', title: 'trace / watch 没有输出', summary: '检查匹配、方法签名与请求落点。', steps: [
    { title: '检查类是否命中', command: 'sc', classOnly: true, read: '没有匹配结果时先核对完整类名与 Pod；有多个加载器时记录对应 hash。不要靠扩大通配范围盲目增强大量类。', next: '类已加载后，继续核对方法名与签名；命令仍在运行时，先用 Ctrl+C 中断，再执行下一条。', example: 'class-info com.example.OrderService\nclassLoaderHash 18b4aac2' },
    { title: '核对方法签名', command: 'sm', method: true, read: '确认声明类、方法名、参数和返回类型。继承的方法要核对实际声明它的父类；多个同名重载可能同时被增强。', next: '再检查请求是否落到该 Pod、是否实际执行了方法以及 trace 耗时阈值。Affect class/method count 为 0 时先解决匹配问题；不是 0 却没输出时检查触发条件。', example: 'declaring-class com.example.OrderService\nmethod-name submit\nparameters java.lang.String' },
  ] },
]


export default function ArthasGuide({ ready, onSend }: { ready: boolean; onSend: (command: string) => void }) {
  const [scenarioID, setScenarioID] = useState('cpu')
  const [stepIndex, setStepIndex] = useState(0)
  const [className, setClassName] = useState('')
  const [method, setMethod] = useState('')
  const [threshold, setThreshold] = useState('100')
  const scenario = scenarios.find(item => item.id === scenarioID)!
  const step = scenario.steps[stepIndex]
  const command = step.method || step.classOnly ? buildMethodCommand(step.command, className.trim(), method.trim(), threshold) : step.command
  return <Card className="ks-arthas-playbook"><CardBody>
    <div className="ks-arthas-playbook-heading"><div><Title headingLevel="h2" size="lg">你遇到了什么问题？</Title><p>选故障现象，按步骤执行并对照输出。选择场景不会自动执行命令。</p></div><span>故障排查向导</span></div>
    <div className="ks-arthas-scenarios" role="group" aria-label="故障现象">{scenarios.map(item => <button key={item.id} type="button" aria-pressed={scenarioID === item.id} onClick={() => { setScenarioID(item.id); setStepIndex(0) }}><strong>{item.title}</strong><span>{item.summary}</span></button>)}</div>
    <div className="ks-arthas-playbook-body">
      <nav aria-label="排查步骤">{scenario.steps.map((item, index) => <button type="button" key={item.title} aria-current={index === stepIndex ? 'step' : undefined} onClick={() => setStepIndex(index)}><span>{index + 1}</span>{item.title}</button>)}</nav>
      <section aria-label="当前排查步骤">
        <Title headingLevel="h3" size="md">{stepIndex + 1}. {step.title}</Title>
        {(step.method || step.classOnly) && <><div className="ks-arthas-method-fields">
          <label>完整类名<input aria-label="完整类名" placeholder="例如 com.example.OrderService" value={className} onChange={event => setClassName(event.target.value)} /></label>
          {!step.classOnly && <label>方法名<input aria-label="方法名" placeholder="例如 submit" value={method} onChange={event => setMethod(event.target.value)} /></label>}
          {step.command === 'trace' && <label>耗时超过（毫秒）<input aria-label="耗时阈值" type="number" min="1" max="999999" value={threshold} onChange={event => setThreshold(event.target.value)} /></label>}
        </div><p>{step.classOnly ? '从异常堆栈或开发提供的信息中获取完整类名，不使用通配符。' : '类名和方法名需从开发、异常堆栈或应用日志获取；接口 URL 不能直接替代方法名。不使用通配符，默认匹配规则可能包含子类。'}</p>{step.method && <p>trace / watch / monitor 会增强目标方法并增加运行开销，使用后按 Ctrl+C 中断；排查结束执行 stop。</p>}</>}
        <div className="ks-arthas-command-preview"><code>{command ?? '填写有效的类名、方法名和参数后生成命令'}</code><Button variant="primary" isDisabled={!ready || !command} onClick={() => command && onSend(command)}>执行这一步</Button></div>
        {!ready && <p className="ks-arthas-guide-hint">先在上方选择目标并启动会话，进入 Arthas 后即可执行。无需手动输入上述命令。</p>}
        <dl><dt>输出怎么看</dt><dd>{step.read}</dd><dt>接下来怎么做</dt><dd>{step.next}</dd></dl>
        <details><summary>查看输出示例（示意数据）</summary><pre>{step.example}</pre><p>示例仅解释格式，实际结果显示在会话终端中；平台不会据此自动判断根因。</p></details>
        <div className="ks-arthas-guide-footer"><span>第 {stepIndex + 1} / {scenario.steps.length} 步 · 可重复采样核对</span>{stepIndex + 1 < scenario.steps.length && <Button variant="secondary" onClick={() => setStepIndex(stepIndex + 1)}>看下一步</Button>}</div>
      </section>
    </div>
    <section className="ks-arthas-community-cases" aria-label="用户排障案例">
      <h3>用户排障案例 · 这个问题别人怎么查</h3>
      <p>来自社区作者的排障记录或复现实验，已注明类型。按当前故障匹配，保留原文供核对。</p>
      {communityCases.filter(item => item.scenarios.includes(scenarioID)).map(item => <article key={item.id}>
        <header><strong>{item.title}</strong><span>{item.author} · {item.kind}</span></header>
        <dl><dt>问题现象</dt><dd>{item.symptom}</dd><dt>使用的方法</dt><dd>{item.method}</dd><dt>如何定位</dt><dd>{item.finding}</dd><dt>在本平台怎么用</dt><dd>{item.apply}</dd></dl>
        <a href={item.url} target="_blank" rel="noreferrer">阅读用户原文</a>
      </article>)}
      {!communityCases.some(item => item.scenarios.includes(scenarioID)) && <p>暂未收录该场景的用户案例；上方仍可按步骤排查。</p>}
    </section>
  </CardBody></Card>
}
