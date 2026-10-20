import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  CardBody,
  Checkbox,
  FormSelect,
  FormSelectOption,
  Label,
  Title,
} from '@patternfly/react-core'
import { AngleLeftIcon, CheckCircleIcon } from '@patternfly/react-icons'
import { fetchClusterPod, fetchClusterPods, fetchPlatformCapabilities, type ClusterPod } from '../api/clusters'
import { usePermission } from '../auth/context'
import ArthasTerminal, { type ArthasTerminalHandle } from '../components/ArthasTerminal'
import ArthasGuide from '../tools/arthas/ArthasGuide'
import './ArthasPage.css'

type Props = {
  clusters: { id: string; name: string }[]
  onBack: () => void
}

const podKey = (pod: ClusterPod) => `${pod.namespace}/${pod.name}`

const quickCommands = [
  { label: '实时面板', command: 'dashboard -n 3' },
  { label: '最忙线程', command: 'thread -n 10' },
  { label: '内存概览', command: 'memory' },
  { label: 'JVM 信息', command: 'jvm' },
]

export default function ArthasPage({ clusters, onBack }: Props) {
  const canConfigure = usePermission('configure')
  const terminalRef = useRef<ArthasTerminalHandle | null>(null)
  const [clusterID, setClusterID] = useState(clusters[0]?.id ?? '')
  const [pods, setPods] = useState<ClusterPod[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [namespace, setNamespace] = useState('')
  const [podQuery, setPodQuery] = useState('')
  const [selectedPodKey, setSelectedPodKey] = useState('')
  const [containers, setContainers] = useState<string[]>([])
  const [container, setContainer] = useState('')
  const [execAvailable, setExecAvailable] = useState<boolean | null>(null)
  const [confirmed, setConfirmed] = useState(false)
  const [active, setActive] = useState(false)
  const [connectionState, setConnectionState] = useState<'idle' | 'connecting' | 'connected'>('idle')
  const [attached, setAttached] = useState(false)
  const [command, setCommand] = useState('')

  const selectedPod = useMemo(() => pods.find(pod => podKey(pod) === selectedPodKey) ?? null, [pods, selectedPodKey])
  const filteredPods = useMemo(() => {
    const query = podQuery.trim().toLowerCase()
    return pods.filter(pod => pod.namespace === namespace && (!query || pod.name.toLowerCase().includes(query))).slice(0, 200)
  }, [pods, podQuery, namespace])
  const namespaces = useMemo(() => [...new Set(pods.map(pod => pod.namespace))].sort(), [pods])

  useEffect(() => {
    let active = true
    void fetchPlatformCapabilities().then(value => { if (active) setExecAvailable(value.pod_exec_enabled) }).catch(() => { if (active) setExecAvailable(false) })
    return () => { active = false }
  }, [])

  useEffect(() => {
    if (!clusterID) return
    let active = true
    setLoading(true); setError(''); setNamespace(''); setConfirmed(false); setPodQuery(''); setPods([]); setSelectedPodKey(''); setContainers([]); setContainer(''); setActive(false)
    void fetchClusterPods(clusterID).then(values => {
      if (!active) return
      setPods(values.filter(pod => pod.phase === 'Running').sort((left, right) => podKey(left).localeCompare(podKey(right))))
    }).catch(cause => { if (active) setError(cause instanceof Error ? cause.message : '读取 Pod 列表失败。') }).finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [clusterID])

  useEffect(() => {
    if (!clusterID || !selectedPod) { setContainers([]); setContainer(''); return }
    let active = true
    setContainers([]); setContainer('')
    void fetchClusterPod(clusterID, selectedPod.namespace, selectedPod.name).then(detail => {
      if (!active) return
      const names = detail.containers.map(item => item.name)
      setContainers(names)
      setContainer(names[0] ?? '')
    }).catch(cause => { if (active) setError(cause instanceof Error ? cause.message : '读取容器列表失败。') })
    return () => { active = false }
  }, [clusterID, selectedPod])

  useEffect(() => {
    if (!active) return
    const timer = window.setTimeout(() => {
      terminalRef.current?.stop()
      setActive(false)
      setError('Arthas 会话已达到 30 分钟上限并自动结束。')
    }, 30 * 60 * 1000)
    return () => window.clearTimeout(timer)
  }, [active])

  const start = () => {
    setError(''); setNotice('')
    if (!selectedPod || !container) { setError('请先选择正在运行的 Pod 和容器。'); return }
    if (execAvailable !== true) { setError('平台未开启 Pod Exec，无法启动 Arthas。'); return }
    if (!confirmed) { setError('请先确认你了解 Arthas 会 attach 到目标 JVM。'); return }
    setAttached(false)
    setActive(true)
  }

  const disconnect = () => {
    terminalRef.current?.stop()
    window.setTimeout(() => { setActive(false); setAttached(false); setConnectionState('idle') }, 1000)
  }

  const send = (value: string, fillOnly = false) => {
    if (!active || connectionState !== 'connected') { setError('请先启动并连接 Arthas 会话。'); return }
    if (!attached) { setError('请先在终端输入 Java 进程编号，看到 [arthas@1]$ 后再发送命令。'); return }
    if (fillOnly) { setCommand(value); setNotice('命令已填入下方输入框，请把类名/方法名改成实际值后发送。'); return }
    if (!terminalRef.current?.send(value)) { setError('Arthas 尚未连接，请稍等或检查错误信息。'); return }
    setNotice(`已发送：${value}`)
  }

  return (
    <div className={`ks-arthas-page ${active ? 'is-session' : ''}`}>
      <header className="ks-arthas-header">
        <div>
          <Button icon={<AngleLeftIcon />} onClick={onBack} variant="link">返回运维工具</Button>
          <Title headingLevel="h1" size="2xl">Arthas 诊断</Title>
          <p>选择目标、启动会话，然后跟着右侧向导排查。终端在主区域，不需要向下翻页；会话最长 30 分钟，仅管理员可用。</p>
        </div>
        <div className="ks-arthas-header-status">
          <Label color={active ? 'green' : 'grey'} icon={active ? <CheckCircleIcon /> : undefined}>{active ? '会话进行中' : '未开始'}</Label>
          {active && <span className="ks-arthas-session-clock">会话上限 30 分钟</span>}
        </div>
      </header>

      {!canConfigure && <Alert isInline title="需要管理员权限" variant="warning">Arthas 可以读取和修改 JVM 运行时，仅允许平台管理员使用。</Alert>}
      {error && <Alert isInline title={error} variant="danger" />}
      {notice && <Alert isInline title={notice} variant="info" />}

      <ol className="ks-arthas-progress" aria-label="Arthas 使用步骤">
        <li className={selectedPod && container ? 'is-done' : 'is-current'}><span>1</span><div><strong>选择目标</strong><small>{selectedPod ? `${selectedPod.namespace}/${selectedPod.name}` : '集群 → 命名空间 → Pod → 容器'}</small></div></li>
        <li className={active ? 'is-done' : selectedPod && container ? 'is-current' : ''}><span>2</span><div><strong>启动会话</strong><small>{active ? '会话已启动' : '确认风险后启动'}</small></div></li>
        <li className={attached ? 'is-done' : active ? 'is-current' : ''}><span>3</span><div><strong>按向导排查</strong><small>{attached ? '可以执行命令' : active ? '等待进入 Arthas' : '进入 Arthas 后启用'}</small></div></li>
      </ol>

      {!active && <>
        <div className="ks-arthas-setup">
          <Card className="ks-arthas-card">
            <CardBody>
              <div className="ks-arthas-step-title"><span>1</span><Title headingLevel="h2" size="md">选择目标</Title><small>先选集群和命名空间，再选 Pod 和容器</small></div>
              <div className="ks-arthas-form">
                <label>集群
                  <FormSelect aria-label="选择 Arthas 集群" isDisabled={active} value={clusterID} onChange={(_, value) => { setClusterID(value); setNamespace(''); setSelectedPodKey(''); setContainer(''); setConfirmed(false) }}>
                    <FormSelectOption label="请选择集群" value="" />
                    {clusters.map(cluster => <FormSelectOption key={cluster.id} label={cluster.name} value={cluster.id} />)}
                  </FormSelect>
                </label>
                <label>命名空间
                  <FormSelect aria-label="选择 Arthas 命名空间" isDisabled={active || loading || !clusterID} value={namespace} onChange={(_, value) => { setNamespace(value); setSelectedPodKey(''); setContainers([]); setContainer(''); setConfirmed(false); setPodQuery('') }}>
                    <FormSelectOption label={loading ? '正在读取…' : '请选择命名空间'} value="" />
                    {namespaces.map(name => <FormSelectOption key={name} label={name} value={name} />)}
                  </FormSelect>
                </label>
                <label>Pod
                  <input aria-label="搜索 Arthas Pod" value={podQuery} onChange={event => setPodQuery(event.target.value)} placeholder="输入名称搜索" disabled={active || loading || !namespace} />
                  <select aria-label="选择 Arthas Pod" size={4} value={selectedPodKey} onChange={event => { setSelectedPodKey(event.target.value); setConfirmed(false) }} disabled={active || loading || !namespace}>
                    <option value="">{!namespace ? '先选择命名空间' : loading ? '正在读取…' : filteredPods.length ? '请选择 Pod' : '没有匹配的运行中 Pod'}</option>
                    {filteredPods.map(pod => <option key={podKey(pod)} value={podKey(pod)}>{pod.name} · {pod.ready_containers}/{pod.total_containers} Ready</option>)}
                  </select>
                </label>
                <label>容器
                  <FormSelect aria-label="选择 Arthas 容器" isDisabled={!selectedPod || active} value={container} onChange={(_, value) => setContainer(value)}>
                    {containers.length === 0 && <FormSelectOption label="先选择 Pod" value="" />}
                    {containers.map(name => <FormSelectOption key={name} label={name} value={name} />)}
                  </FormSelect>
                </label>
              </div>
              <div className="ks-arthas-selection">
                <span>当前目标 <strong>{selectedPod ? `${selectedPod.namespace} / ${selectedPod.name} / ${container || '未选容器'}` : '尚未选择'}</strong></span>
                <span>运行状态 <strong>{selectedPod ? selectedPod.phase : '—'}</strong></span>
                <span>容器终端 <strong>{execAvailable === null ? '检查中' : execAvailable ? '可用' : '未开启'}</strong></span>
              </div>
            </CardBody>
          </Card>

          <Card className="ks-arthas-card">
            <CardBody>
              <div className="ks-arthas-step-title"><span>2</span><Title headingLevel="h2" size="md">启动前确认</Title></div>
              <p className="ks-arthas-safety-text">Arthas 会附加到目标 JVM，可能造成短暂停顿；trace/watch 会增加开销。请在问题定位时临时使用，结束后执行 stop。</p>
              <Checkbox id="arthas-confirm" isChecked={confirmed} isDisabled={active} onChange={(_, value) => setConfirmed(value)} label="我确认目标是当前 Pod，并了解诊断期间的性能影响" />
              <div className="ks-arthas-actions">
                <Button variant="primary" isDisabled={!canConfigure || active || !selectedPod || !container || execAvailable !== true || !confirmed} onClick={start}>启动 Arthas 会话</Button>
                {active && <Button variant="danger" style={{ color: "#fff" }} onClick={disconnect}>结束会话</Button>}
                <span className="ks-arthas-actions-hint">仅管理员 · 最长 30 分钟</span>
              </div>
            </CardBody>
          </Card>
        </div>
      </>}

      {active && selectedPod && <div className="ks-arthas-activebar">
        <div><Label color="green" icon={<CheckCircleIcon />}>会话已启动</Label><strong>{selectedPod.namespace} / {selectedPod.name} / {container}</strong><span>目标已锁定；结束会话后才能重新选择。</span></div>
        <Button variant="danger" style={{ color: "#fff" }} onClick={disconnect}>结束会话</Button>
      </div>}

      <div className={`ks-arthas-workspace ${active ? 'is-active' : ''}`}>
        <section className="ks-arthas-main" aria-label="Arthas 工作区">
          {active && selectedPod ? <Card className="ks-arthas-card ks-arthas-session-card">
            <CardBody>
              <div className="ks-arthas-session-head">
                <div><Title headingLevel="h2" size="md">Arthas 终端</Title><p>{selectedPod.namespace} / {selectedPod.name} / {container} · 最长 30 分钟</p></div>
                <Label color={connectionState === 'connected' ? 'green' : connectionState === 'connecting' ? 'orange' : 'grey'}>{connectionState === 'connected' ? '已连接' : connectionState === 'connecting' ? '连接中' : '未连接'}</Label>
              </div>
              <ArthasTerminal
                active={active}
                clusterId={clusterID}
                container={container}
                namespace={selectedPod.namespace}
                podName={selectedPod.name}
                onAttached={() => { setAttached(true); setNotice('Arthas 已附加到 JVM，可以发送预设命令了。') }}
                onError={setError}
                onState={value => { setConnectionState(value); if (value === 'connected') setError('') }}
                ref={terminalRef}
              />
              <div className="ks-arthas-command-bar">
                <input aria-label="Arthas 命令" value={command} disabled={!attached} onChange={event => setCommand(event.target.value)} placeholder="也可以输入自定义命令，例如 thread 42" onKeyDown={event => { if (event.key === 'Enter') { send(command); setCommand('') } }} />
                <Button variant="secondary" isDisabled={!attached} onClick={() => { send(command); setCommand('') }}>发送</Button>
              </div>
              {!attached ? <Alert isInline title="等待选择 Java 进程" variant="info">
                在终端输入 Java 进程编号（选择对应应用，不要把 PID 当成编号）并回车；看到 <code>[arthas@1]$</code> 后，右侧向导的命令按钮才会启用。
                <div><Button variant="link" isInline onClick={() => { setAttached(true); setNotice('已手动确认进入 Arthas，可以发送命令。') }}>我已看到 [arthas@1]$</Button></div>
              </Alert> : null}
              <div className="ks-arthas-quick">
                <span>快速命令</span>
                {quickCommands.map(item => <Button key={item.command} variant="secondary" isDisabled={!attached} onClick={() => send(item.command)} title={item.command}>{item.label}</Button>)}
                <Button variant="secondary" isDisabled={connectionState !== 'connected'} onClick={() => terminalRef.current?.interrupt()}>Ctrl+C 中断</Button>
              </div>
            </CardBody>
          </Card> : <Card className="ks-arthas-card ks-arthas-empty">
            <CardBody>
              <Title headingLevel="h2" size="md">终端会在启动后显示在这里</Title>
              <p>先在上方完成目标选择和风险确认，启动后 Arthas 终端会占据这个区域，右侧向导始终可见。</p>
              <div className="ks-arthas-empty-steps">
                <div><span>1</span><strong>选择目标</strong><p>集群 → 命名空间 → Pod → 容器。</p></div>
                <div><span>2</span><strong>启动会话</strong><p>勾选确认，点击“启动 Arthas 会话”。</p></div>
                <div><span>3</span><strong>跟着向导做</strong><p>输入 Java 进程编号，右侧选择“内存上涨 / CPU 偏高”等场景。</p></div>
              </div>
              <Alert isInline title="第一次使用" variant="info">启动后终端会列出 Java 进程。输入对应应用的编号（通常是 1），看到 <code>[arthas@1]$</code> 后即可执行命令。</Alert>
            </CardBody>
          </Card>}
        </section>

        <aside className="ks-arthas-side" aria-label="Arthas 故障排查向导">
          <ArthasGuide ready={active && attached && connectionState === 'connected'} onSend={value => send(value)} />
        </aside>
      </div>
    </div>
  )
}
