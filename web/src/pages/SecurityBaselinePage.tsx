import { Component, useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Alert, Button, Label, Modal, ModalBody, ModalFooter, ModalHeader, Spinner } from '@patternfly/react-core'
import { ExclamationTriangleIcon, PlayIcon, SearchIcon, ShieldAltIcon, SyncAltIcon } from '@patternfly/react-icons'
import {
  fetchFleetSecurityBaselines,
  fetchSecurityBaselineHistory,
  fetchSecurityBenchmarks,
  addSecurityWhitelist,
  deleteSecurityWhitelist,
  scanClusterSecurityBaseline,
  type ClusterSecurityBaseline,
  type SecurityBaselineFinding,
  type SecurityBenchmark,
  type SecurityBaselineHistory,
  type SecurityBaselineRecord,
} from '../api/clusters'
import { usePermission } from '../auth/context'
import SecurityScanDialog from '../components/SecurityScanDialog'
import SettingsRequirementAlert from '../components/SettingsRequirementAlert'
import { usePlatformSettings } from './usePlatformSettings'
import './SecurityBaselinePage.css'

type ScanView = 'all' | 'unscanned' | 'attention' | 'passed' | 'failed' | 'running'
const statusColor = { PASS: 'green', FAIL: 'red', WARN: 'orange', WHITELISTED: 'blue' } as const
const statusName = { PASS: '通过', FAIL: '失败', WARN: '人工复核', WHITELISTED: '白名单' } as const

function scanState(item: ClusterSecurityBaseline): Exclude<ScanView, 'all'> {
  const scan = item.scan
  if (!scan) return 'unscanned'
  if (scan.status === 'queued' || scan.status === 'running') return 'running'
  if (scan.status === 'failed') return 'failed'
  const summary = scan.report?.summary
  if (!summary) return 'failed'
  if ((summary.failed ?? 0) > 0 || (summary.warnings ?? 0) > 0) return 'attention'
  return 'passed'
}

const stateLabel: Record<Exclude<ScanView, 'all'>, string> = {
  unscanned: '未扫描', running: '扫描中', failed: '扫描失败', attention: '需要关注', passed: '已通过',
}
class SecurityBaselineErrorBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false }
  static getDerivedStateFromError() { return { failed: true } }
  render() {
    if (this.state.failed) return <div className="ks-security-crash" role="alert">
      <h2>安全扫描页面加载失败</h2>
      <p>报告数据不完整或读取过程中发生了错误。页面已隔离，不会影响其他功能。</p>
      <button onClick={() => this.setState({ failed: false })}>重新加载页面</button>
    </div>
    return this.props.children
  }
}

const scannerName = () => 'kube-bench 节点 + 集群侧 API'
const showTime = (value?: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—'

const progressSteps = [
  { phase: 'queued', label: '提交任务', detail: '写入扫描记录并等待执行槽位' },
  { phase: 'connecting', label: '连接集群', detail: '读取凭据并连接 Kubernetes API' },
  { phase: 'discovering_nodes', label: '发现节点', detail: '识别节点数量及 x86 / ARM 架构' },
  { phase: 'creating_jobs', label: '启动扫描 Pod', detail: '为节点创建临时高权限 Job' },
  { phase: 'scanning_nodes', label: '执行 kube-bench', detail: '检查文件权限和组件启动参数' },
  { phase: 'collecting_report', label: '整理报告', detail: '收集结果并回收 Job / Pod' },
] as const

function ScanProgress({ scan }: { scan: SecurityBaselineRecord }) {
  const progress = scan.report?.progress
  const effectivePhase = progress?.phase === 'failed' ? progress.failed_phase : progress?.phase
  const activeIndex = Math.max(0, progressSteps.findIndex(step => step.phase === effectivePhase))
  const complete = progress?.phase === 'completed'
  return <div className="ks-security-progress">
    <div className="ks-security-progress__heading">
      <div><strong>{scan.status === 'failed' ? '扫描已停止' : '扫描执行进度'}</strong><span>{progress?.message || (scan.status === 'queued' ? '扫描任务已提交。' : '正在执行 kube-bench 扫描。')}</span></div>
      {(progress?.total_nodes ?? 0) > 0 && <b>{progress?.completed_nodes ?? 0} / {progress?.total_nodes} 节点</b>}
    </div>
    <ol>{progressSteps.map((step, index) => {
      const failedHere = scan.status === 'failed' && index === activeIndex
      const state = complete || index < activeIndex ? 'complete' : failedHere ? 'failed' : index === activeIndex ? 'active' : 'pending'
      return <li className={`is-${state}`} key={step.phase}><i>{complete || index < activeIndex ? '✓' : failedHere ? '!' : index + 1}</i><div><strong>{step.label}</strong><span>{step.detail}</span></div></li>
    })}</ol>
    {scan.status === 'failed' && <Alert isInline title="该次扫描失败" variant="danger">{scan.error || '请检查集群权限、扫描镜像和镜像仓库网络。'}</Alert>}
  </div>
}

function ComparisonPanel({ history, currentScan, benchmarkLabel }: { history: SecurityBaselineHistory | null; currentScan?: SecurityBaselineRecord; benchmarkLabel: (id?: string) => string }) {
  const comparison = history?.comparison
  const previous = comparison?.previous_scan_id ? history?.records.find(record => record.id === comparison.previous_scan_id) : undefined
  const current = comparison?.current_scan_id ? history?.records.find(record => record.id === comparison.current_scan_id) : undefined
  const changes = [
    ...(comparison?.new_risks ?? []).map(item => ({ ...item, kind: '新增风险' })),
    ...(comparison?.resolved ?? []).map(item => ({ ...item, kind: '已解决' })),
    ...(comparison?.changed ?? []).map(item => ({ ...item, kind: '状态变化' })),
  ]
  if (!comparison?.previous_scan_id || !previous || !current) {
    return <section className="ks-security-compare is-empty">
      <div><h3>扫描对比</h3><p>至少完成两次成功扫描后，这里会显示新增风险、已解决项和状态变化。当前只展示本次汇总。</p></div>
    </section>
  }
  return <section className="ks-security-compare">
    <div className="ks-security-compare__head"><div><h3>最近两次扫描差异</h3><p>{showTime(previous.finished_at ?? previous.started_at)} → {showTime(current.finished_at ?? current.started_at)}</p></div><span>{changes.length ? `${changes.length} 项变化` : '结果一致'}</span></div>
    <div className="ks-security-compare__scores">
      <div><span>上次</span><strong>{previous.report?.summary?.score ?? 0}%</strong><small>{benchmarkLabel(previous.report?.benchmark)}</small></div>
      <div><span>本次</span><strong>{currentScan?.report?.summary?.score ?? current.report?.summary?.score ?? 0}%</strong><small>{benchmarkLabel(current.report?.benchmark)}</small></div>
      <div className="is-new"><span>新增风险</span><strong>{comparison.new_risks?.length ?? 0}</strong></div>
      <div className="is-resolved"><span>已解决</span><strong>{comparison.resolved?.length ?? 0}</strong></div>
      <div className="is-changed"><span>状态变化</span><strong>{comparison.changed?.length ?? 0}</strong></div>
    </div>
    {(() => {
      const remediation = comparison.remediation ?? []
      const count = (kind: string) => remediation.filter(item => item.result === kind).length
      const labels: Record<string, string> = { remediated: '整改成功', not_remediated: '未整改', new_risk: '新增风险', whitelisted: '白名单', not_run: '未执行' }
      return <div className="ks-security-remediation">
        <div className="ks-security-remediation__head"><h4>整改结果</h4><span>上次不合规项在本次扫描后的状态</span></div>
        <div className="ks-security-remediation__counts">
          <span className="is-remediated">整改成功 {count('remediated')}</span>
          <span className="is-not-remediated">未整改 {count('not_remediated')}</span>
          <span className="is-new">新增风险 {count('new_risk')}</span>
          <span className="is-whitelisted">白名单 {count('whitelisted')}</span>
        </div>
        {(() => {
          const visible = remediation.filter(item => item.result !== 'not_remediated')
          return visible.length === 0 ? <p className="ks-security-remediation__empty">{count('not_remediated') > 0 ? '未整改项不在对比区重复列出，请在下方“控制项明细 → 失败/人工复核”中查看。' : '上次扫描没有不合规项，或本次没有可对比的整改结果。'}</p> : <div className="ks-security-remediation__list">
          {visible.slice(0, 20).map(item => <div key={`${item.control_id}-${item.result}`}>
            <span className={`is-${item.result}`}>{labels[item.result] ?? item.result}</span>
            <code>{item.control_id}</code>
            <p>{item.title}</p>
            <small>{item.previous_status || '—'} → {item.current_status || '—'}</small>
          </div>)}
          {visible.length > 20 && <small>还有 {visible.length - 20} 项，请在下方控制项明细中查看。</small>}
        </div>
        })()}
      </div>
    })()}
  </section>
}

function Finding({ finding, onWhitelist, onRemoveWhitelist }: { finding: SecurityBaselineFinding; onWhitelist?: (finding: SecurityBaselineFinding) => void; onRemoveWhitelist?: (finding: SecurityBaselineFinding) => void }) {
  const [open, setOpen] = useState(false)
  return <article className={`ks-security-finding is-${finding.status.toLowerCase()}`}>
    <button aria-expanded={open} onClick={() => setOpen(value => !value)}>
      <Label color={statusColor[finding.status]} isCompact>{statusName[finding.status]}</Label>
      <code>{finding.control_id}</code>
      <span><strong>{finding.title}</strong><small>{finding.category} · {finding.severity || '未分级'} · 影响 {finding.affected} 个资源</small></span>
      <b>{open ? '收起' : '查看证据'}</b>
    </button>
    {open && <div className="ks-security-finding__detail">
      <section><h4>证据摘要</h4><p>{finding.evidence}</p></section>
      {finding.resources.length > 0 && <section><h4>受影响资源样例</h4><ul>{finding.resources.map(resource => <li key={resource}>{resource}</li>)}</ul></section>}
      <section className="is-remediation"><h4>整改建议</h4><p>{finding.remediation}</p></section>
      {finding.whitelist_reason && <section><h4>白名单原因</h4><p>{finding.whitelist_reason}</p></section>}
      {finding.status !== 'PASS' && finding.status !== 'WHITELISTED' && onWhitelist && <div className="ks-security-whitelist-action"><Button variant="secondary" onClick={() => onWhitelist(finding)}>加入白名单</Button><small>仅对当前集群和控制项生效，需要填写原因。</small></div>}
      {finding.status === 'WHITELISTED' && onRemoveWhitelist && <div className="ks-security-whitelist-action"><Button variant="secondary" onClick={() => onRemoveWhitelist(finding)}>移出白名单</Button><small>移除后该控制项会恢复为失败或人工复核。</small></div>}
    </div>}
  </article>
}

function SecurityBaselineContent() {
  const canConfigure = usePermission('configure')
  const settings = usePlatformSettings()
  const securitySettings = settings?.security
  const securityImageConfigured = Boolean(securitySettings && [securitySettings.kube_bench_image, securitySettings.kube_bench_image_amd64, securitySettings.kube_bench_image_arm64].some(value => (value ?? '').trim()))
  const securityImageMissing = Boolean(securitySettings) && !securityImageConfigured
  const usingDefaultImage = Boolean(securitySettings && securityImageConfigured && !settings?.updated_at?.security)
  const [clusters, setClusters] = useState<ClusterSecurityBaseline[]>([])
  const [benchmarks, setBenchmarks] = useState<SecurityBenchmark[]>([])
  const benchmarkLabel = (id?: string) => benchmarks.find(item => item.id === id)?.name ?? (id ? `CIS Benchmark v${id.replace(/^cis-/, '').replace(/\.0$/, '')}.0` : '自动识别')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [selectedID, setSelectedID] = useState('')
  const [query, setQuery] = useState('')
  const [environment, setEnvironment] = useState('ALL')
  const [view, setView] = useState<ScanView>('all')
  const [dialogOpen, setDialogOpen] = useState(false)
  const [detailOpen, setDetailOpen] = useState(false)
  const [detailFullscreen, setDetailFullscreen] = useState(false)
  const [dialogClusterID, setDialogClusterID] = useState('')
  const [history, setHistory] = useState<SecurityBaselineHistory | null>(null)
  const [historyLoading, setHistoryLoading] = useState(false)
  const [viewedScanID, setViewedScanID] = useState<number | null>(null)
  const [findingFilter, setFindingFilter] = useState<'fail' | 'warn' | 'pass' | 'whitelisted' | 'all' | ''>('')
  const [whitelistAddMode, setWhitelistAddMode] = useState(false)
  const findingsRef = useRef<HTMLElement | null>(null)
  const [whitelistTarget, setWhitelistTarget] = useState<SecurityBaselineFinding | null>(null)
  const [whitelistReason, setWhitelistReason] = useState('')
  const [whitelistError, setWhitelistError] = useState('')
  const [whitelistSaving, setWhitelistSaving] = useState(false)
  const [securityRefresh, setSecurityRefresh] = useState(0)

  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true)
    setError('')
    try {
      const result = await fetchFleetSecurityBaselines()
      setClusters(result.clusters ?? [])
      setSelectedID(current => current || result.clusters?.[0]?.cluster_id || '')
    } catch (cause) { setError(cause instanceof Error ? cause.message : '读取安全扫描结果失败。') }
    finally { if (!silent) setLoading(false) }
  }, [])
  useEffect(() => { void load() }, [load])
  useEffect(() => {
    let active = true
    void fetchSecurityBenchmarks().then(items => { if (active && Array.isArray(items)) setBenchmarks(items) }).catch(() => {})
    return () => { active = false }
  }, [])

  const counts = useMemo(() => clusters.reduce((values, item) => {
    values[scanState(item)]++
    return values
  }, { unscanned: 0, running: 0, failed: 0, attention: 0, passed: 0 }), [clusters])
  const environments = useMemo(() => ['ALL', ...new Set(clusters.map(item => item.environment).filter(Boolean))], [clusters])
  const visible = useMemo(() => {
    const keyword = query.trim().toLowerCase()
    return clusters.filter(item => (view === 'all' || scanState(item) === view)
      && (environment === 'ALL' || item.environment === environment)
      && (!keyword || `${item.cluster_name} ${item.environment} ${item.technology_stack}`.toLowerCase().includes(keyword)))
  }, [clusters, environment, query, view])
  const selected = clusters.find(item => item.cluster_id === selectedID)
  useEffect(() => {
    setHistory(null)
    setViewedScanID(null)
    if (!selectedID) return
    let active = true
    const requestID = selectedID
    setHistoryLoading(true)
    void fetchSecurityBaselineHistory(requestID).then(result => {
      if (!active) return
      setHistory(result)
      setViewedScanID(result.records[0]?.id ?? null)
    }).catch(() => {
      if (active) setHistory(null)
    }).finally(() => {
      if (active) setHistoryLoading(false)
    })
    return () => { active = false }
  }, [selectedID, selected?.scan?.finished_at, securityRefresh])
  useEffect(() => {
    if (!detailOpen) return
    const previous = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => { document.body.style.overflow = previous }
  }, [detailOpen])
  useEffect(() => {
    if (!detailOpen) return
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      // Esc always leaves the detail view, even when it is fullscreen.
      setDetailFullscreen(false)
      setDetailOpen(false)
    }
    document.addEventListener('keydown', escape)
    return () => document.removeEventListener('keydown', escape)
  }, [detailOpen, detailFullscreen])
  const selectedScan = selected?.scan
  const historyRecords = (history?.records ?? []).filter(record => !selectedID || record.cluster_id === selectedID)
  const displayedScan: SecurityBaselineRecord | undefined = selectedScan?.status === 'queued' || selectedScan?.status === 'running'
    ? selectedScan
    : historyRecords.find(record => record.id === viewedScanID) ?? historyRecords[0] ?? selectedScan
  const findings = displayedScan?.report?.findings ?? []
  const coverage = displayedScan?.report?.coverage ?? { included: [], not_covered: [] }
  const summary = displayedScan?.report?.summary ?? { controls: 0, passed: 0, failed: 0, warnings: 0, whitelisted: 0, score: 0 }
  const cisFindingCount = findings.filter(finding => finding.control_id.startsWith('CIS-')).length
  const clusterFindingCount = findings.filter(finding => finding.control_id.startsWith('CLUSTER-')).length
  useEffect(() => { setFindingFilter('') }, [displayedScan?.id])
  const filteredFindings = findings.filter(finding => {
    if (!findingFilter) return false
    if (whitelistAddMode) return finding.status === 'FAIL' || finding.status === 'WARN'
    if (findingFilter === 'all') return true
    if (findingFilter === 'fail') return finding.status === 'FAIL'
    if (findingFilter === 'warn') return finding.status === 'WARN'
    if (findingFilter === 'whitelisted') return finding.status === 'WHITELISTED'
    return finding.status === 'PASS' || finding.status === 'WHITELISTED'
  })

  const hasActiveScans = counts.running > 0
  useEffect(() => {
    if (!hasActiveScans) return
    const timer = window.setInterval(() => { if (!document.hidden) void load(true) }, 4000)
    return () => window.clearInterval(timer)
  }, [hasActiveScans, load])

  const showFindings = (filter: 'fail' | 'warn' | 'pass' | 'whitelisted' | 'all', addMode = false) => {
    setWhitelistAddMode(addMode)
    setFindingFilter(filter)
    window.setTimeout(() => { if (typeof findingsRef.current?.scrollIntoView === 'function') findingsRef.current.scrollIntoView({ behavior: 'smooth', block: 'start' }) }, 0)
  }

  const openWhitelist = () => {
    const count = summary.whitelisted ?? 0
    showFindings(count === 0 ? 'fail' : 'whitelisted', count === 0)
  }

  const submitWhitelist = async () => {
    if (!selectedID || !whitelistTarget) return
    if (whitelistReason.trim().length < 3) { setWhitelistError('请填写至少 3 个字符的白名单原因。'); return }
    setWhitelistSaving(true); setWhitelistError('')
    try {
      await addSecurityWhitelist(selectedID, whitelistTarget.control_id, whitelistReason.trim())
      setWhitelistTarget(null); setWhitelistReason(''); setWhitelistAddMode(false); setFindingFilter('whitelisted'); setSecurityRefresh(value => value + 1)
      await load(true)
    } catch (cause) { setWhitelistError(cause instanceof Error ? cause.message : '加入白名单失败。') }
    finally { setWhitelistSaving(false) }
  }

  const removeWhitelist = async (controlID: string) => {
    if (!selectedID) return
    setWhitelistError('')
    try {
      await deleteSecurityWhitelist(selectedID, controlID)
      setSecurityRefresh(value => value + 1)
      await load(true)
    } catch (cause) { setWhitelistError(cause instanceof Error ? cause.message : '移除白名单失败。') }
  }

  const openScanDialog = (clusterID = '') => {
    setDialogClusterID(clusterID)
    if (clusterID) setSelectedID(clusterID)
    setDetailOpen(false)
    setDialogOpen(true)
  }

  const launchScans = async (clusterIDs: string[], benchmark: string) => {
    const queue = [...clusterIDs]
    const failures: string[] = []
    let started = 0
    const worker = async () => {
      for (;;) {
        const id = queue.shift()
        if (!id) return
        try {
          await scanClusterSecurityBaseline(id, benchmark)
          started++
        } catch (cause) {
          const name = clusters.find(item => item.cluster_id === id)?.cluster_name ?? id
          failures.push(`${name}：${cause instanceof Error ? cause.message : '提交失败'}`)
        }
      }
    }
    await Promise.all(Array.from({ length: Math.min(2, clusterIDs.length) }, () => worker()))
    await load(true)
    if (started === 0) {
      throw new Error(failures.join('；') || '没有任务被提交，请稍后重试。')
    }
    setDialogOpen(false)
    setSelectedID(clusterIDs[0])
    setDetailOpen(true)
    setError(failures.length > 0 ? `部分任务未提交：${failures.join('；')}` : '')
  }

  return <div className="ks-security-page">
    <section className="ks-security-summary" aria-label="安全扫描概况">
      <button className={view === 'all' ? 'is-active' : ''} onClick={() => setView('all')}><span>纳管集群</span><strong>{clusters.length}</strong><small>全部扫描对象</small></button>
      <button className={`is-unscanned ${view === 'unscanned' ? 'is-active' : ''}`} onClick={() => setView('unscanned')}><span>未扫描</span><strong>{counts.unscanned}</strong><small>需要建立首次结果</small></button>
      <button className={`is-attention ${view === 'attention' ? 'is-active' : ''}`} onClick={() => setView('attention')}><span>需要关注</span><strong>{counts.attention}</strong><small>存在失败或复核项</small></button>
      <button className={`is-passed ${view === 'passed' ? 'is-active' : ''}`} onClick={() => setView('passed')}><span>已通过</span><strong>{counts.passed}</strong><small>最近扫描无异常项</small></button>
      <button className={`is-failed ${view === 'failed' ? 'is-active' : ''}`} onClick={() => setView('failed')}><span>任务失败</span><strong>{counts.failed}</strong><small>检查权限、镜像或网络</small></button>
    </section>

    {securityImageMissing && <SettingsRequirementAlert section="security" title="安全扫描缺少 kube-bench 镜像配置" variant="danger">
      未配置扫描镜像时平台无法创建 kube-bench Pod。请先在参数配置中填写通用镜像或 amd64 镜像，并确认目标节点可以拉取该镜像。
    </SettingsRequirementAlert>}
    {usingDefaultImage && <SettingsRequirementAlert section="security" title="当前使用平台默认 kube-bench 镜像" variant="info">
      内网环境建议在参数配置中指定内部镜像和 ImagePullSecret，避免扫描 Pod 拉取公网镜像失败。
    </SettingsRequirementAlert>}

    <section className="ks-security-register">
      <div className="ks-security-register__header">
        <div><h2>集群扫描台账</h2><p>每次结果都会保存；完整扫描包含节点 kube-bench 和集群侧只读 API。完成第二次扫描后，平台会自动对比最近两次成功结果。</p></div>
        <div className="ks-security-register__actions">
          <Button icon={<PlayIcon />} isDisabled={!canConfigure || clusters.length === 0 || loading || securityImageMissing} onClick={() => openScanDialog()} variant="primary">手动执行扫描</Button>
          <Button icon={<SyncAltIcon />} isDisabled={loading} onClick={() => void load()} variant="secondary">刷新</Button>
        </div>
      </div>
      {error && <div className="ks-security-register__message"><Alert isInline title="操作未完成" variant="danger">{error}</Alert></div>}
      <div className="ks-security-filters">
        <label><span>搜索集群</span><div className="ks-security-search"><SearchIcon /><input aria-label="搜索安全扫描集群" placeholder="集群名称、环境或技术栈" value={query} onChange={event => setQuery(event.target.value)} /></div></label>
        <label><span>环境</span><select aria-label="筛选安全扫描环境" value={environment} onChange={event => setEnvironment(event.target.value)}>{environments.map(value => <option value={value} key={value}>{value === 'ALL' ? '全部环境' : value}</option>)}</select></label>
        <label><span>扫描状态</span><select aria-label="筛选安全扫描状态" value={view} onChange={event => setView(event.target.value as ScanView)}><option value="all">全部状态</option>{Object.entries(stateLabel).map(([value, label]) => <option value={value} key={value}>{label}</option>)}</select></label>
      </div>
      {loading ? <div className="ks-security-empty"><Spinner size="lg" />正在读取扫描台账…</div> : visible.length === 0 ? <div className="ks-security-empty"><ShieldAltIcon /><strong>没有匹配的集群</strong><span>请调整搜索或筛选条件。</span></div> : <div className="ks-security-table-wrap"><table className="ks-security-table">
        <thead><tr><th>集群</th><th>状态</th><th>通过率</th><th>失败 / 复核</th><th>最近扫描</th><th>操作</th></tr></thead>
        <tbody>{visible.map(item => {
          const state = scanState(item)
          const report = item.scan?.report
          const busy = state === 'running'
          const openDetail = () => { setSelectedID(item.cluster_id); setDetailOpen(true) }
          return <tr
            className={`${state === 'unscanned' ? 'is-unscanned' : ''} ${selectedID === item.cluster_id ? 'is-selected' : ''}`}
            key={item.cluster_id}
            onClick={openDetail}
            onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); openDetail() } }}
            tabIndex={0}
          >
            <td><button className="ks-security-cluster-link" onClick={event => { event.stopPropagation(); openDetail() }}><strong>{item.cluster_name}</strong><small>{item.environment || '未分类'} · {item.technology_stack || '未标记'}</small></button></td>
            <td><span className={`ks-security-state is-${state}`}>{state === 'attention' && <ExclamationTriangleIcon />}{stateLabel[state]}</span></td>
            <td>{item.scan?.status === 'completed' ? `${report?.summary.score ?? 0}%` : '—'}</td>
            <td>{item.scan?.status === 'completed' ? `${report?.summary.failed ?? 0} / ${report?.summary.warnings ?? 0}` : '—'}</td>
            <td>{showTime(item.scan?.finished_at ?? item.scan?.started_at)}</td>
            <td><div className="ks-security-row-actions"><Button isInline onClick={event => { event.stopPropagation(); openDetail() }} variant="link">扫描详情</Button><Button isInline isDisabled={!canConfigure || busy || securityImageMissing} onClick={event => { event.stopPropagation(); openScanDialog(item.cluster_id) }} variant="link">{busy ? '扫描中' : item.scan ? '重新扫描' : '开始扫描'}</Button></div></td>
          </tr>
        })}</tbody>
      </table></div>}
    </section>

    {detailOpen && selected && createPortal(<div className="ks-security-drawer-backdrop" onMouseDown={event => { if (event.target === event.currentTarget) { setDetailOpen(false); setDetailFullscreen(false) } }}>
      <section aria-label={`${selected.cluster_name} 安全扫描结果`} aria-modal="true" className={`ks-security-detail ks-security-drawer ${detailFullscreen ? 'is-fullscreen' : ''}`} role="dialog">
        <header className="ks-security-drawer__head">
          <div>
            <span className="ks-security-eyebrow">集群安全态势</span>
            <h2>{selected.cluster_name}</h2>
            <p>{selected.environment || '未分类'} · {selected.technology_stack || '未标记'} · {displayedScan ? showTime(displayedScan.finished_at ?? displayedScan.started_at) : '尚未扫描'}</p>
          </div>
          <div className="ks-security-drawer__score"><strong>{summary.score}%</strong><span>通过率</span></div>
          <div className="ks-security-drawer__actions">
            <Button aria-pressed={detailFullscreen} variant="link" onClick={() => setDetailFullscreen(value => !value)}>{detailFullscreen ? '退出全屏' : '全屏'}</Button>
            <button aria-label="关闭集群扫描详情" className="ks-security-drawer__close" onClick={() => { setDetailOpen(false); setDetailFullscreen(false) }} type="button">×</button>
          </div>
        </header>

        <div className="ks-security-drawer__body">
          {historyLoading && !displayedScan ? <div className="ks-security-empty"><Spinner size="lg" />正在读取历史结果…</div>
            : !displayedScan ? <div className="ks-security-empty"><ShieldAltIcon /><strong>该集群尚未扫描</strong><span>手动执行首次扫描后，这里会显示控制项、证据和整改建议。</span><Button isDisabled={!canConfigure || securityImageMissing} onClick={() => openScanDialog(selected.cluster_id)}>开始扫描</Button></div>
            : displayedScan.status === 'queued' || displayedScan.status === 'running' ? <ScanProgress scan={displayedScan} />
            : <>
              {displayedScan.status === 'failed' ? <ScanProgress scan={displayedScan} /> : <>
                <div className="ks-security-detail__summary">
                  <button className={findingFilter === 'all' ? 'is-active' : ''} onClick={() => showFindings('all')} type="button"><span>控制项</span><strong>{summary.controls}</strong><small>查看全部</small></button>
                  <button className={`is-pass ${findingFilter === 'pass' ? 'is-active' : ''}`} onClick={() => showFindings('pass')} type="button"><span>通过</span><strong>{summary.passed}</strong><small>含白名单</small></button>
                  <button className={`is-fail ${findingFilter === 'fail' ? 'is-active' : ''}`} onClick={() => showFindings('fail')} type="button"><span>失败</span><strong>{summary.failed}</strong><small>查看并处理失败项</small></button>
                  <button className={`is-warn ${findingFilter === 'warn' ? 'is-active' : ''}`} onClick={() => showFindings('warn')} type="button"><span>人工复核</span><strong>{summary.warnings}</strong><small>查看复核项</small></button>
                  <button className={`is-whitelisted ${findingFilter === 'whitelisted' ? 'is-active' : ''}`} onClick={openWhitelist} type="button"><span>白名单</span><strong>{summary.whitelisted ?? 0}</strong><small>{summary.whitelisted ? '查看或新增' : '点击选择失败项'}</small></button>
                </div>
                <div className="ks-security-scanmeta"><span>{scannerName()}</span><span>{benchmarkLabel(displayedScan.report?.benchmark)}</span><span>CIS 节点 {cisFindingCount} 项 · 集群侧 {clusterFindingCount} 项</span><span>{displayedScan.report?.progress?.message || '报告已生成'}</span></div>
                <ComparisonPanel benchmarkLabel={benchmarkLabel} currentScan={displayedScan} history={history} />
                <div className="ks-security-coverage"><div><strong>已覆盖</strong><p>{coverage.included.join('、') || '未记录'}</p></div><div><strong>明确未覆盖</strong><p>{coverage.not_covered.join('、') || '无'}</p></div></div>
              </>}

              <details className="ks-security-history-fold">
                <summary><span>历史扫描</span><strong>{historyRecords.length} 次</strong><small>展开选择要查看的记录，不展开全部明细</small></summary>
                <div className="ks-security-history-list">
                  {historyRecords.length === 0 ? <p>暂无可查看的历史记录。</p> : historyRecords.map(record => <button className={record.id === viewedScanID ? 'is-selected' : ''} key={record.id ?? `${record.started_at}-${record.status}`} onClick={() => setViewedScanID(record.id ?? null)} type="button">
                    <span><strong>{showTime(record.finished_at ?? record.started_at)}</strong><small>{record.status === 'completed' ? '扫描完成' : record.status === 'failed' ? '扫描失败' : '进行中'} · {benchmarkLabel(record.report?.benchmark)}</small></span>
                    <b>{record.status === 'completed' ? `${record.report?.summary?.score ?? 0}%` : record.status === 'failed' ? '失败' : '进行中'}</b>
                    <em>{record.report?.summary ? `通过 ${record.report.summary.passed} · 失败 ${record.report.summary.failed} · 复核 ${record.report.summary.warnings}` : (record.error || '暂无报告')}</em>
                  </button>)}
                </div>
              </details>

              {findingFilter && <section className="ks-security-findings-panel" ref={findingsRef}>
                <header>
                  <div><h3>{whitelistAddMode ? '选择不合规项加入白名单' : findingFilter === 'all' ? '全部控制项' : findingFilter === 'fail' ? '失败项' : findingFilter === 'warn' ? '人工复核项' : findingFilter === 'whitelisted' ? '白名单项' : '通过项'}</h3><span>{filteredFindings.length} 条</span></div>
                  <div className="ks-security-findings-panel-actions">
                    {findingFilter === 'whitelisted' && !whitelistAddMode && <Button variant="secondary" onClick={() => showFindings('fail', true)}>新增白名单</Button>}
                    {whitelistAddMode && (summary.whitelisted ?? 0) > 0 && <Button variant="link" onClick={() => showFindings('whitelisted')}>返回白名单</Button>}
                    <Button variant="link" onClick={() => { setFindingFilter(''); setWhitelistAddMode(false) }}>收起</Button>
                  </div>
                </header>
                {whitelistAddMode ? <div className="ks-security-whitelist-picklist">
                  {filteredFindings.length === 0 ? <div className="ks-security-empty">当前没有可加入白名单的失败或复核项。</div> : filteredFindings.map((finding, index) => <div className="ks-security-whitelist-pick" key={`${finding.control_id}-${index}`}>
                    <Label color={statusColor[finding.status]} isCompact>{statusName[finding.status]}</Label>
                    <code>{finding.control_id}</code>
                    <span><strong>{finding.title}</strong><small>{finding.category} · 影响 {finding.affected} 个资源</small></span>
                    <Button variant="secondary" onClick={() => setWhitelistTarget(finding)}>加入白名单</Button>
                  </div>)}
                </div> : <div className="ks-security-findings">{filteredFindings.length === 0 ? <div className="ks-security-empty">当前状态没有控制项。</div> : filteredFindings.map((finding, index) => <Finding finding={finding} key={`${finding.control_id}-${index}`} onRemoveWhitelist={finding => void removeWhitelist(finding.control_id)} onWhitelist={setWhitelistTarget} />)}</div>}
              </section>}            </>}
        </div>
      </section>
    </div>, document.body)}

    {whitelistTarget && <Modal aria-labelledby="security-whitelist-title" className="ks-security-whitelist-modal" isOpen onClose={() => { setWhitelistTarget(null); setWhitelistError('') }} variant="small">
      <ModalHeader title={`加入白名单：${whitelistTarget.control_id}`} labelId="security-whitelist-title" description="仅对当前集群的这一个控制项生效，并在后续扫描中标记为豁免。" />
      <ModalBody>
        <p className="ks-security-whitelist-question">{whitelistTarget.title}</p>
        <label className="ks-security-whitelist-reason">豁免原因（必填，至少 3 个字符）<textarea aria-label="白名单原因" autoFocus rows={4} value={whitelistReason} onChange={event => setWhitelistReason(event.target.value)} placeholder="例如：测试环境需要允许 NodePort，已通过边界防火墙限制。" /></label>
        {whitelistError && <Alert isInline title={whitelistError} variant="danger" />}
      </ModalBody>
      <ModalFooter>
        <Button isDisabled={whitelistSaving} isLoading={whitelistSaving} onClick={() => void submitWhitelist()} variant="primary">确认加入</Button>
        <Button isDisabled={whitelistSaving} onClick={() => { setWhitelistTarget(null); setWhitelistError('') }} variant="link">取消</Button>
      </ModalFooter>
    </Modal>}

    {dialogOpen && <SecurityScanDialog
      benchmarks={benchmarks}
      clusters={clusters}
      initialClusterID={dialogClusterID}
      onClose={() => setDialogOpen(false)}
      onScan={launchScans}
    />}
  </div>
}


export default function SecurityBaselinePage() {
  return <SecurityBaselineErrorBoundary><SecurityBaselineContent /></SecurityBaselineErrorBoundary>
}
