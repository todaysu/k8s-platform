import { useMemo, useState } from 'react'
import { Alert, Button, Card, CardBody, Label, Title } from '@patternfly/react-core'
import { CheckCircleIcon, ExclamationCircleIcon, ExclamationTriangleIcon } from '@patternfly/react-icons'
import { analyzeJvm, jvmSample, type JvmInput, type JvmReport, type JvmSeverity } from '../tools/jvm'
import './JvmAnalysisPage.css'

const emptyInput: JvmInput = { jstat: '', heap: '', histogram: '', gc: '', containerLimit: '', containerUsage: '' }
const severityLabel: Record<JvmSeverity, string> = { critical: '高风险', warning: '需关注', info: '提示' }
const severityColor: Record<JvmSeverity, 'red' | 'orange' | 'blue'> = { critical: 'red', warning: 'orange', info: 'blue' }
const severityIcon: Record<JvmSeverity, typeof ExclamationCircleIcon> = {
  critical: ExclamationCircleIcon,
  warning: ExclamationTriangleIcon,
  info: CheckCircleIcon,
}

const formatBytes = (value: number | undefined) => {
  if (!value) return '—'
  const gib = value / 1024 ** 3
  return gib >= 1 ? `${gib.toFixed(2)} GiB` : `${(value / 1024 ** 2).toFixed(0)} MiB`
}

function PercentBar({ label, value }: { label: string; value: number }) {
  return (
    <div className="ks-jvm-bar">
      <div><span>{label}</span><strong>{value.toFixed(1)}%</strong></div>
      <div className="ks-jvm-track"><span className={value >= 85 ? 'is-critical' : value >= 70 ? 'is-warning' : ''} style={{ width: `${Math.min(100, Math.max(0, value))}%` }} /></div>
    </div>
  )
}

function Metric({ label, value, detail }: { label: string; value: string; detail: string }) {
  return <div className="ks-jvm-metric"><span>{label}</span><strong>{value}</strong><small>{detail}</small></div>
}

export default function JvmAnalysisPage() {
  const [input, setInput] = useState<JvmInput>(emptyInput)
  const [report, setReport] = useState<JvmReport | null>(null)
  const [error, setError] = useState('')

  const latest = report?.jstat?.samples.at(-1)
  const heapPercent = report?.heap && report.heap.totalBytes > 0 ? report.heap.usedBytes / report.heap.totalBytes * 100 : 0
  const histogramRows = useMemo(() => report?.histogram?.entries.slice(0, 12) ?? [], [report])
  const jstatRows = useMemo(() => report?.jstat?.samples.slice(-8) ?? [], [report])

  const update = (key: keyof JvmInput, value: string) => setInput(current => ({ ...current, [key]: value }))
  const analyze = () => {
    setError('')
    if (!input.jstat.trim() && !input.heap.trim() && !input.histogram.trim() && !input.gc.trim()) {
      setError('请至少粘贴 jstat、GC.heap_info、类直方图或 GC 日志之一。')
      return
    }
    setReport(analyzeJvm(input))
  }

  return (
    <div className="ks-jvm-page">
      <header className="ks-jvm-header">
        <div>
          <p>导入 JVM 诊断输出，判断老年代增长、Full GC、对象滞留、Metaspace 和容器堆外内存风险。所有数据只在浏览器本地处理。</p>
        </div>
        <div className="ks-jvm-header-actions">
          <Button variant="secondary" onClick={() => { setInput(jvmSample); setReport(analyzeJvm(jvmSample)); setError('') }}>查看示例</Button>
          <Button variant="link" onClick={() => { setInput(emptyInput); setReport(null); setError('') }}>清空</Button>
        </div>
      </header>

      {error && <Alert isInline title={error} variant="danger" />}

      <div className="ks-jvm-layout">
        <section className="ks-jvm-inputs" aria-label="JVM 诊断数据输入">
          <InputCard title="jstat -gcutil" hint="每行采样，至少 2 行可看趋势；也支持 jstat -gc。" value={input.jstat} onChange={value => update('jstat', value)} placeholder="S0 S1 E O M CCS YGC YGCT FGC FGCT GCT" />
          <InputCard title="jcmd <pid> GC.heap_info" hint="堆总量、已用、Metaspace 和 class space。" value={input.heap} onChange={value => update('heap', value)} placeholder="garbage-first heap total 4194304K, used ..." />
          <InputCard title="jcmd <pid> GC.class_histogram" hint="也可粘贴 jmap -histo:live 输出；关注 [B、String 和业务缓存类。" value={input.histogram} onChange={value => update('histogram', value)} placeholder="num #instances #bytes class name" />
          <InputCard title="GC 日志（可选）" hint="支持常见 JDK 9+ unified logging 和 JDK 8 G1 行。" value={input.gc} onChange={value => update('gc', value)} placeholder="GC(1) Pause Young ... 1024M->512M(2048M) 12.3ms" />
          <Card className="ks-jvm-card">
            <CardBody>
              <Title headingLevel="h2" size="md">容器内存（可选）</Title>
              <p className="ks-jvm-hint">用于比较 JVM 堆与容器 limit，识别堆外内存和 OOMKill 风险。</p>
              <div className="ks-jvm-inline">
                <label>容器 limit<input value={input.containerLimit} onChange={event => update('containerLimit', event.target.value)} placeholder="例如 2Gi" /></label>
                <label>当前用量<input value={input.containerUsage} onChange={event => update('containerUsage', event.target.value)} placeholder="例如 1.95Gi" /></label>
              </div>
            </CardBody>
          </Card>
          <Button variant="primary" onClick={analyze}>开始分析</Button>
        </section>

        <section className="ks-jvm-results" aria-label="JVM 分析结果">
          {!report && <div className="ks-jvm-empty"><strong>还没有分析结果</strong><p>粘贴至少一种 JVM 输出，或点击“查看示例”。</p></div>}
          {report && <>
            <Alert isInline title={report.summary} variant={report.findings.some(finding => finding.severity === 'critical') ? 'danger' : report.findings.some(finding => finding.severity === 'warning') ? 'warning' : 'info'} />
            <div className="ks-jvm-metrics">
              <Metric label="堆使用率" value={report.heap ? `${heapPercent.toFixed(1)}%` : latest ? '—' : '—'} detail={report.heap ? `${formatBytes(report.heap.usedBytes)} / ${formatBytes(report.heap.totalBytes)}` : '导入 GC.heap_info'} />
              <Metric label="老年代" value={latest ? `${latest.o.toFixed(1)}%` : '—'} detail={latest ? `最新采样 · Young GC ${latest.ygc} 次` : '导入 jstat'} />
              <Metric label="Full GC" value={latest ? String(latest.fgc) : report.gc ? String(report.gc.fullPauses) : '—'} detail={latest ? `${latest.fgct.toFixed(2)} 秒` : report.gc ? `最大停顿 ${report.gc.maxPauseMs.toFixed(0)} ms` : '导入 jstat 或 GC 日志'} />
              <Metric label="Metaspace" value={report.heap ? formatBytes(report.heap.metaspaceUsedBytes) : '—'} detail={report.heap ? `class space ${formatBytes(report.heap.classSpaceUsedBytes)}` : '导入 GC.heap_info'} />
            </div>

            <div className="ks-jvm-findings">
              {report.findings.map(finding => {
                const Icon = severityIcon[finding.severity]
                return <article className={`ks-jvm-finding is-${finding.severity}`} key={finding.title + finding.detail}>
                  <Icon />
                  <div>
                    <header><Label color={severityColor[finding.severity]} isCompact>{severityLabel[finding.severity]}</Label><strong>{finding.title}</strong></header>
                    <p>{finding.detail}</p>
                    <small>{finding.action}</small>
                  </div>
                </article>
              })}
            </div>

            {report.jstat && latest && <Card className="ks-jvm-card"><CardBody>
              <Title headingLevel="h2" size="md">GC 趋势</Title>
              <div className="ks-jvm-bars">
                <PercentBar label="Eden" value={latest.e} />
                <PercentBar label="Old" value={latest.o} />
                <PercentBar label="Metaspace" value={latest.m} />
              </div>
              <div className="ks-jvm-table-wrap"><table className="ks-jvm-table"><thead><tr><th>采样</th><th>Eden</th><th>Old</th><th>YGC</th><th>FGC</th><th>FGCT</th></tr></thead><tbody>
                {jstatRows.map((sample, index) => <tr key={index}><td>{index + 1}</td><td>{sample.e.toFixed(1)}%</td><td>{sample.o.toFixed(1)}%</td><td>{sample.ygc}</td><td>{sample.fgc}</td><td>{sample.fgct.toFixed(2)}s</td></tr>)}
              </tbody></table></div>
            </CardBody></Card>}

            {report.histogram && <Card className="ks-jvm-card"><CardBody>
              <Title headingLevel="h2" size="md">类直方图 Top {histogramRows.length}</Title>
              <p className="ks-jvm-hint">按字节数排序。内存升高前后各导出一份，比较实例数和字节数更有价值。</p>
              <div className="ks-jvm-table-wrap"><table className="ks-jvm-table"><thead><tr><th>类</th><th>实例数</th><th>字节</th><th>占比</th></tr></thead><tbody>
                {histogramRows.map(entry => <tr key={entry.className}><td className="ks-jvm-class">{entry.className}</td><td>{entry.instances.toLocaleString()}</td><td>{formatBytes(entry.bytes)}</td><td>{(entry.bytes / report.histogram!.totalBytes * 100).toFixed(1)}%</td></tr>)}
              </tbody></table></div>
            </CardBody></Card>}

            {report.gc && <Card className="ks-jvm-card"><CardBody>
              <Title headingLevel="h2" size="md">GC 日志摘要</Title>
              <div className="ks-jvm-metrics is-compact">
                <Metric label="GC 次数" value={String(report.gc.pauses)} detail={`其中 Full GC ${report.gc.fullPauses} 次`} />
                <Metric label="最大停顿" value={`${report.gc.maxPauseMs.toFixed(0)} ms`} detail={`累计 ${(report.gc.totalPauseMs / 1000).toFixed(1)} 秒`} />
                <Metric label="最近一次后堆" value={formatBytes(report.gc.lastAfterBytes)} detail={`堆总量 ${formatBytes(report.gc.lastTotalBytes)}`} />
              </div>
            </CardBody></Card>}
          </>}
        </section>
      </div>

      <section className="ks-jvm-guide">
        <Title headingLevel="h2" size="lg">内存升高时的排查顺序</Title>
        <ol>
          <li><strong>先分清是堆内还是堆外。</strong> 比较 Pod 内存曲线、容器 limit 与 JVM 堆上限；容器涨但堆不涨，优先查 Metaspace、直接内存、线程栈、Netty/JNI。</li>
          <li><strong>看 Full GC 后是否回落。</strong> 连续执行 <code>jstat -gcutil 1 1000 10</code>。Full GC 后老年代仍然很高，才更像对象滞留/泄漏。</li>
          <li><strong>抓两份类直方图。</strong> 内存升高前后各执行一次 <code>jcmd 1 GC.class_histogram</code>，对比实例数和字节数，不要只看单次排名。</li>
          <li><strong>确认堆参数。</strong> 检查 <code>-Xmx</code>、<code>-XX:MaxRAMPercentage</code> 与容器 limit 的差值，至少为元空间、线程栈和堆外内存预留 20%。</li>
          <li><strong>需要时再导堆。</strong> <code>jcmd 1 GC.heap_dump /tmp/heap.hprof</code> 会影响性能和磁盘，生产环境先评估；用 MAT/JProfiler 分析支配树和 GC Root。</li>
        </ol>
        <div className="ks-jvm-command">
          <pre>{`jcmd -l
jcmd <pid> GC.heap_info
jstat -gcutil <pid> 1000 10
jcmd <pid> GC.class_histogram
jcmd <pid> Thread.print`}</pre>
          <p>可以在平台的集群终端里执行；容器镜像需要包含 JDK 工具（jcmd/jstat），只有 JRE 时需使用 Arthas 或临时调试容器。</p>
        </div>
      </section>
    </div>
  )
}

function InputCard({ title, hint, value, onChange, placeholder }: { title: string; hint: string; value: string; onChange: (value: string) => void; placeholder: string }) {
  return <Card className="ks-jvm-card"><CardBody>
    <Title headingLevel="h2" size="md">{title}</Title>
    <p className="ks-jvm-hint">{hint}</p>
    <textarea aria-label={title} value={value} onChange={event => onChange(event.target.value)} placeholder={placeholder} spellCheck={false} rows={4} />
  </CardBody></Card>
}
