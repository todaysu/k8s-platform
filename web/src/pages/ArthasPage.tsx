import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
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
import { fetchClusterPod, fetchClusterPods, fetchPlatformCapabilities, type ClusterPod } from '../api/clusters'
import { usePermission } from '../auth/context'
import ArthasTerminal, { type ArthasTerminalHandle } from '../components/ArthasTerminal'
import ArthasPodPicker from './ArthasPodPicker'
import { openPlatformSettings, usePlatformSettings } from './usePlatformSettings'
import ArthasGuide from '../tools/arthas/ArthasGuide'
import { useFullscreenPanel } from '../monitoring/useFullscreenPanel'
import './ArthasPage.css'

type Props = {
  clusters: { id: string; name: string }[]
}

const podKey = (pod: ClusterPod) => `${pod.namespace}/${pod.name}`

const quickCommands = [
  { label: '实时面板', command: 'dashboard -n 3' },
  { label: '最忙线程', command: 'thread -n 10' },
  { label: '阻塞线程', command: 'thread -b' },
  { label: '线程列表', command: 'thread' },
  { label: '内存概览', command: 'memory' },
  { label: 'JVM 信息', command: 'jvm' },
  { label: '类加载器', command: 'classloader -l' },
]

export default function ArthasPage({ clusters }: Props) {
  const canConfigure = usePermission('configure')
  const settings = usePlatformSettings()
  const arthasPackageMissing = Boolean(settings?.java) && !(settings?.java?.arthas_url ?? '').trim()
  const terminalRef = useRef<ArthasTerminalHandle | null>(null)
  const pageRoot = useRef<HTMLDivElement | null>(null)
  const [titleActions, setTitleActions] = useState<Element | null>(null)
  useLayoutEffect(() => {
    const page = pageRoot.current
    const title = page?.closest('.pf-v6-c-page__main')?.querySelector('.ks-title-section')
    if (!page || !title) return
    setTitleActions(title.querySelector('.ks-title-row'))
    const update = () => {
      const scroll = page.closest('.pf-v6-c-page__main')
      if (scroll) {
        // Use the viewport rather than the content-sized main element: measuring
        // that element's height can progressively shrink the workspace.
        const top = page.getBoundingClientRect().top
        page.style.setProperty('--ks-arthas-page-height', `${Math.max(0, window.innerHeight - top - 16)}px`)
      }
    }
    update()
    const observer = new ResizeObserver(update)
    observer.observe(title)
    const scroll = page.closest('.pf-v6-c-page__main')
    if (scroll) observer.observe(scroll)
    window.addEventListener('resize', update)
    return () => { observer.disconnect(); window.removeEventListener('resize', update) }
  }, [])
  const [clusterID, setClusterID] = useState(clusters[0]?.id ?? '')
  const [pods, setPods] = useState<ClusterPod[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [namespace, setNamespace] = useState('')
  const [selectedPodKey, setSelectedPodKey] = useState('')
  const [containers, setContainers] = useState<string[]>([])
  const [container, setContainer] = useState('')
  const [execAvailable, setExecAvailable] = useState<boolean | null>(null)
  const [confirmed, setConfirmed] = useState(false)
  const [active, setActive] = useState(false)
  const [connectionState, setConnectionState] = useState<'idle' | 'connecting' | 'connected'>('idle')
  const [attached, setAttached] = useState(false)
  const [command, setCommand] = useState('')
  const [sessionRevision, setSessionRevision] = useState(0)
  const [ending, setEnding] = useState(false)
  const [switchOpen, setSwitchOpen] = useState(false)
  const [guideOpen, setGuideOpen] = useState(false)
  const guidePane = useRef<HTMLElement | null>(null)
  useLayoutEffect(() => {
    if (!guideOpen) return
    const pane = guidePane.current
    const scroll = pageRoot.current?.querySelector('.ks-arthas-scroll-region')
    if (!pane || !scroll) return
    const update = () => pane.style.setProperty('--ks-arthas-guide-height', `${Math.max(160, window.innerHeight - pane.getBoundingClientRect().top - 32)}px`)
    update()
    const observer = new ResizeObserver(update)
    observer.observe(scroll)
    scroll.addEventListener('scroll', update, { passive: true })
    window.addEventListener('resize', update)
    return () => { observer.disconnect(); scroll.removeEventListener('scroll', update); window.removeEventListener('resize', update) }
  }, [guideOpen, active])
  const [fullscreen, setFullscreen] = useState(false)
  const sessionRoot = useRef<HTMLDivElement | null>(null)
  const sessionAnchor = useRef<HTMLDivElement | null>(null)
  const [sessionHost] = useState(() => document.createElement('div'))
  useLayoutEffect(() => {
    const parent = fullscreen ? document.body : sessionAnchor.current
    if (parent && active) parent.appendChild(sessionHost)
    return () => sessionHost.remove()
  }, [fullscreen, active, sessionHost])
  const endTimer = useRef<number | undefined>(undefined)
  useFullscreenPanel(fullscreen, () => setFullscreen(false), sessionRoot)
  useEffect(() => () => window.clearTimeout(endTimer.current), [])

  const selectedPod = useMemo(() => pods.find(pod => podKey(pod) === selectedPodKey) ?? null, [pods, selectedPodKey])
  const filteredPods = useMemo(() => pods.filter(pod => pod.namespace === namespace), [pods, namespace])
  const namespaces = useMemo(() => [...new Set(pods.map(pod => pod.namespace))].sort(), [pods])

  useEffect(() => {
    let active = true
    void fetchPlatformCapabilities().then(value => { if (active) setExecAvailable(value.pod_exec_enabled) }).catch(() => { if (active) setExecAvailable(false) })
    return () => { active = false }
  }, [])

  useEffect(() => {
    if (!clusterID) return
    let active = true
    setLoading(true); setError(''); setNamespace(''); setConfirmed(false); setPods([]); setSelectedPodKey(''); setContainers([]); setContainer(''); setActive(false)
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
      setFullscreen(false)
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
    setConnectionState('connecting')
    setActive(true)
  }

  const disconnect = () => {
    if (ending) return
    setSwitchOpen(false)
    setConfirmed(false)
    setEnding(true)
    setError('')
    setNotice('')
    terminalRef.current?.stop()
    endTimer.current = window.setTimeout(() => { setActive(false); setAttached(false); setConnectionState('idle'); setFullscreen(false); setEnding(false) }, 1000)
  }

  const send = (value: string, fillOnly = false) => {
    if (!value.trim()) return
    if (!active || connectionState !== 'connected') { setError('请先启动并连接 Arthas 会话。'); return }
    if (!attached) { setError('请先在终端输入 Java 进程编号，看到 [arthas@1]$ 后再发送命令。'); return }
    if (fillOnly) { setCommand(value); setNotice('命令已填入下方输入框，请把类名/方法名改成实际值后发送。'); return }
    if (!terminalRef.current?.send(value)) { setError('Arthas 尚未连接，请稍等或检查错误信息。'); return }
    setNotice(`已发送：${value}`)
  }

  const guideToggle = <div className="ks-arthas-toolbar"><Button variant="link" aria-expanded={guideOpen} aria-controls="arthas-guide-panel" onClick={() => setGuideOpen(value => !value)}>{guideOpen ? '收起故障排查向导' : '故障排查向导'}</Button></div>

  return (
    <div ref={pageRoot} className={`ks-arthas-page ${active ? 'is-session' : ''}`}>
      {titleActions ? createPortal(guideToggle, titleActions) : guideToggle}
      <div className="ks-arthas-scroll-region" aria-label="Arthas 可滚动工作区">
      {!canConfigure && <Alert isInline title="需要管理员权限" variant="warning">Arthas 可以读取和修改 JVM 运行时，仅允许平台管理员使用。</Alert>}
      {arthasPackageMissing && <p className="ks-arthas-package-hint">
        默认 Arthas 版本：4.3.5。如需更改，请到<button type="button" onClick={() => openPlatformSettings('java')}>平台参数</button>更改。
      </p>}
      {error && <Alert isInline title={error} variant="danger" />}

        <Card className="ks-arthas-card ks-arthas-target-card" aria-label="诊断目标">
          <CardBody>
              <div className="ks-arthas-form">
                <label>集群
                  <FormSelect aria-label="选择 Arthas 集群" isDisabled={active} value={clusterID} onChange={(_, value) => { setClusterID(value); setNamespace(''); setSelectedPodKey(''); setContainer(''); setConfirmed(false) }}>
                    <FormSelectOption label="请选择集群" value="" />
                    {clusters.map(cluster => <FormSelectOption key={cluster.id} label={cluster.name} value={cluster.id} />)}
                  </FormSelect>
                </label>
                <label>命名空间
                  <FormSelect aria-label="选择 Arthas 命名空间" isDisabled={active || loading || !clusterID} value={namespace} onChange={(_, value) => { setNamespace(value); setSelectedPodKey(''); setContainers([]); setContainer(''); setConfirmed(false) }}>
                    <FormSelectOption label={loading ? '正在读取…' : '请选择命名空间'} value="" />
                    {namespaces.map(name => <FormSelectOption key={name} label={name} value={name} />)}
                  </FormSelect>
                </label>
                <div className="ks-arthas-field">
                  <span>Pod</span>
                  <ArthasPodPicker disabled={active || !namespace} loading={loading} namespace={namespace} onChange={value => { setSelectedPodKey(value); setConfirmed(false) }} pods={filteredPods} value={selectedPodKey} />
                </div>
                <label>容器
                  <FormSelect aria-label="选择 Arthas 容器" isDisabled={!selectedPod || active} value={container} onChange={(_, value) => setContainer(value)}>
                    {containers.length === 0 && <FormSelectOption label="先选择 Pod" value="" />}
                    {containers.map(name => <FormSelectOption key={name} label={name} value={name} />)}
                  </FormSelect>
                </label>
              </div>
              <div className="ks-arthas-target-actions">
                {active ? <div className="ks-arthas-switch">
                  <Button variant="secondary" isDisabled={ending} aria-expanded={switchOpen} onClick={() => setSwitchOpen(value => !value)}>{ending ? '正在结束…' : '切换目标'}</Button>
                  {switchOpen && <><button className="ks-arthas-switch-dismiss" aria-label="关闭切换目标确认" onClick={() => setSwitchOpen(false)} /><div className="ks-arthas-switch-confirm" role="dialog" aria-label="切换诊断目标" onKeyDown={event => { if (event.key === 'Escape') setSwitchOpen(false) }}>
                    <strong>切换诊断目标</strong><p>切换前需结束当前会话。</p>
                    <div><Button variant="link" onClick={() => setSwitchOpen(false)}>取消</Button><Button variant="primary" onClick={disconnect}>结束并切换</Button></div>
                  </div></>}
                </div> : <Button variant="primary" isDisabled={!canConfigure || !selectedPod || !container || execAvailable !== true || !confirmed} onClick={start}>启动终端</Button>}
              </div>
              <div className="ks-arthas-target-note">
                {active ? <span>当前会话已锁定目标</span> : <><Checkbox id="arthas-confirm" isChecked={confirmed} onChange={(_, value) => setConfirmed(value)} label="确认诊断目标，并了解附加 JVM 及 trace/watch 的性能影响" /><span>仅管理员 · 最长 30 分钟</span></>}
              </div>
              {execAvailable === false && <Alert isInline title="平台未开启 Pod Exec，无法启动 Arthas。" variant="warning" />}
          </CardBody>
        </Card>
      <div className={`ks-arthas-workspace ${guideOpen ? 'has-guide' : ''} ${active ? 'is-active' : ''}`}>
        <section className="ks-arthas-main" aria-label="Arthas 工作区">
          {active && selectedPod ? <><div ref={sessionAnchor} />{createPortal(<Card ref={sessionRoot} className={`ks-arthas-card ks-arthas-session-card ${fullscreen ? 'is-fullscreen' : ''}`} role={fullscreen ? 'dialog' : undefined} aria-modal={fullscreen || undefined} aria-label={fullscreen ? 'Arthas 全屏终端' : undefined}>
            <CardBody>
              <div className="ks-arthas-session-head">
                <div><Title headingLevel="h2" size="md">交互终端</Title></div>
                <div className="ks-arthas-terminal-actions"><Label color={connectionState === 'connected' ? 'green' : connectionState === 'connecting' ? 'orange' : 'grey'}>{connectionState === 'connected' ? attached ? 'JVM 已连接' : '通道已连接' : connectionState === 'connecting' ? '正在连接' : '已断开'}</Label>
                {connectionState === 'idle' && <Button variant="secondary" isDisabled={ending} onClick={() => { setAttached(false); setError(''); setNotice(''); setConnectionState('connecting'); setSessionRevision(value => value + 1) }}>重新连接</Button>}
                <Button variant="secondary" onClick={() => terminalRef.current?.clear()}>清屏</Button>
                <Button variant="secondary" isDanger isDisabled={ending} onClick={disconnect}>{ending ? '正在结束…' : '结束会话'}</Button>
                <Button variant="secondary" onClick={() => setFullscreen(value => !value)}>{fullscreen ? '退出全屏' : '全屏'}</Button></div>
              </div>
              <ArthasTerminal
                key={sessionRevision}
                active={active}
                clusterId={clusterID}
                container={container}
                namespace={selectedPod.namespace}
                podName={selectedPod.name}
                onAttached={() => { setAttached(true) }}
                onError={value => { if (!ending) setError(value) }}
                onState={value => { setConnectionState(value); if (value === 'idle') setAttached(false); if (value === 'connected') setError('') }}
                ref={terminalRef}
              />
              <div className="ks-arthas-command-bar">
                <input aria-label="Arthas 命令" value={command} disabled={!attached} onChange={event => setCommand(event.target.value)} placeholder="也可以输入自定义命令，例如 thread 42" onKeyDown={event => { if (event.key === 'Enter') { send(command); setCommand('') } }} />
                <Button variant="secondary" isDisabled={!attached} onClick={() => { send(command); setCommand('') }}>发送</Button>
              </div>
              {notice && <div className="ks-arthas-command-notice" role="status">{notice}</div>}
              {!attached && connectionState !== 'idle' ? <Alert isInline title="等待选择 Java 进程" variant="info">
                在终端输入 Java 进程编号（选择对应应用，不要把 PID 当成编号）并回车；看到 <code>[arthas@1]$</code> 后，快捷命令和向导操作才会启用。不要选择 arthas-boot.jar 进程。
                <div><Button variant="link" isInline onClick={() => { setAttached(true); setNotice('已手动确认进入 Arthas，可以发送命令。') }}>我已看到 [arthas@1]$</Button></div>
              </Alert> : null}
              <div className="ks-arthas-quick">
                <span>快速命令</span>
                {quickCommands.map(item => <Button key={item.command} variant="secondary" isDisabled={!attached} onClick={() => send(item.command)} title={item.command}>{item.label}</Button>)}
                <Button variant="secondary" isDisabled={connectionState !== 'connected'} onClick={() => terminalRef.current?.interrupt()}>Ctrl+C 中断</Button>
              </div>
            </CardBody>
          </Card>, sessionHost)}</> : null}
        </section>

        <aside ref={guidePane} tabIndex={0} id="arthas-guide-panel" className="ks-arthas-side" hidden={!guideOpen} aria-label="Arthas 故障排查向导">
          <ArthasGuide ready={active && attached && connectionState === 'connected'} onSend={value => send(value)} />
        </aside>
      </div>
      </div>
    </div>
  )
}
